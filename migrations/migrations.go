// Package migrations embeds the SQL schema migrations. Files are named
// NNNN_description.sql and applied in lexical order.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
