// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package main

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func (h *DBHandler) ProcessTitles() error {
	conn := h.pool.Get(context.Background())
	if conn == nil {
		return fmt.Errorf("failed to get connection")
	}
	defer h.pool.Put(conn)

	err := sqlitex.ExecuteTransient(conn, "INSERT INTO article_search(rowid, title) SELECT id, title FROM articles", nil)
	if err != nil {
		return fmt.Errorf("error populating article_search table: %v", err)
	}

	return nil
}

func (h *DBHandler) ProcessContents() error {
	conn := h.pool.Get(context.Background())
	if conn == nil {
		return fmt.Errorf("failed to get connection")
	}
	defer h.pool.Put(conn)

	err := sqlitex.ExecuteTransient(conn, "INSERT INTO section_search(rowid, title, content) SELECT id, title, content FROM sections", nil)
	if err != nil {
		return fmt.Errorf("error populating section_search table: %v", err)
	}

	return nil
}

func (h *DBHandler) ProcessVocabulary() error {
	conn := h.pool.Get(context.Background())
	if conn == nil {
		return fmt.Errorf("failed to get connection")
	}
	defer h.pool.Put(conn)

	err := sqlitex.ExecuteTransient(conn, "INSERT OR IGNORE INTO vocabulary SELECT term FROM article_search_vocabulary", nil)
	if err != nil {
		return fmt.Errorf("error populating vocabulary table: %v", err)
	}

	err = sqlitex.ExecuteTransient(conn, "INSERT OR IGNORE INTO vocabulary SELECT term FROM section_search_vocabulary", nil)
	if err != nil {
		return fmt.Errorf("error populating vocabulary table: %v", err)
	}

	return nil
}

func (h *DBHandler) ProcessEmbeddings() (err error) {
	batchSize := 250

	if options.aiModel != "" {
		if err = h.SetupPut("model", options.aiModel); err != nil {
			return
		}
	}

	if err = h.SetupPut("modelPrefixSave", options.aiModelPrefixSave); err != nil {
		return
	}

	if err = h.SetupPut("modelPrefixSearch", options.aiModelPrefixSearch); err != nil {
		return
	}

	log.Printf("Loading pending vector IDs for Embeddings processing...")

	conn := h.pool.Get(context.Background())
	if conn == nil {
		return fmt.Errorf("failed to get connection")
	}
	defer h.pool.Put(conn)

	var pendingSectionIDs []int
	err = sqlitex.ExecuteTransient(conn, `
		SELECT s.id 
		FROM sections s 
		WHERE s.id NOT IN (SELECT id FROM vectors)
		ORDER BY s.id`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			pendingSectionIDs = append(pendingSectionIDs, int(stmt.ColumnInt64(0)))
			return nil
		},
	})
	if err != nil {
		return fmt.Errorf("error loading pending section IDs: %w", err)
	}

	totalCount := len(pendingSectionIDs)
	log.Printf("Pending section embeddings: %d", totalCount)

	if totalCount == 0 {
		log.Printf("No sections to process for embeddings")
	}

	startTime := time.Now()
	processed := 0
	var problematicIDs []int

	for processed < totalCount {
		end := min(processed+batchSize, totalCount)
		batchIDs := pendingSectionIDs[processed:end]
		if len(batchIDs) == 0 {
			break
		}

		err = func() error {
			var err error
			deferFn := sqlitex.Transaction(conn)
			defer deferFn(&err)

			placeholders := make([]string, len(batchIDs))
			args := make([]any, len(batchIDs))
			for i, id := range batchIDs {
				placeholders[i] = "?"
				args[i] = id
			}

			query := fmt.Sprintf(`
				SELECT s.id, s.title, a.title, s.content 
				FROM sections s 
				JOIN articles a ON s.article_id = a.id 
				WHERE s.id IN (%s)`, strings.Join(placeholders, ","))

			type sectionData struct {
				id      int
				sTitle  string
				aTitle  string
				content string
			}
			var sections []sectionData

			err = sqlitex.ExecuteTransient(conn, query, &sqlitex.ExecOptions{
				Args: args,
				ResultFunc: func(stmt *sqlite.Stmt) error {
					sections = append(sections, sectionData{
						id:      int(stmt.ColumnInt64(0)),
						sTitle:  stmt.ColumnText(1),
						aTitle:  stmt.ColumnText(2),
						content: stmt.ColumnText(3),
					})
					return nil
				},
			})
			if err != nil {
				return err
			}

			for _, s := range sections {
				fullSectionText := s.aTitle + " - " + s.sTitle + "\n\n" + s.content
				embedding, err := aiEmbeddings(options.aiModelPrefixSave + fullSectionText)
				if err != nil {
					log.Printf("Embedding generation error for section %d: %v", s.id, err)
					problematicIDs = append(problematicIDs, s.id)
					continue
				}

				err = sqlitex.ExecuteTransient(conn, "INSERT OR REPLACE INTO vectors (id, embedding) VALUES (?, ?)", &sqlitex.ExecOptions{
					Args: []any{s.id, Float32ToBytes(embedding)},
				})
				if err != nil {
					log.Printf("Error inserting vector for section %d: %v", s.id, err)
					problematicIDs = append(problematicIDs, s.id)
					continue
				}
			}

			return nil
		}()
		if err != nil {
			return err
		}

		processed += len(batchIDs)
		progress := float64(processed) / float64(totalCount) * 100
		elapsed := time.Since(startTime)

		if progress > 0 {
			estimatedTotalTime := time.Duration(float64(elapsed) / (progress / 100.0))
			remainingTime := estimatedTotalTime - elapsed
			log.Printf("Embedding progress: %.2f%%, Processed: %d/%d, Remaining: %s",
				progress, processed, totalCount, remainingTime.Truncate(time.Second))
		}
	}

	if len(problematicIDs) > 0 {
		log.Printf("Embedding process completed with %d problematic sections that need manual review", len(problematicIDs))
	}

	if options.aiAnn {
		return h.ProcessANN()
	}

	return nil
}

func (h *DBHandler) ProcessANN() error {
	batchSize := 250
	method := ""
	size := 0
	if options.aiAnnMode == "mrl" || options.aiAnnMode == "binary" {
		method = options.aiAnnMode
		size = options.aiAnnSize
		if err := h.SetupPut("annMode", method); err != nil {
			return err
		}
		if err := h.SetupPut("annSize", fmt.Sprintf("%d", size)); err != nil {
			return err
		}
	}

	if method == "" {
		return fmt.Errorf("invalid quantization method")
	}
	if method == "mrl" && size == 0 {
		return fmt.Errorf("invalid quantization size")
	}

	log.Printf("Loading pending vector IDs for ANN processing using mode %s and size %d...", method, size)

	conn := h.pool.Get(context.Background())
	if conn == nil {
		return fmt.Errorf("failed to get connection")
	}
	defer h.pool.Put(conn)

	var pendingVectorIDs []int
	err := sqlitex.ExecuteTransient(conn, `
        SELECT v.id 
        FROM vectors v 
        WHERE v.id NOT IN (SELECT vectors_id FROM vectors_ann_index)
        ORDER BY v.id`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			pendingVectorIDs = append(pendingVectorIDs, int(stmt.ColumnInt64(0)))
			return nil
		},
	})
	if err != nil {
		return fmt.Errorf("error loading pending vector IDs: %w", err)
	}

	totalCount := len(pendingVectorIDs)
	log.Printf("Pending ANN processing: %d", totalCount)

	if totalCount == 0 {
		log.Printf("No vectors to process")
		return nil
	}

	sort.Ints(pendingVectorIDs)

	startTime := time.Now()
	processed := 0

	for processed < totalCount {
		end := min(processed+batchSize, totalCount)
		batchIDs := pendingVectorIDs[processed:end]
		if len(batchIDs) == 0 {
			break
		}

		err = func() error {
			var err error
			deferFn := sqlitex.Transaction(conn)
			defer deferFn(&err)

			placeholders := make([]string, len(batchIDs))
			args := make([]any, len(batchIDs))
			for i, id := range batchIDs {
				placeholders[i] = "?"
				args[i] = id
			}

			query := fmt.Sprintf("SELECT id, embedding FROM vectors WHERE id IN (%s)", strings.Join(placeholders, ","))

			type vectorData struct {
				id        int
				embedding []byte
			}
			var vectors []vectorData

			err = sqlitex.ExecuteTransient(conn, query, &sqlitex.ExecOptions{
				Args: args,
				ResultFunc: func(stmt *sqlite.Stmt) error {
					embBytes := make([]byte, stmt.ColumnLen(1))
					stmt.ColumnBytes(1, embBytes)
					vectors = append(vectors, vectorData{
						id:        int(stmt.ColumnInt64(0)),
						embedding: embBytes,
					})
					return nil
				},
			})
			if err != nil {
				return err
			}

			var annChunkID int
			err = sqlitex.ExecuteTransient(conn, "SELECT COALESCE(MAX(id), 0) + 1 FROM vectors_ann_chunks", &sqlitex.ExecOptions{
				ResultFunc: func(stmt *sqlite.Stmt) error {
					annChunkID = int(stmt.ColumnInt64(0))
					return nil
				},
			})
			if err != nil {
				return err
			}

			var annChunkData []byte
			for i, v := range vectors {
				embedding := BytesToFloat32(v.embedding)
				var annData []byte
				if method == "mrl" {
					truncated := make([]float32, size)
					copy(truncated, embedding[:size])
					l2Norm(truncated)
					annData = Float32ToBytes(truncated)
				} else if method == "binary" {
					annData = QuantizeBinary(embedding)
				}

				err = sqlitex.ExecuteTransient(conn, "INSERT INTO vectors_ann_index (vectors_id, chunk_id, chunk_position) VALUES (?, ?, ?)", &sqlitex.ExecOptions{
					Args: []any{v.id, annChunkID, i},
				})
				if err != nil {
					return err
				}

				annChunkData = append(annChunkData, annData...)
			}

			err = sqlitex.ExecuteTransient(conn, "INSERT INTO vectors_ann_chunks (id, chunk) VALUES (?, ?)", &sqlitex.ExecOptions{
				Args: []any{annChunkID, annChunkData},
			})
			if err != nil {
				return err
			}

			return nil
		}()
		if err != nil {
			return err
		}

		processed += len(batchIDs)
		progress := float64(processed) / float64(totalCount) * 100
		elapsed := time.Since(startTime)

		if progress > 0 {
			estimatedTotal := time.Duration(float64(elapsed) / (progress / 100.0))
			remaining := estimatedTotal - elapsed
			log.Printf("ANN progress: %.2f%%, Processed: %d/%d, Remaining: %s",
				progress, processed, totalCount, remaining.Truncate(time.Second))
		}
	}

	log.Printf("ANN processing completed in %s", time.Since(startTime))
	return nil
}
