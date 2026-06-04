// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package main

import (
	"database/sql"
	"fmt"
	"os"
)

func (h *DBHandler) AiModelImport(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("file does not exist: %s", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read file: %v", err)
	}

	_, err = h.db.Exec(`INSERT OR REPLACE INTO setup (key, value) VALUES ("model", ?)`, data)

	if err != nil {
		return fmt.Errorf("failed to import model into database: %v", err)
	}

	return nil
}

func (h *DBHandler) AiModelLoad() []byte {
	var data []byte

	row := h.db.QueryRow(`SELECT value FROM setup WHERE key = "model" LIMIT 1`)

	err := row.Scan(&data)
	if err != nil {
		return nil
	}

	return data
}

func (h *DBHandler) AiHasANN() bool {
	var id int
	err := h.db.QueryRow("SELECT id FROM vectors_ann_index LIMIT 1").Scan(&id)
	return err != sql.ErrNoRows
}

func (h *DBHandler) AiHasVectors() bool {
	var id int
	err := h.db.QueryRow("SELECT id FROM vectors LIMIT 1").Scan(&id)
	return err != sql.ErrNoRows
}
