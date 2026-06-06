//go:build android || !cgo

package main

import (
	"database/sql"

	_ "modernc.org/sqlite"
)

func openDB(dbPath string) (*sql.DB, error) {
	return sql.Open("sqlite", dbPath)
}
