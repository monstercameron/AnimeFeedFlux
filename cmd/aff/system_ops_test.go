package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

type cmdSystemClient struct {
	affv1.SystemServiceClient
	stats     func(context.Context, *affv1.SystemServiceStatsRequest, ...grpc.CallOption) (*affv1.SystemServiceStatsResponse, error)
	setGen    func(context.Context, *affv1.SystemServiceSetGenerationEnabledRequest, ...grpc.CallOption) (*affv1.SystemServiceSetGenerationEnabledResponse, error)
	backupRPC func(context.Context, *affv1.SystemServiceBackupRequest, ...grpc.CallOption) (*affv1.SystemServiceBackupResponse, error)
}

func (f *cmdSystemClient) Stats(ctx context.Context, req *affv1.SystemServiceStatsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceStatsResponse, error) {
	return f.stats(ctx, req, opts...)
}

func (f *cmdSystemClient) SetGenerationEnabled(ctx context.Context, req *affv1.SystemServiceSetGenerationEnabledRequest, opts ...grpc.CallOption) (*affv1.SystemServiceSetGenerationEnabledResponse, error) {
	return f.setGen(ctx, req, opts...)
}

func (f *cmdSystemClient) Backup(ctx context.Context, req *affv1.SystemServiceBackupRequest, opts ...grpc.CallOption) (*affv1.SystemServiceBackupResponse, error) {
	return f.backupRPC(ctx, req, opts...)
}

func TestSystemStatsPrintsTheFiguresAnOperatorCameFor(t *testing.T) {
	a, stdout, _ := newTestApp()
	a.clients.System = &cmdSystemClient{
		stats: func(context.Context, *affv1.SystemServiceStatsRequest, ...grpc.CallOption) (*affv1.SystemServiceStatsResponse, error) {
			return &affv1.SystemServiceStatsResponse{
				FeedCount: 4, EnabledFeedCount: 3, ItemCount: 120, DbSizeBytes: 4096,
				TodaySpendUsd: 0.1234, TodayRemainingBudgetUsd: 4.8766, GenerationEnabled: true,
			}, nil
		},
	}
	if code := a.cmdSystemStats(nil); code != exitOK {
		t.Fatalf("exit code = %d", code)
	}
	out := stdout.String()
	for _, want := range []string{"4", "3", "120", "4096", "0.1234", "4.8766", "true"} {
		if !strings.Contains(out, want) {
			t.Errorf("stats output is missing %q:\n%s", want, out)
		}
	}
	// Costs are printed to four decimals: a run can cost fractions of a cent,
	// and two decimals would render most days as $0.00.
	if strings.Contains(out, "$0.12 ") {
		t.Errorf("spend was rounded to cents:\n%s", out)
	}
}

func TestKillSwitchReportsAndFlips(t *testing.T) {
	t.Run("no argument reports the current state", func(t *testing.T) {
		a, stdout, _ := newTestApp()
		a.clients.System = &cmdSystemClient{
			stats: func(context.Context, *affv1.SystemServiceStatsRequest, ...grpc.CallOption) (*affv1.SystemServiceStatsResponse, error) {
				return &affv1.SystemServiceStatsResponse{GenerationEnabled: false}, nil
			},
			setGen: func(context.Context, *affv1.SystemServiceSetGenerationEnabledRequest, ...grpc.CallOption) (*affv1.SystemServiceSetGenerationEnabledResponse, error) {
				t.Error("reading the state flipped the switch")
				return nil, nil
			},
		}
		if code := a.cmdSystemKillSwitch(nil); code != exitOK {
			t.Fatalf("exit code = %d", code)
		}
		if !strings.Contains(stdout.String(), "DISABLED") {
			t.Errorf("output = %q, want it to say generation is disabled", stdout.String())
		}
	})

	for _, tc := range []struct {
		arg  string
		want bool
	}{{"on", true}, {"off", false}} {
		t.Run("kill-switch "+tc.arg, func(t *testing.T) {
			var got *affv1.SystemServiceSetGenerationEnabledRequest
			a, stdout, _ := newTestApp()
			a.clients.System = &cmdSystemClient{
				setGen: func(_ context.Context, req *affv1.SystemServiceSetGenerationEnabledRequest, _ ...grpc.CallOption) (*affv1.SystemServiceSetGenerationEnabledResponse, error) {
					got = req
					return &affv1.SystemServiceSetGenerationEnabledResponse{Enabled: req.GetEnabled()}, nil
				},
			}
			if code := a.cmdSystemKillSwitch([]string{tc.arg}); code != exitOK {
				t.Fatalf("exit code = %d", code)
			}
			if got.GetEnabled() != tc.want {
				t.Errorf("sent enabled=%v for %q, want %v", got.GetEnabled(), tc.arg, tc.want)
			}
			// The confirmation must state the resulting state, not just
			// "done": this is the control that stops all spending, and an
			// operator running it at 4am needs to read back what is now true.
			if !strings.Contains(stdout.String(), "now") {
				t.Errorf("output = %q, want it to state the new state", stdout.String())
			}
		})
	}

	t.Run("anything else is a usage error", func(t *testing.T) {
		for _, arg := range []string{"true", "enable", "ON!"} {
			a, _, stderr := newTestApp()
			a.clients.System = &cmdSystemClient{
				setGen: func(context.Context, *affv1.SystemServiceSetGenerationEnabledRequest, ...grpc.CallOption) (*affv1.SystemServiceSetGenerationEnabledResponse, error) {
					t.Errorf("%q was treated as a valid argument", arg)
					return nil, nil
				},
			}
			if code := a.cmdSystemKillSwitch([]string{arg}); code != exitUsage {
				t.Errorf("%q exited %d, want exitUsage", arg, code)
			}
			if stderr.Len() == 0 {
				t.Errorf("%q produced no message", arg)
			}
		}
		a, _, _ := newTestApp()
		a.clients.System = &cmdSystemClient{}
		if code := a.cmdSystemKillSwitch([]string{"on", "off"}); code != exitUsage {
			t.Errorf("two arguments exited %d, want exitUsage", code)
		}
	})
}

func TestSystemBackupWritesTheBytesItWasGiven(t *testing.T) {
	// The payload is a database file. Anything that transformed it — a text
	// mode write, a truncation, printing it to stdout — produces a backup
	// that only fails when someone tries to restore it.
	payload := []byte{0x53, 0x51, 0x4c, 0x69, 0x74, 0x65, 0x00, 0xff, 0x00}
	out := filepath.Join(t.TempDir(), "backup.db")

	a, stdout, _ := newTestApp()
	a.clients.System = &cmdSystemClient{
		backupRPC: func(context.Context, *affv1.SystemServiceBackupRequest, ...grpc.CallOption) (*affv1.SystemServiceBackupResponse, error) {
			return &affv1.SystemServiceBackupResponse{DbFile: payload, Filename: "aff-2026-08-11.db"}, nil
		},
	}
	if code := a.cmdSystemBackup([]string{"--out", out}); code != exitOK {
		t.Fatalf("exit code = %d", code)
	}
	written, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading the backup: %v", err)
	}
	if string(written) != string(payload) {
		t.Errorf("backup bytes = %v, want %v", written, payload)
	}
	if !strings.Contains(stdout.String(), "aff-2026-08-11.db") {
		t.Errorf("output does not name the server-side file: %q", stdout.String())
	}

	// The mode matters: a backup readable by every user on the box is a copy
	// of every session hash and the whole content database.
	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if runtimeIsUnix() && info.Mode().Perm() != 0o600 {
		t.Errorf("backup mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestSystemBackupRequiresAnOutPath(t *testing.T) {
	// Without --out the only alternative is stdout, and a binary database on
	// a terminal is at best noise and at worst a mangled "backup" someone
	// redirected to a file.
	a, _, stderr := newTestApp()
	a.clients.System = &cmdSystemClient{
		backupRPC: func(context.Context, *affv1.SystemServiceBackupRequest, ...grpc.CallOption) (*affv1.SystemServiceBackupResponse, error) {
			t.Error("the backup RPC ran with nowhere to write the result")
			return nil, nil
		},
	}
	if code := a.cmdSystemBackup(nil); code != exitUsage {
		t.Errorf("exit code = %d, want exitUsage", code)
	}
	if stderr.Len() == 0 {
		t.Error("nothing on stderr")
	}
}

func TestSystemBackupReportsAnUnwritablePath(t *testing.T) {
	a, _, stderr := newTestApp()
	a.clients.System = &cmdSystemClient{
		backupRPC: func(context.Context, *affv1.SystemServiceBackupRequest, ...grpc.CallOption) (*affv1.SystemServiceBackupResponse, error) {
			return &affv1.SystemServiceBackupResponse{DbFile: []byte("x")}, nil
		},
	}
	// A directory that does not exist: a silent success here would leave an
	// operator believing they have a backup.
	bad := filepath.Join(t.TempDir(), "no-such-dir", "backup.db")
	if code := a.cmdSystemBackup([]string{"--out", bad}); code != exitFail {
		t.Errorf("exit code = %d, want exitFail", code)
	}
	if stderr.Len() == 0 {
		t.Error("nothing on stderr")
	}
}

func TestFormatOrEmptyLeavesAZeroTimeBlank(t *testing.T) {
	// "never run" must render as an empty cell, not as year 1 — a table
	// showing 0001-01-01 reads as corruption.
	if got := formatOrEmpty(time.Time{}); got != "" {
		t.Errorf("formatOrEmpty(zero) = %q, want empty", got)
	}
	when := time.Date(2026, 8, 11, 14, 30, 0, 0, time.FixedZone("EDT", -4*3600))
	got := formatOrEmpty(when)
	if !strings.HasSuffix(got, "Z") {
		t.Errorf("formatOrEmpty = %q, want a UTC (Z) timestamp so two rows are comparable", got)
	}
	if !strings.HasPrefix(got, "2026-08-11T18:30") {
		t.Errorf("formatOrEmpty = %q, want the UTC instant", got)
	}
}

// runtimeIsUnix reports whether file permission bits are meaningful here.
// Windows does not honour 0600 on a plain file, so asserting it there would
// fail for a reason that has nothing to do with this code.
func runtimeIsUnix() bool {
	return os.PathSeparator == '/'
}
