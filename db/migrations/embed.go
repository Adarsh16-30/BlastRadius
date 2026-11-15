// Package migrations embeds the numbered SQL files applied by `api migrate`.
package migrations

import "embed"

// FS holds every *.sql migration, applied in filename order.
//
//go:embed *.sql
var FS embed.FS
