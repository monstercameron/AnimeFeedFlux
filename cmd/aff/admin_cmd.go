package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// recoveryCodeCount matches no fixed number in PLAN.md §4/§12.2 beyond
// "generated once at enrollment" and the dashboard nagging "at <=2
// remaining" — 10 gives headroom above that threshold without the operator
// needing to regenerate on a normal cadence.
const recoveryCodeCount = 10

// totpAccountName and totpIssuer label the otpauth:// URI shown in an
// authenticator app. There is exactly one admin account (PLAN.md §4), so
// these are constants rather than derived from anything.
const (
	totpAccountName = "admin"
	totpIssuer      = "AnimeFeedFlux"
)

// cmdAdmin dispatches `aff admin init|reset` — see admin_cmd.go's package
// doc comment (app.go) for why these two, alone, bypass gRPC entirely.
func (a *app) cmdAdmin(args []string) int {
	if len(args) == 0 {
		return a.missingSubcommand("admin", "init", "reset")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "init":
		return a.cmdAdminInit(rest)
	case "reset":
		return a.cmdAdminReset(rest)
	default:
		return a.unknownSubcommand("admin", sub, "init", "reset")
	}
}

// openLocalStore is the shared "local-only" gate for both admin
// subcommands: neither may proceed without direct filesystem access to the
// SQLite file, and both must say so plainly rather than failing with a bare
// driver error when, say, AFF_DB_PATH points at a path that does not exist
// on this machine (PLAN.md §11: "aff admin init and aff admin reset ... are
// the only local-only commands, and require filesystem access to the DB").
func (a *app) openLocalStore(ctx context.Context) (*store.Store, error) {
	if a.DBPath == "" {
		return nil, errors.New("aff admin: this command is LOCAL-ONLY and requires --db or AFF_DB_PATH " +
			"pointing at the SQLite file on this machine; it does not talk to the server over gRPC")
	}
	st, err := store.Open(ctx, store.Options{Path: a.DBPath})
	if err != nil {
		return nil, fmt.Errorf("aff admin: cannot open the database locally at %q (this command requires "+
			"direct filesystem access, not a running server): %w", a.DBPath, err)
	}
	if _, err := st.Migrate(ctx); err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("aff admin: migrating local database: %w", err)
	}
	return st, nil
}

// secretKey reads AFF_SECRET_KEY directly rather than through
// internal/config.Load, because admin init/reset must work when the rest of
// the server's environment (AFF_PUBLIC_BASE_URL, AFF_ALLOWED_ORIGINS, ...)
// is not configured at all yet — that is the whole point of these two
// commands being local-only recovery paths (PLAN.md §12.2).
func secretKey() ([]byte, error) {
	v := os.Getenv("AFF_SECRET_KEY")
	if v == "" {
		return nil, errors.New("aff admin: AFF_SECRET_KEY is not set; it is required to encrypt the " +
			"TOTP secret at rest (PLAN.md §4)")
	}
	return []byte(v), nil
}

// adminEnrollment is what init/reset print: a QR-code URI and recovery
// codes, both "shown once, stored hashed" (PLAN.md §4). Neither is
// retrievable again after this call returns, so both --json and the
// human-readable form show them in full rather than truncating.
type adminEnrollment struct {
	ProvisioningURI string   `json:"provisioning_uri"`
	RecoveryCodes   []string `json:"recovery_codes"`
}

func (a *app) printEnrollment(heading string, e adminEnrollment) {
	if a.JSON {
		_ = a.printJSON(e)
		return
	}
	fmt.Fprintln(a.Stdout, heading)
	fmt.Fprintln(a.Stdout, "\nScan this URI with an authenticator app (shown once):")
	fmt.Fprintln(a.Stdout, "  "+e.ProvisioningURI)
	fmt.Fprintln(a.Stdout, "\nRecovery codes (shown once, store them somewhere safe):")
	for _, c := range e.RecoveryCodes {
		fmt.Fprintln(a.Stdout, "  "+c)
	}
}

// cmdAdminInit implements `aff admin init`: create the singleton admin row,
// refusing a weak password (auth.IsWeak) and refusing to run at all if an
// admin already exists (store.ErrAdminExists) — see store.ErrAdminExists
// for why that is a refusal rather than an overwrite.
func (a *app) cmdAdminInit(args []string) int {
	fs := a.newFlagSet("aff admin init")
	fs.StringVar(&a.DBPath, "db", a.DBPath, "path to the SQLite database file (local-only)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(a.Stderr, "aff admin init: takes no positional arguments")
		return exitUsage
	}

	ctx := context.Background()

	// The local-DB-access gate comes before anything else, including the
	// AFF_SECRET_KEY check below: "cannot reach this database at all" is a
	// different, more fundamental failure than "the database is reachable
	// but the environment is otherwise incomplete", and the local-only
	// message must be the one an operator sees first.
	st, err := a.openLocalStore(ctx)
	if err != nil {
		fmt.Fprintln(a.Stderr, err)
		return exitFail
	}
	defer func() { _ = st.Close() }()

	key, err := secretKey()
	if err != nil {
		fmt.Fprintln(a.Stderr, err)
		return exitFail
	}

	if _, err := st.GetAdmin(ctx); err == nil {
		fmt.Fprintln(a.Stderr, "aff admin init: an admin account already exists; refusing to overwrite it "+
			"(use `aff admin reset` if you are locked out)")
		return exitFail
	} else if !errors.Is(err, store.ErrNotFound) {
		fmt.Fprintf(a.Stderr, "aff admin init: checking for an existing admin: %v\n", err)
		return exitFail
	}

	password, err := a.readPassword("New admin password: ")
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff admin init: reading password: %v\n", err)
		return exitFail
	}
	if err := auth.IsWeak(password); err != nil {
		fmt.Fprintf(a.Stderr, "aff admin init: %v\n", err)
		return exitFail
	}

	hash, err := auth.Hash(password, auth.DefaultParams())
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff admin init: hashing password: %v\n", err)
		return exitFail
	}
	kdfParams, err := json.Marshal(auth.DefaultParams())
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff admin init: encoding KDF params: %v\n", err)
		return exitFail
	}

	if err := st.InitAdmin(ctx, hash, string(kdfParams)); err != nil {
		fmt.Fprintf(a.Stderr, "aff admin init: %v\n", err)
		return exitFail
	}

	secret, provisioningURI, err := auth.Enroll(totpAccountName, totpIssuer)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff admin init: enrolling TOTP: %v\n", err)
		return exitFail
	}
	encSecret, err := auth.EncryptSecret(secret, key)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff admin init: encrypting TOTP secret: %v\n", err)
		return exitFail
	}
	if err := st.SetTOTPSecret(ctx, encSecret); err != nil {
		fmt.Fprintf(a.Stderr, "aff admin init: storing TOTP secret: %v\n", err)
		return exitFail
	}

	plainCodes, hashedCodes, err := auth.GenerateCodes(recoveryCodeCount)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff admin init: generating recovery codes: %v\n", err)
		return exitFail
	}
	if err := st.StoreRecoveryCodes(ctx, hashedCodes); err != nil {
		fmt.Fprintf(a.Stderr, "aff admin init: storing recovery codes: %v\n", err)
		return exitFail
	}

	a.printEnrollment("Admin account created.", adminEnrollment{
		ProvisioningURI: provisioningURI,
		RecoveryCodes:   plainCodes,
	})
	return exitOK
}

// cmdAdminReset implements `aff admin reset`: the break-glass path (PLAN.md
// §12.2, "one of only two ways back in when locked out"). It resets the
// password AND re-enrolls TOTP AND regenerates recovery codes — a partial
// reset would leave the operator still locked out by whichever factor it
// didn't touch, which defeats the point of a break-glass command. It also
// revokes every existing session, matching the same rule the RPC recovery
// path uses (PLAN.md §12.2: "forces a full re-login and revokes every other
// session"), since a reset that leaves old sessions alive has not actually
// locked anyone out.
func (a *app) cmdAdminReset(args []string) int {
	fs := a.newFlagSet("aff admin reset")
	fs.StringVar(&a.DBPath, "db", a.DBPath, "path to the SQLite database file (local-only)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(a.Stderr, "aff admin reset: takes no positional arguments")
		return exitUsage
	}

	ctx := context.Background()

	// See cmdAdminInit's identical ordering comment: the local-DB-access gate
	// must be the first thing checked.
	st, err := a.openLocalStore(ctx)
	if err != nil {
		fmt.Fprintln(a.Stderr, err)
		return exitFail
	}
	defer func() { _ = st.Close() }()

	key, err := secretKey()
	if err != nil {
		fmt.Fprintln(a.Stderr, err)
		return exitFail
	}

	if _, err := st.GetAdmin(ctx); errors.Is(err, store.ErrNotFound) {
		fmt.Fprintln(a.Stderr, "aff admin reset: no admin account exists yet; run `aff admin init` instead")
		return exitFail
	} else if err != nil {
		fmt.Fprintf(a.Stderr, "aff admin reset: checking for an existing admin: %v\n", err)
		return exitFail
	}

	password, err := a.readPassword("New admin password: ")
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff admin reset: reading password: %v\n", err)
		return exitFail
	}
	if err := auth.IsWeak(password); err != nil {
		fmt.Fprintf(a.Stderr, "aff admin reset: %v\n", err)
		return exitFail
	}

	hash, err := auth.Hash(password, auth.DefaultParams())
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff admin reset: hashing password: %v\n", err)
		return exitFail
	}
	kdfParams, err := json.Marshal(auth.DefaultParams())
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff admin reset: encoding KDF params: %v\n", err)
		return exitFail
	}
	if err := st.UpdatePassword(ctx, hash, string(kdfParams)); err != nil {
		fmt.Fprintf(a.Stderr, "aff admin reset: updating password: %v\n", err)
		return exitFail
	}

	secret, provisioningURI, err := auth.Enroll(totpAccountName, totpIssuer)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff admin reset: enrolling TOTP: %v\n", err)
		return exitFail
	}
	encSecret, err := auth.EncryptSecret(secret, key)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff admin reset: encrypting TOTP secret: %v\n", err)
		return exitFail
	}
	if err := st.SetTOTPSecret(ctx, encSecret); err != nil {
		fmt.Fprintf(a.Stderr, "aff admin reset: storing TOTP secret: %v\n", err)
		return exitFail
	}

	plainCodes, hashedCodes, err := auth.GenerateCodes(recoveryCodeCount)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff admin reset: generating recovery codes: %v\n", err)
		return exitFail
	}
	if err := st.StoreRecoveryCodes(ctx, hashedCodes); err != nil {
		fmt.Fprintf(a.Stderr, "aff admin reset: storing recovery codes: %v\n", err)
		return exitFail
	}

	if err := st.RevokeAllSessions(ctx); err != nil {
		fmt.Fprintf(a.Stderr, "aff admin reset: revoking existing sessions: %v\n", err)
		return exitFail
	}
	// The local session file, if any, refers to one of the sessions just
	// revoked server-side; leaving it in place would have the CLI itself
	// present a now-dead token on its next call.
	_ = clearSession(a.SessionFile)

	a.printEnrollment("Admin credentials reset. All existing sessions were revoked.", adminEnrollment{
		ProvisioningURI: provisioningURI,
		RecoveryCodes:   plainCodes,
	})
	return exitOK
}
