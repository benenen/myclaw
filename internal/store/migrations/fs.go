package migrations

import "embed"

//go:embed *.sql
var migrationsFS embed.FS

func FS() embed.FS {
	return migrationsFS
}
