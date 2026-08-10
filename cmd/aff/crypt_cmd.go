// aff encrypt / aff decrypt — the off-box shipping step PLAN.md §15
// requires between "backup taken" and "backup safe": a nightly VACUUM INTO
// snapshot that never leaves the droplet defends against `rm`, not against
// losing the volume, and the ArticleFlux incident this design cites (§15) is
// exactly what happens when the key lives next to what it protects. See
// internal/ops/cli.go's EncryptionKeyEnv doc comment for why the key is
// read only from the environment and there is deliberately no --key flag.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/monstercameron/AnimeFeedFlux/internal/ops"
)

func (a *app) cmdEncrypt(args []string) int {
	return a.cmdCrypt(args, "aff encrypt", ops.Encrypt)
}

func (a *app) cmdDecrypt(args []string) int {
	return a.cmdCrypt(args, "aff decrypt", ops.Decrypt)
}

// cmdCrypt is the one place that opens files, checks for an existing --out,
// and reports the result — encrypt and decrypt differ only in which of
// ops.Encrypt/ops.Decrypt they pass in, both of which share this signature.
func (a *app) cmdCrypt(args []string, name string, fn func(in io.Reader, out io.Writer, key []byte) error) int {
	fs := a.newFlagSet(name)
	in := fs.String("in", "", "input file (required)")
	out := fs.String("out", "", "output file (required)")
	yes := fs.Bool("yes", false, "overwrite --out if it already exists, without asking")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(a.Stderr, "%s: takes no positional arguments, use --in and --out\n", name)
		return exitUsage
	}
	if *in == "" || *out == "" {
		fmt.Fprintf(a.Stderr, "%s: --in and --out are both required\n", name)
		return exitUsage
	}

	key, err := ops.LoadEncryptionKey()
	if err != nil {
		fmt.Fprintf(a.Stderr, "%s: %v\n", name, err)
		return exitFail
	}

	if _, statErr := os.Stat(*out); statErr == nil {
		ok, err := a.confirm(*yes, fmt.Sprintf("%s already exists. Overwrite it?", *out))
		if err != nil {
			fmt.Fprintf(a.Stderr, "%s: %v\n", name, err)
			return exitFail
		}
		if !ok {
			fmt.Fprintf(a.Stderr, "%s: aborted, %s was not touched\n", name, *out)
			return exitFail
		}
	}

	inFile, err := os.Open(*in)
	if err != nil {
		fmt.Fprintf(a.Stderr, "%s: opening %s: %v\n", name, *in, err)
		return exitFail
	}
	defer func() { _ = inFile.Close() }()

	// 0600: the plaintext side of this operation (the decrypted database, or
	// the not-yet-shipped ciphertext) is exactly the kind of file that must
	// not be group/world readable — same reasoning as session.go's session
	// file and admin_cmd.go's DB access.
	outFile, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		fmt.Fprintf(a.Stderr, "%s: creating %s: %v\n", name, *out, err)
		return exitFail
	}

	if err := fn(inFile, outFile, key); err != nil {
		_ = outFile.Close()
		fmt.Fprintf(a.Stderr, "%s: %v\n", name, err)
		return exitFail
	}
	if err := outFile.Close(); err != nil {
		fmt.Fprintf(a.Stderr, "%s: closing %s: %v\n", name, *out, err)
		return exitFail
	}

	fi, err := os.Stat(*out)
	var size int64
	if err == nil {
		size = fi.Size()
	}
	if a.JSON {
		_ = a.printJSON(map[string]any{"in": *in, "out": *out, "bytes": size})
	} else {
		fmt.Fprintf(a.Stdout, "wrote:  %s\n", *out)
		fmt.Fprintf(a.Stdout, "size:   %d bytes (%s)\n", size, humanBytes(size))
	}
	return exitOK
}
