// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func (h *DBHandler) AiModelImport(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("file does not exist: %s", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read file: %v", err)
	}

	conn := h.pool.Get(context.Background())
	if conn == nil {
		return fmt.Errorf("failed to get connection")
	}
	defer h.pool.Put(conn)

	err = sqlitex.ExecuteTransient(conn, `INSERT OR REPLACE INTO setup (key, value) VALUES ('gguf', ?)`, &sqlitex.ExecOptions{
		Args: []any{data},
	})
	if err != nil {
		return fmt.Errorf("failed to import model into database: %v", err)
	}

	return nil
}

func (h *DBHandler) AiModelBlob() (io.ReadSeeker, func() error, error) {
	conn := h.pool.Get(context.Background())
	if conn == nil {
		return nil, nil, fmt.Errorf("failed to get connection")
	}

	var rowID int64
	err := sqlitex.ExecuteTransient(conn, "SELECT rowid FROM setup WHERE key = 'gguf' LIMIT 1", &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			rowID = stmt.ColumnInt64(0)
			return nil
		},
	})
	if err != nil || rowID == 0 {
		h.pool.Put(conn)
		return nil, nil, fmt.Errorf("gguf model not found in setup table")
	}

	blob, err := conn.OpenBlob("", "setup", "value", rowID, false)
	if err != nil {
		h.pool.Put(conn)
		return nil, nil, fmt.Errorf("failed to open gguf blob: %w", err)
	}

	closeFn := func() error {
		blob.Close()
		h.pool.Put(conn)
		return nil
	}

	return blob, closeFn, nil
}

func (h *DBHandler) AiHasANN() bool {
	conn := h.pool.Get(context.Background())
	if conn == nil {
		return false
	}
	defer h.pool.Put(conn)

	var found bool
	err := sqlitex.ExecuteTransient(conn, "SELECT id FROM vectors_ann_index LIMIT 1", &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			found = true
			return nil
		},
	})
	return err == nil && found
}

func (h *DBHandler) AiHasVectors() bool {
	conn := h.pool.Get(context.Background())
	if conn == nil {
		return false
	}
	defer h.pool.Put(conn)

	var found bool
	err := sqlitex.ExecuteTransient(conn, "SELECT id FROM vectors LIMIT 1", &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			found = true
			return nil
		},
	})
	return err == nil && found
}
