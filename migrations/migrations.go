// Package migrations carries the schema, embedded in the binary.
//
// It exists as a package rather than a bare directory because go:embed cannot
// reach outside the package that declares it, and PLAN.md §3 puts migrations at
// the repository root rather than under internal/store.
//
// Embedding is the point: the binary ships its own schema, so a container can
// never start against a migrations directory that was not built with it — which
// is exactly the failure that produces a half-migrated database in production.
package migrations

import "embed"

// FS holds every NNNN_name.sql file in this directory.
//
//go:embed *.sql
var FS embed.FS
