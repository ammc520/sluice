package migrations

import "embed"

//go:embed *.sql
var files embed.FS

// FS returns the embedded migration files.
func FS() embed.FS {
	return files
}
