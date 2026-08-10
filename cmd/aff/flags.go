package main

import "flag"

// newFlagSet builds a flag.FlagSet pre-registered with the flags every
// command accepts (dispatch.go's usage text: "accepted by every command"),
// bound directly to the app's fields so a per-invocation override (`aff
// --server ... feed list`) takes effect before that command dials.
// ContinueOnError plus a Stderr-directed output means a bad flag prints its
// own message and this returns a distinguishable error rather than calling
// os.Exit itself (flag.ExitOnError would bypass this CLI's own exit-code
// conventions).
func (a *app) newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	fs.StringVar(&a.Server, "server", a.Server, "admin gRPC address (default $AFF_ADMIN_ADDR)")
	fs.StringVar(&a.SessionFile, "session-file", a.SessionFile, "local session file path")
	fs.BoolVar(&a.JSON, "json", a.JSON, "machine-readable JSON output")
	return fs
}
