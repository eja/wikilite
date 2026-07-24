// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

const VectorsPerCentroid = 2500

type DBHandler struct {
	pool *sqlitex.Pool
}

func NewDBHandler(dbPath string) (*DBHandler, error) {
	isReadOnly := !options.aiSync && options.wikiImport == "" && options.aiModelImport == "" && !options.dbCompress
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		isReadOnly = false
	}

	var conn *sqlite.Conn
	var err error

	if isReadOnly {
		conn, err = sqlite.OpenConn(dbPath, sqlite.OpenReadOnly)
		if err != nil {
			return nil, fmt.Errorf("error opening database in read-only mode: %v", err)
		}
	} else {
		conn, err = sqlite.OpenConn(dbPath, sqlite.OpenReadWrite|sqlite.OpenCreate)
		if err != nil {
			return nil, fmt.Errorf("error opening database in read-write mode: %v", err)
		}
	}

	if !isReadOnly {
		pragmas := []string{
			"PRAGMA synchronous = OFF",
			"PRAGMA journal_mode = OFF",
			"PRAGMA foreign_keys = OFF",
			"PRAGMA cache_size = -10000",
			"PRAGMA mmap_size = 268435456",
			"PRAGMA temp_store = MEMORY",
		}
		for _, pragma := range pragmas {
			if err := sqlitex.ExecuteTransient(conn, pragma, nil); err != nil {
				conn.Close()
				return nil, fmt.Errorf("error executing initialization PRAGMA %s: %v", pragma, err)
			}
		}

		queries := []string{
			`CREATE TABLE IF NOT EXISTS setup (
				key TEXT PRIMARY KEY,
				value BLOB
			)`,
			`CREATE TABLE IF NOT EXISTS articles (
				id INTEGER PRIMARY KEY,
				title TEXT NOT NULL,
				entity TEXT NOT NULL
			)`,
			`CREATE VIRTUAL TABLE IF NOT EXISTS article_search USING fts5(
				title,
				content='articles',
				content_rowid='id'
			)`,
			`CREATE VIRTUAL TABLE IF NOT EXISTS article_search_vocabulary USING fts5vocab(article_search, row)`,
			`CREATE TABLE IF NOT EXISTS sections (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				article_id INTEGER,
				title TEXT,
				content TEXT,
				content_flate BLOB,
				pow INTEGER DEFAULT 0,
				FOREIGN KEY(article_id) REFERENCES articles(id)
			)`,
			`CREATE VIRTUAL TABLE IF NOT EXISTS section_search USING fts5(
				title, content,
				content='sections',
				content_rowid='id'
			)`,
			`CREATE VIRTUAL TABLE IF NOT EXISTS section_search_vocabulary USING fts5vocab(section_search, row)`,
			`CREATE TABLE IF NOT EXISTS vocabulary (term TEXT)`,
			`CREATE TABLE IF NOT EXISTS vectors (
				id INTEGER PRIMARY KEY,
				embedding BLOB
			)`,
			`CREATE TABLE IF NOT EXISTS vectors_ann_chunks (
				id INTEGER PRIMARY KEY,
				chunk BLOB
			)`,
			`CREATE TABLE IF NOT EXISTS vectors_ann_index (
				id INTEGER PRIMARY KEY,
				vectors_id INTEGER NOT NULL,
				chunk_id INTEGER NOT NULL,
				chunk_position INTEGER NOT NULL
			)`,
			`CREATE TABLE IF NOT EXISTS vectors_ann_centroids (
				id INTEGER PRIMARY KEY,
				centroid BLOB
			)`,
			`CREATE TABLE IF NOT EXISTS vectors_ann_centroid_chunks (
				centroid_id INTEGER,
				chunk_id INTEGER
			)`,
			`CREATE INDEX IF NOT EXISTS idx_vectors_ann_centroid_chunks ON vectors_ann_centroid_chunks (centroid_id)`,
			`CREATE INDEX IF NOT EXISTS idx_vectors_ann_index_chunk_id_position ON vectors_ann_index (chunk_id, chunk_position)`,
			`CREATE INDEX IF NOT EXISTS idx_sections_article_id ON sections(article_id)`,
		}
		for _, query := range queries {
			if err := sqlitex.ExecuteTransient(conn, query, nil); err != nil {
				conn.Close()
				return nil, fmt.Errorf("error executing schema query: %v", err)
			}
		}
	} else {
		mmapVal := "268435456"
		cacheVal := "-10000"
		if !options.aiCache {
			mmapVal = "0"
			cacheVal = "-1000"
		}
		pragmas := []string{
			"PRAGMA query_only = ON",
			"PRAGMA cache_size = " + cacheVal,
			"PRAGMA mmap_size = " + mmapVal,
			"PRAGMA temp_store = MEMORY",
		}
		for _, pragma := range pragmas {
			if err := sqlitex.ExecuteTransient(conn, pragma, nil); err != nil {
				conn.Close()
				return nil, fmt.Errorf("error executing read-only PRAGMA %s: %v", pragma, err)
			}
		}
	}

	conn.Close()

	poolSize := 10
	if !options.aiCache {
		poolSize = 1
	}

	opts := sqlitex.PoolOptions{
		PoolSize: poolSize,
		PrepareConn: func(conn *sqlite.Conn) error {
			var pragmas []string
			if isReadOnly {
				mmapVal := "268435456"
				cacheVal := "-10000"
				if !options.aiCache {
					mmapVal = "0"
					cacheVal = "-1000"
				}
				pragmas = []string{
					"PRAGMA query_only = ON",
					"PRAGMA cache_size = " + cacheVal,
					"PRAGMA mmap_size = " + mmapVal,
					"PRAGMA temp_store = MEMORY",
				}
			} else {
				mmapVal := "268435456"
				cacheVal := "-10000"
				if !options.aiCache {
					mmapVal = "0"
					cacheVal = "-1000"
				}
				pragmas = []string{
					"PRAGMA synchronous = OFF",
					"PRAGMA foreign_keys = OFF",
					"PRAGMA cache_size = " + cacheVal,
					"PRAGMA mmap_size = " + mmapVal,
					"PRAGMA temp_store = MEMORY",
				}
			}
			for _, pragma := range pragmas {
				if err := sqlitex.ExecuteTransient(conn, pragma, nil); err != nil {
					return err
				}
			}
			return nil
		},
	}
	if isReadOnly {
		opts.Flags = sqlite.OpenReadOnly | sqlite.OpenURI
	} else {
		opts.Flags = sqlite.OpenReadWrite | sqlite.OpenCreate | sqlite.OpenURI
		opts.PoolSize = 1
	}

	pool, err := sqlitex.NewPool(dbPath, opts)
	if err != nil {
		return nil, fmt.Errorf("error opening database pool: %v", err)
	}

	handler := &DBHandler{pool: pool}

	if !isReadOnly {
		if err := handler.PragmaInitMode(); err != nil {
			pool.Close()
			return nil, err
		}
	} else {
		if err := handler.PragmaReadMode(); err != nil {
			pool.Close()
			return nil, err
		}
	}

	if language, err := handler.SetupGet("language"); err == nil && language != "" {
		options.language = language
	}

	if model, err := handler.SetupGet("model"); err == nil && model != "" {
		options.aiModel = model
	}

	if annSize, err := handler.SetupGet("annSize"); err == nil && annSize != "" {
		options.aiAnnSize = extractNumberFromString(annSize)
	}

	if modelPrefixSearch, err := handler.SetupGet("modelPrefixSearch"); err == nil && modelPrefixSearch != "" {
		options.aiModelPrefixSearch = modelPrefixSearch
	}

	if modelPrefixSave, err := handler.SetupGet("modelPrefixSave"); err == nil && modelPrefixSave != "" {
		options.aiModelPrefixSave = modelPrefixSave
	}

	return handler, nil
}

func (h *DBHandler) Close() error {
	return h.pool.Close()
}

func (h *DBHandler) Pragma(pragmas []string) error {
	conn := h.pool.Get(context.Background())
	if conn == nil {
		return fmt.Errorf("failed to get connection")
	}
	defer h.pool.Put(conn)

	for _, pragma := range pragmas {
		if err := sqlitex.ExecuteTransient(conn, pragma, nil); err != nil {
			return fmt.Errorf("error executing PRAGMA %s: %v", pragma, err)
		}
	}
	return nil
}

func (h *DBHandler) PragmaInitMode() error {
	pragmas := []string{
		"PRAGMA synchronous = OFF",
		"PRAGMA foreign_keys = OFF",
		"PRAGMA cache_size = -10000",
		"PRAGMA mmap_size = 268435456",
		"PRAGMA temp_store = MEMORY",
	}
	return h.Pragma(pragmas)
}

func (h *DBHandler) PragmaReadMode() error {
	pragmas := []string{
		"PRAGMA locking_mode = NORMAL",
		"PRAGMA query_only = ON",
	}
	return h.Pragma(pragmas)
}

func (h *DBHandler) PragmaImportMode() error {
	pragmas := []string{
		"PRAGMA locking_mode = EXCLUSIVE",
		"PRAGMA query_only = OFF",
	}
	return h.Pragma(pragmas)
}

func (h *DBHandler) Optimize() error {
	conn := h.pool.Get(context.Background())
	if conn == nil {
		return fmt.Errorf("failed to get connection")
	}
	defer h.pool.Put(conn)

	log.Println("Deleting duplicate sections")
	err := func() error {
		var err error
		deferFn := sqlitex.Transaction(conn)
		defer deferFn(&err)

		err = sqlitex.Execute(conn, `
			DELETE FROM sections
			WHERE id NOT IN (
				SELECT MAX(id)
				FROM sections
				GROUP BY article_id, title
			)`, nil)
		return err
	}()
	if err != nil {
		return fmt.Errorf("error deleting duplicate sections: %v", err)
	}

	log.Println("Running VACUUM")
	err = sqlitex.Execute(conn, "VACUUM", nil)
	if err != nil {
		return fmt.Errorf("error executing VACUUM: %v", err)
	}

	return nil
}

func (h *DBHandler) SetupPut(key, value string) error {
	conn := h.pool.Get(context.Background())
	if conn == nil {
		return fmt.Errorf("failed to get connection")
	}
	defer h.pool.Put(conn)

	return sqlitex.Execute(conn, "INSERT OR REPLACE INTO setup (key, value) VALUES (?, ?)", &sqlitex.ExecOptions{
		Args: []any{key, value},
	})
}

func (h *DBHandler) SetupGet(key string) (string, error) {
	conn := h.pool.Get(context.Background())
	if conn == nil {
		return "", fmt.Errorf("failed to get connection")
	}
	defer h.pool.Put(conn)

	var value string
	var found bool
	err := sqlitex.Execute(conn, "SELECT value FROM setup WHERE key = ? LIMIT 1", &sqlitex.ExecOptions{
		Args: []any{key},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			value = stmt.ColumnText(0)
			found = true
			return nil
		},
	})
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("key not found")
	}
	return value, nil
}

func (h *DBHandler) ArticlePut(article OutputArticle) error {
	conn := h.pool.Get(context.Background())
	if conn == nil {
		return fmt.Errorf("failed to get connection")
	}
	defer h.pool.Put(conn)

	var err error
	deferFn := sqlitex.Transaction(conn)
	defer deferFn(&err)

	err = sqlitex.Execute(conn, "INSERT OR REPLACE INTO articles (id, title, entity) VALUES (?, ?, ?)", &sqlitex.ExecOptions{
		Args: []any{article.ID, article.Title, article.Entity},
	})
	if err != nil {
		return fmt.Errorf("error inserting article: %v", err)
	}

	for _, item := range article.Items {
		title, _ := item["title"].(string)
		pow, _ := item["pow"].(int)
		content, _ := item["content"].(string)

		err = sqlitex.Execute(conn, "INSERT INTO sections (article_id, title, content, pow) VALUES (?, ?, ?, ?)", &sqlitex.ExecOptions{
			Args: []any{article.ID, title, content, pow},
		})
		if err != nil {
			return fmt.Errorf("error inserting section: %v", err)
		}
	}

	return nil
}

func (h *DBHandler) ArticleGet(articleID int) (ArticleResult, error) {
	conn := h.pool.Get(context.Background())
	if conn == nil {
		return ArticleResult{}, fmt.Errorf("failed to get connection")
	}
	defer h.pool.Put(conn)

	start := time.Now()

	article := ArticleResult{
		Sections: []ArticleResultSection{},
	}

	sqlQuery := `
		SELECT
			a.id,
			a.title,
			a.entity,
			s.id,
			s.title,
			s.content
		FROM
			articles a
		JOIN
			sections s ON a.id = s.article_id
		WHERE
			a.id = ?
		ORDER BY
			s.id ASC
	`

	var isFirstRow = true
	err := sqlitex.Execute(conn, sqlQuery, &sqlitex.ExecOptions{
		Args: []any{articleID},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			var (
				artID          int
				artTitle       string
				artEntity      string
				section        ArticleResultSection
				sectionContent string
			)

			artID = int(stmt.ColumnInt64(0))
			artTitle = stmt.ColumnText(1)
			artEntity = stmt.ColumnText(2)
			section.ID = int(stmt.ColumnInt64(3))
			section.Title = stmt.ColumnText(4)
			sectionContent = stmt.ColumnText(5)

			if sectionContent != "" {
				section.Content = sectionContent
			} else {
				var contentFlate []byte
				err := sqlitex.Execute(conn, "SELECT content_flate FROM sections WHERE id = ?", &sqlitex.ExecOptions{
					Args: []any{section.ID},
					ResultFunc: func(subStmt *sqlite.Stmt) error {
						contentFlate = make([]byte, subStmt.ColumnLen(0))
						subStmt.ColumnBytes(0, contentFlate)
						return nil
					},
				})
				if err == nil && len(contentFlate) > 0 {
					if content, err := TextInflate(contentFlate); err == nil {
						section.Content = content
					}
				}
			}

			if isFirstRow {
				article.ID = artID
				article.Title = artTitle
				article.Entity = artEntity
				isFirstRow = false
			}

			article.Sections = append(article.Sections, section)
			return nil
		},
	})

	if err != nil {
		return article, fmt.Errorf("article query error: %v", err)
	}

	if article.ID == 0 {
		return article, fmt.Errorf("article not found")
	}

	log.Printf("Article retrieve: %d (%v)", articleID, time.Since(start))

	return article, nil
}

func (h *DBHandler) Compress() error {
	conn := h.pool.Get(context.Background())
	if conn == nil {
		return fmt.Errorf("failed to get connection")
	}
	defer h.pool.Put(conn)

	var totalSections int
	err := sqlitex.Execute(conn, "SELECT COUNT(*) FROM sections WHERE content IS NOT NULL AND content != ''", &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			totalSections = int(stmt.ColumnInt64(0))
			return nil
		},
	})
	if err != nil {
		return fmt.Errorf("error counting sections: %v", err)
	}

	log.Printf("Compressing %d sections", totalSections)

	type section struct {
		id      int
		content string
	}
	var sections []section

	err = sqlitex.Execute(conn, "SELECT id, content FROM sections WHERE content IS NOT NULL AND content != ''", &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			sections = append(sections, section{
				id:      int(stmt.ColumnInt64(0)),
				content: stmt.ColumnText(1),
			})
			return nil
		},
	})
	if err != nil {
		return fmt.Errorf("error querying sections: %v", err)
	}

	processed := 0
	compressed := 0
	var lastLogTime time.Time
	batchStartTime := time.Now()

	err = func() error {
		var err error
		deferFn := sqlitex.Transaction(conn)
		defer deferFn(&err)

		for _, s := range sections {
			if s.content != "" {
				compressedContent, err := TextDeflate(s.content)
				if err != nil {
					return fmt.Errorf("error compressing section content: %v", err)
				}

				if len(compressedContent) < len(s.content) {
					err = sqlitex.Execute(conn, "UPDATE sections SET content_flate = ?, content = NULL WHERE id = ?", &sqlitex.ExecOptions{
						Args: []any{compressedContent, s.id},
					})
					if err != nil {
						return fmt.Errorf("error updating section with compressed content: %v", err)
					}
					compressed++
				}
			}

			processed++

			now := time.Now()
			if processed%10000 == 0 || now.Sub(lastLogTime) >= 5*time.Second {
				progress := (float64(processed) / float64(totalSections) * 100)
				elapsed := time.Since(batchStartTime)
				estimatedTotal := time.Duration(float64(processed) / float64(totalSections))
				remaining := estimatedTotal - elapsed
				log.Printf("Compression progress: %.2f%% - ETA: %v", progress, remaining.Round(time.Second))
				lastLogTime = now
			}
		}
		return nil
	}()
	if err != nil {
		return err
	}

	log.Printf("Compression ready, running VACUUM...")
	err = sqlitex.Execute(conn, "VACUUM", nil)
	if err != nil {
		return fmt.Errorf("error executing VACUUM: %v", err)
	}

	return nil
}
