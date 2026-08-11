// expirydecision.go is the pure decision half of D0-08: "session expiry
// mid-session drops to ANON without losing unsaved work silently." No
// build tag deliberately, split out of session.go's applyEvent (js-only:
// it also writes PendingExpiryAtom, a live GWC atom, which needs a real
// fiber/DOM environment) so the actual branch — hold vs. apply
// immediately — is host-testable with `go test ./web/...` rather than
// only provable by compiling and running a WASM binary.
package shell

// ShouldHoldExpiry reports whether a received EvSessionExpired should be
// held back (shown as a confirmation modal, applied only once the admin
// acknowledges) rather than applied immediately. dirtyCheck is the
// currently-registered page predicate (RegisterDirtyCheck) — nil until
// some page registers one, meaning "never dirty," matching dirtyCheck's
// own zero-value doc comment in session.go. Calling dirtyCheck() at most
// once, only when non-nil, mirrors applyEvent's own short-circuit so a
// test can assert this without a real page's closure.
func ShouldHoldExpiry(dirtyCheck func() bool) bool {
	return dirtyCheck != nil && dirtyCheck()
}
