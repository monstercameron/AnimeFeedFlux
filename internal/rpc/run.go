// RunServer implements aff.v1.RunService (PLAN.md §11, §12.4): History, Get,
// Watch and Delete over the `runs` table written by internal/store/runs.go.
//
// There is deliberately no Update here, matching the proto surface: a run
// records something that happened, and editing it would make the cost and
// drift numbers lie (§12.4). Delete is allowed — it prunes noise and leaves
// no dangling data, because items.run_id is ON DELETE SET NULL (migrations
// 0002): a deleted run detaches from its items, it does not take them with
// it, so the published feed is unaffected by pruning run history.
//
// This file talks to SQLite directly through store.Store's exported
// Writer()/Reader() handles rather than adding new methods to package store,
// because internal/store/runs.go's ListRuns supports only a feed filter and
// no pagination, no status/date filters and no cursor — History needs all
// four (§12.4: "Filter by feed, status, date range") and this package may
// not edit internal/store. Helper names are prefixed `run` per the sibling
// package convention (auth.go/feed.go/item.go/sample.go use their own
// prefixes) so two agents editing this package concurrently do not collide.
package rpc

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// runTimeLayout mirrors internal/store's unexported timeLayout (items.go:
// "the one format every timestamp column uses: RFC3339Nano, UTC"). It must
// match exactly, because History filters by comparing this package's
// formatted bound against store-written TEXT columns with plain SQL
// operators — a mismatched layout would silently filter nothing or
// everything.
const runTimeLayout = time.RFC3339Nano

func runFormatTime(t time.Time) string { return t.UTC().Format(runTimeLayout) }

func runParseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(runTimeLayout, s)
}

// runColumnsSQL mirrors internal/store/runs.go's unexported runColumns
// exactly (same table, migrations/0002_feeds_items.sql `runs`), duplicated
// here because this package cannot import an unexported constant and may
// not edit internal/store to export one.
const runColumnsSQL = `id, feed_id, started_at, finished_at, status, error_kind, error,
	items_added, items_rejected, reject_reasons_json, tokens_in, tokens_out, est_cost_usd,
	trigger, lock_holder, heartbeat_at`

// runRow is the scan target for runColumnsSQL, kept separate from
// store.Run so this package has no dependency on that unexported shape.
type runRow struct {
	id                               int64
	feedID                           int64
	startedAt, finishedAt            string
	status, errorKind, errText       string
	itemsAdded, itemsRejected        int
	reasonsJSON                      string
	tokensIn, tokensOut              int
	estCostUSD                       float64
	trigger, lockHolder, heartbeatAt string
}

// runRowScanner is satisfied by both *sql.Row and *sql.Rows.
type runRowScanner interface {
	Scan(dest ...any) error
}

func runScanRow(sc runRowScanner) (runRow, error) {
	var (
		r                                           runRow
		finishedAt, errorKind, errText, heartbeatAt sql.NullString
		lockHolder                                  sql.NullString
	)
	if err := sc.Scan(
		&r.id, &r.feedID, &r.startedAt, &finishedAt, &r.status, &errorKind, &errText,
		&r.itemsAdded, &r.itemsRejected, &r.reasonsJSON, &r.tokensIn, &r.tokensOut, &r.estCostUSD,
		&r.trigger, &lockHolder, &heartbeatAt,
	); err != nil {
		return runRow{}, err
	}
	r.finishedAt = finishedAt.String
	r.errorKind = errorKind.String
	r.errText = errText.String
	r.lockHolder = lockHolder.String
	r.heartbeatAt = heartbeatAt.String
	return r, nil
}

// runIsTerminal reports whether a store run status is one after which a
// Watch stream must end (PLAN.md §22 J9: "the stream terminates when the run
// does, in every branch including failure"). Every status but 'running' is
// terminal, including the two crash-recovery statuses
// ReclaimStaleRuns produces (internal/store/runs.go) that have no dedicated
// RunStatus enum value.
func runIsTerminal(storeStatus string) bool { return storeStatus != "running" }

// runStatusToProto maps a store status string onto the RunStatus enum.
// 'interrupted' and 'completed_unconfirmed' — both written only by
// ReclaimStaleRuns's boot watchdog (§15) — have no enum value of their own,
// so they fold into FAILED; runToProto preserves the original word in the
// Run's error field so History still tells them apart (§22 J5: "read
// status, error kind, and reject reasons").
func runStatusToProto(storeStatus string) affv1.RunStatus {
	switch storeStatus {
	case "running":
		return affv1.RunStatus_RUN_STATUS_RUNNING
	case "success":
		return affv1.RunStatus_RUN_STATUS_SUCCEEDED
	case "failed", "interrupted", "completed_unconfirmed":
		return affv1.RunStatus_RUN_STATUS_FAILED
	case "skipped":
		return affv1.RunStatus_RUN_STATUS_SKIPPED
	default:
		return affv1.RunStatus_RUN_STATUS_UNSPECIFIED
	}
}

func runProtoStatusToStore(s affv1.RunStatus) []string {
	switch s {
	case affv1.RunStatus_RUN_STATUS_RUNNING:
		return []string{"running"}
	case affv1.RunStatus_RUN_STATUS_SUCCEEDED:
		return []string{"success"}
	case affv1.RunStatus_RUN_STATUS_FAILED:
		// Also matches the two crash-recovery statuses folded into FAILED by
		// runStatusToProto, so filtering History by FAILED finds them too.
		return []string{"failed", "interrupted", "completed_unconfirmed"}
	case affv1.RunStatus_RUN_STATUS_SKIPPED:
		return []string{"skipped"}
	default:
		return nil
	}
}

func runErrorKindToProto(k string) affv1.ErrorKind {
	switch k {
	case "transient":
		return affv1.ErrorKind_ERROR_KIND_TRANSIENT
	case "invalid":
		return affv1.ErrorKind_ERROR_KIND_INVALID
	case "fatal":
		return affv1.ErrorKind_ERROR_KIND_FATAL
	default:
		return affv1.ErrorKind_ERROR_KIND_UNSPECIFIED
	}
}

// runReasonsFromJSON decodes reject_reasons_json into a deterministically
// ordered slice (sorted by reason) so repeated calls and tests are stable
// regardless of Go's randomized map iteration order.
func runReasonsFromJSON(js string) ([]*affv1.RejectReason, error) {
	if js == "" {
		js = "{}"
	}
	var m map[string]int
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		return nil, fmt.Errorf("rpc: parsing reject_reasons_json: %w", err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]*affv1.RejectReason, 0, len(keys))
	for _, k := range keys {
		out = append(out, &affv1.RejectReason{Reason: k, Count: int32(m[k])})
	}
	return out, nil
}

// runToProto converts one runs row into the wire Run message.
func runToProto(r runRow) (*affv1.Run, error) {
	reasons, err := runReasonsFromJSON(r.reasonsJSON)
	if err != nil {
		return nil, err
	}

	startedAt, err := runParseTime(r.startedAt)
	if err != nil {
		return nil, fmt.Errorf("rpc: parsing run %d started_at: %w", r.id, err)
	}
	finishedAt, err := runParseTime(r.finishedAt)
	if err != nil {
		return nil, fmt.Errorf("rpc: parsing run %d finished_at: %w", r.id, err)
	}
	heartbeatAt, err := runParseTime(r.heartbeatAt)
	if err != nil {
		return nil, fmt.Errorf("rpc: parsing run %d heartbeat_at: %w", r.id, err)
	}

	errText := r.errText
	if r.status == "interrupted" || r.status == "completed_unconfirmed" {
		// See runStatusToProto: these two store statuses fold into FAILED,
		// so the raw word is preserved here or History can no longer tell a
		// genuine provider failure apart from a crashed process (§22 J5).
		if errText == "" {
			errText = r.status
		} else {
			errText = fmt.Sprintf("%s: %s", r.status, errText)
		}
	}

	out := &affv1.Run{
		Id:            r.id,
		FeedId:        r.feedID,
		Trigger:       runTriggerToProto(r.trigger),
		Status:        runStatusToProto(r.status),
		ErrorKind:     runErrorKindToProto(r.errorKind),
		Error:         errText,
		ItemsAdded:    int32(r.itemsAdded),
		ItemsRejected: int32(r.itemsRejected),
		RejectReasons: reasons,
		TokensIn:      int32(r.tokensIn),
		TokensOut:     int32(r.tokensOut),
		// EstCostUsd is an ESTIMATE, not authoritative (proto doc on
		// Run.est_cost_usd, PLAN.md §8.1/§13): SchemaFlux reports no usage or
		// cost, so this is our own token-boundary estimate. The field name
		// and its proto comment are the label; nothing here may present it
		// as measured.
		EstCostUsd: r.estCostUSD,
	}
	if !startedAt.IsZero() {
		out.StartedAt = timestamppb.New(startedAt)
	}
	if !finishedAt.IsZero() {
		out.FinishedAt = timestamppb.New(finishedAt)
	}
	if !heartbeatAt.IsZero() {
		out.HeartbeatAt = timestamppb.New(heartbeatAt)
	}
	return out, nil
}

func runTriggerToProto(t string) affv1.RunTrigger {
	switch t {
	case "cron":
		return affv1.RunTrigger_RUN_TRIGGER_CRON
	case "manual":
		return affv1.RunTrigger_RUN_TRIGGER_MANUAL
	default:
		return affv1.RunTrigger_RUN_TRIGGER_UNSPECIFIED
	}
}

// runDefaultPageSize and runMaxPageSize bound History's page_size (§11:
// "list RPCs paginate with an opaque cursor").
const (
	runDefaultPageSize = 50
	runMaxPageSize     = 200
)

// runEncodeCursor/runDecodeCursor make the page token opaque to the client
// (§11) while staying a plain run id internally: History orders newest-first
// by id (ties with started_at never happen because StartRun stamps both in
// the same INSERT), so "the next page" is simply "id < cursor".
func runEncodeCursor(id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(id, 10)))
}

func runDecodeCursor(token string) (int64, error) {
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(string(b), 10, 64)
}

// RunServer implements affv1.RunServiceServer.
type RunServer struct {
	affv1.UnimplementedRunServiceServer

	st  *store.Store
	log *slog.Logger

	// watchPoll is how often Watch re-reads the run row. A field (not a
	// literal) so tests can shrink it instead of sleeping through the
	// production interval.
	watchPoll time.Duration
}

// defaultWatchPoll is production's Watch polling interval. There is no
// pub/sub for run progress (§10 has no notification mechanism), so Watch
// polls the row like any other reader — cheap, because runs.id is a rowid
// primary key lookup and Watch only exists while a run is in flight.
const defaultWatchPoll = 500 * time.Millisecond

// NewRunServer wires a RunServer against an open store. log defaults to a
// discarding logger if nil, matching store.Open's own convention.
func NewRunServer(st *store.Store, log *slog.Logger) *RunServer {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &RunServer{st: st, log: log, watchPoll: defaultWatchPoll}
}

// History paginates runs newest-first, filterable by feed, status and
// started_at range (§12.4).
func (s *RunServer) History(ctx context.Context, req *affv1.RunServiceHistoryRequest) (*affv1.RunServiceHistoryResponse, error) {
	pageSize := int(req.GetPageSize())
	if pageSize <= 0 {
		pageSize = runDefaultPageSize
	}
	if pageSize > runMaxPageSize {
		pageSize = runMaxPageSize
	}

	var (
		conds []string
		args  []any
	)
	if tok := req.GetPageToken(); tok != "" {
		cursor, err := runDecodeCursor(tok)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "rpc: invalid page_token")
		}
		conds = append(conds, "id < ?")
		args = append(args, cursor)
	}
	if fid := req.GetFeedId(); fid != 0 {
		conds = append(conds, "feed_id = ?")
		args = append(args, fid)
	}
	if statuses := runProtoStatusToStore(req.GetStatus()); statuses != nil {
		placeholders := make([]string, len(statuses))
		for i, st := range statuses {
			placeholders[i] = "?"
			args = append(args, st)
		}
		conds = append(conds, "status IN ("+strings.Join(placeholders, ",")+")")
	}
	if req.GetStartedAfter() != nil {
		conds = append(conds, "started_at >= ?")
		args = append(args, runFormatTime(req.GetStartedAfter().AsTime()))
	}
	if req.GetStartedBefore() != nil {
		conds = append(conds, "started_at <= ?")
		args = append(args, runFormatTime(req.GetStartedBefore().AsTime()))
	}

	query := "SELECT " + runColumnsSQL + " FROM runs"
	if len(conds) > 0 {
		query += " WHERE " + strings.Join(conds, " AND ")
	}
	// Order by id DESC, not started_at DESC: StartRun (internal/store/runs.go)
	// assigns both in the same INSERT, so id order and start order always
	// agree, and id gives an exact, tie-free "newest first" cursor boundary
	// that a same-second started_at could not.
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, pageSize+1) // +1 to detect a next page without a COUNT(*)

	rows, err := s.st.Reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: listing run history: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*affv1.Run
	var lastIncludedID int64
	for rows.Next() {
		r, err := runScanRow(rows)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "rpc: scanning run: %v", err)
		}
		if len(out) == pageSize {
			// This is the lookahead row fetched by LIMIT pageSize+1; it
			// proves a next page exists but is not itself returned. The
			// cursor must be the LAST ROW ACTUALLY RETURNED, not this
			// lookahead row's id — using the lookahead id here would make
			// "id < cursor" skip it on the next page too, silently dropping
			// exactly one run per page boundary.
			return &affv1.RunServiceHistoryResponse{
				Runs:          out,
				NextPageToken: runEncodeCursor(lastIncludedID),
			}, nil
		}
		p, err := runToProto(r)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "rpc: converting run: %v", err)
		}
		out = append(out, p)
		lastIncludedID = r.id
	}
	if err := rows.Err(); err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: iterating run history: %v", err)
	}
	return &affv1.RunServiceHistoryResponse{Runs: out}, nil
}

// runGetByID reads a single run row, or codes.NotFound if it does not exist.
func runGetByID(ctx context.Context, r store.Reader, runID int64) (runRow, error) {
	row := r.QueryRowContext(ctx, "SELECT "+runColumnsSQL+" FROM runs WHERE id = ?", runID)
	rr, err := runScanRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return runRow{}, status.Errorf(codes.NotFound, "rpc: run %d not found", runID)
		}
		return runRow{}, status.Errorf(codes.Internal, "rpc: reading run %d: %v", runID, err)
	}
	return rr, nil
}

// Get returns one run plus its log. Log is always empty: nothing in this
// schema persists per-run log text (migrations/0002_feeds_items.sql's runs
// table has no log column, and there is no separate log table) — run
// progress is structured JSON to stdout keyed by run_id (§15.0), not a
// column this RPC can join against. Adding that persistence is out of this
// file's scope (internal/rpc/run.go, internal/rpc/system.go only).
func (s *RunServer) Get(ctx context.Context, req *affv1.RunServiceGetRequest) (*affv1.RunServiceGetResponse, error) {
	rr, err := runGetByID(ctx, s.st.Reader(), req.GetRunId())
	if err != nil {
		return nil, err
	}
	p, err := runToProto(rr)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: converting run: %v", err)
	}
	return &affv1.RunServiceGetResponse{Run: p}, nil
}

// Watch streams a run's progress by polling until it reaches a terminal
// status, then returns — in EVERY branch, success or failure (§22 J9: "the
// stream terminates when the run does, in every branch including failure").
//
// A dropped client is detected via stream.Context() being done and simply
// stops the loop; nothing here ever touches the run row on that path,
// because the run is not the stream's to cancel (§22 J9: "a dropped socket
// does not abort the run").
func (s *RunServer) Watch(req *affv1.RunServiceWatchRequest, stream grpc.ServerStreamingServer[affv1.RunServiceWatchResponse]) error {
	ctx := stream.Context()
	ticker := time.NewTicker(s.watchPoll)
	defer ticker.Stop()

	for {
		// Checked before every read, not only in the select below: the
		// ticker and ctx.Done() can both be ready in the same instant (the
		// test's cancel() racing a poll tick is a real, reachable case, not
		// just a test artifact), and select picks between ready cases at
		// random. Without this, a read that loses that race hits SQLite
		// with an already-canceled context and returns a spurious error
		// instead of the clean "client is gone" termination §22 J9 asks
		// for.
		if ctx.Err() != nil {
			return nil
		}

		rr, err := runGetByID(ctx, s.st.Reader(), req.GetRunId())
		if err != nil {
			if ctx.Err() != nil {
				// The read failed because the client disconnected, not
				// because anything about the run is wrong — same clean
				// termination as the check above.
				return nil
			}
			return err
		}
		p, err := runToProto(rr)
		if err != nil {
			return status.Errorf(codes.Internal, "rpc: converting run: %v", err)
		}
		if err := stream.Send(&affv1.RunServiceWatchResponse{Run: p}); err != nil {
			// The client is gone. Return without touching the run — it keeps
			// running to completion regardless of who is watching (§22 J9).
			return err
		}
		if runIsTerminal(rr.status) {
			return nil
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// Delete removes a run's history row. No expected_version: `runs` has no
// version column (PLAN.md §10), and a historical row is not a "recipe" two
// tabs can silently clobber in the §11 sense. Deleting the run detaches its
// items (items.run_id ON DELETE SET NULL) rather than removing them — the
// published feed is untouched by pruning history (§12.4: Delete "prunes
// noise" over items that already left the run behind).
func (s *RunServer) Delete(ctx context.Context, req *affv1.RunServiceDeleteRequest) (*affv1.RunServiceDeleteResponse, error) {
	res, err := s.st.Writer().ExecContext(ctx, "DELETE FROM runs WHERE id = ?", req.GetRunId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: deleting run %d: %v", req.GetRunId(), err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: rows affected deleting run %d: %v", req.GetRunId(), err)
	}
	if n == 0 {
		return nil, status.Errorf(codes.NotFound, "rpc: run %d not found", req.GetRunId())
	}
	return &affv1.RunServiceDeleteResponse{}, nil
}
