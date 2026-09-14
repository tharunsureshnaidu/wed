// Package migrations embeds the SQL migration files so the binary is
// self-contained - no migrations directory needs to ship alongside it.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
