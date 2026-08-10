package feedspec

import "encoding/json"

// Export and Import back the "aff recipe export|import" versioning and
// disaster-recovery path (§7). PLAN.md's §7 and §12.5 call the on-disk
// format TOML. This package uses JSON instead: `go list -m all` shows no TOML
// library in go.mod (checked before writing this file), and the hard rule
// for this change is no new dependencies and no hand-rolled TOML parser.
// JSON gives the same property §7 actually needs — a stable, round-trippable,
// human-readable text encoding for versioning and restore — so this is a
// deliberate, noted deviation from the letter of §7, to be reconciled
// (either by adding a TOML dependency later, or by updating §7 to say JSON)
// rather than silently diverging from the plan.

// Export serializes s to its on-disk recipe format.
func Export(s Spec) ([]byte, error) {
	return json.MarshalIndent(s, "", "  ")
}

// Import parses b back into a Spec. It does not call Validate — callers
// (the RPC's ImportTOML equivalent) are expected to validate the result
// explicitly, the same way a freshly edited Spec is validated before save.
func Import(b []byte) (Spec, error) {
	var s Spec
	if err := json.Unmarshal(b, &s); err != nil {
		return Spec{}, err
	}
	return s, nil
}
