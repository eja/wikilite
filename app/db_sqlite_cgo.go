//go:build !android && cgo

package main

import (
	"database/sql"

	_ "github.com/mattn/go-sqlite3"
)

func openDB(dbPath string) (*sql.DB, error) {
	return sql.Open("sqlite3", dbPath)
}
