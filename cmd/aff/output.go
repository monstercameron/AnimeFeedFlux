package main

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// protoJSON marshals a proto message the way a human piping `--json` output
// wants it: enum fields as their string names (FEED_KIND_GENERATIVE, not 1)
// and field names in the same snake_case the proto uses, which encoding/json
// cannot do on a generated struct on its own — protoc-gen-go does not emit a
// MarshalJSON for open-enum fields, so a plain json.Marshal would print raw
// integers (PLAN.md's proto messages lean on enums throughout: FeedKind,
// Origin, RunStatus, ...).
var protoJSONOpts = protojson.MarshalOptions{
	Multiline:       true,
	Indent:          "  ",
	EmitUnpopulated: true,
	UseProtoNames:   true,
}

func (a *app) printProtoJSON(msg proto.Message) error {
	b, err := protoJSONOpts.Marshal(msg)
	if err != nil {
		return fmt.Errorf("aff: encoding response as JSON: %w", err)
	}
	_, err = fmt.Fprintln(a.Stdout, string(b))
	return err
}

// printJSON is for CLI-native values (session status, confirmations) that
// have no proto counterpart, where plain encoding/json is exactly right.
func (a *app) printJSON(v any) error {
	enc := json.NewEncoder(a.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// newTabWriter is the shared human-readable table format for `list`/`runs`
// output: two-space minimum column gap, no padding character games.
func newTabWriter(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
}
