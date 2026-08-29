package migrations

import "embed"

// FS contains the versioned SQL migrations shipped with the binary.
//
// The first migration is deliberately a no-op. Ticket 01 proves that goose
// is wired into startup; domain tables belong to the later domain tickets.
//
//go:embed *.sql
var FS embed.FS
