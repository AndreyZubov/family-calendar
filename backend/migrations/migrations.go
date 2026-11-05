// Package migrations embeds the SQL schema migrations into the binary, so the
// server can apply them on startup without shipping files alongside it.
package migrations

import "embed"

// FS contains all goose SQL migrations, named NNNNN_description.sql.
//
//go:embed *.sql
var FS embed.FS
