// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package main

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"time"
)

func (h *DBHandler) ProcessTitles() error {
	_, err := h.db.Exec("INSERT INTO article_search(rowid, title) SELECT id, title FROM articles")
	if err != nil {
		return fmt.Errorf("error populating article_search table: %v", err)
	}

	return nil
}

func (h *DBHandler) ProcessContents() error {
	_, err := h.db.Exec("INSERT INTO section_search(rowid, title, content) SELECT id, title, content FROM sections")
	if err != nil {
		return fmt.Errorf("error populating section_search table: %v", err)
	}

	return nil
}

func (h *DBHandler) ProcessVocabulary() error {
	_, err := h.db.Exec("INSERT OR IGNORE INTO vocabulary SELECT term FROM article_search_vocabulary")
	if err != nil {
		return fmt.Errorf("error populating vocabulary table: %v", err)
	}

	_, err = h.db.Exec("INSERT OR IGNORE INTO vocabulary SELECT term FROM section_search_vocabulary")
	if err != nil {
		return fmt.Errorf("error populating vocabulary table: %v", err)
	}

	return nil
}

func (h *DBHandler) ProcessEmbeddings() (err error) {
	batchSize := 250

	if options.aiModel != "" {
		if err = db.SetupPut("model", options.aiModel); err != nil {
			return
		}
	}

	if err = db.SetupPut("modelPrefixSave", options.aiModelPrefixSave); err != nil {
		return
	}

	if err = db.SetupPut("modelPrefixSearch", options.aiModelPrefixSearch); err != nil {
		return
	}

	log.Printf("Loading pending vector IDs for Embeddings processing...")
	rows, err := h.db.Query(`
		SELECT s.id 
		FROM sections s 
		WHERE s.id NOT IN (SELECT id FROM vectors)
		ORDER BY s.id`)
	if err != nil {
		return fmt.Errorf("error loading pending section IDs: %w", err)
	}
	defer rows.Close()

	var pendingSectionIDs []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("error scanning section ID: %w", err)
		}
		pendingSectionIDs = append(pendingSectionIDs, id)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating section IDs: %w", err)
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

		tx, err := h.db.Begin()
		if err != nil {
			return fmt.Errorf("error starting transaction: %w", err)
		}

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

		rows, err := tx.Query(query, args...)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("error querying sections batch: %w", err)
		}

		var sectionIDs []int
		var sectionTitles []string
		var articleTitles []string
		var sectionContents []string

		for rows.Next() {
			var sectionID int
			var sectionTitle, articleTitle, sectionContent string
			if err := rows.Scan(&sectionID, &sectionTitle, &articleTitle, &sectionContent); err != nil {
				rows.Close()
				tx.Rollback()
				return fmt.Errorf("error scanning section row: %w", err)
			}
			sectionIDs = append(sectionIDs, sectionID)
			sectionTitles = append(sectionTitles, sectionTitle)
			articleTitles = append(articleTitles, articleTitle)
			sectionContents = append(sectionContents, sectionContent)
		}
		rows.Close()

		if err := rows.Err(); err != nil {
			tx.Rollback()
			return fmt.Errorf("error iterating section rows: %w", err)
		}

		for i, sectionID := range sectionIDs {
			fullSectionText := articleTitles[i] + " - " + sectionTitles[i] + "\n\n" + sectionContents[i]

			embedding, err := aiEmbeddings(options.aiModelPrefixSave + fullSectionText)
			if err != nil {
				log.Printf("Embedding generation error for section %d: %v", sectionID, err)
				problematicIDs = append(problematicIDs, sectionID)
				continue
			}

			if _, err := tx.Exec("INSERT OR REPLACE INTO vectors (id, embedding) VALUES (?, ?)", sectionID, Float32ToBytes(embedding)); err != nil {
				log.Printf("Error inserting vector for section %d: %v", sectionID, err)
				problematicIDs = append(problematicIDs, sectionID)
				continue
			}
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("error committing transaction: %w", err)
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
		if err := db.SetupPut("annMode", method); err != nil {
			return err
		}
		if err := db.SetupPut("annSize", fmt.Sprintf("%d", size)); err != nil {
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

	rows, err := h.db.Query(`
        SELECT v.id 
        FROM vectors v 
        WHERE v.id NOT IN (SELECT vectors_id FROM vectors_ann_index)
        ORDER BY v.id`)
	if err != nil {
		return fmt.Errorf("error loading pending vector IDs: %w", err)
	}
	defer rows.Close()

	var pendingVectorIDs []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("error scanning vector ID: %w", err)
		}
		pendingVectorIDs = append(pendingVectorIDs, id)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating vector IDs: %w", err)
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

		tx, err := h.db.Begin()
		if err != nil {
			return fmt.Errorf("error starting transaction: %w", err)
		}

		placeholders := make([]string, len(batchIDs))
		args := make([]any, len(batchIDs))
		for i, id := range batchIDs {
			placeholders[i] = "?"
			args[i] = id
		}

		query := fmt.Sprintf("SELECT id, embedding FROM vectors WHERE id IN (%s)", strings.Join(placeholders, ","))
		rows, err := tx.Query(query, args...)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("error querying vectors batch: %w", err)
		}

		var vectorIDs []int
		var embeddings [][]byte

		for rows.Next() {
			var vectorID int
			var embedding []byte
			if err := rows.Scan(&vectorID, &embedding); err != nil {
				rows.Close()
				tx.Rollback()
				return fmt.Errorf("error scanning vector row: %w", err)
			}
			vectorIDs = append(vectorIDs, vectorID)
			embeddings = append(embeddings, embedding)
		}
		rows.Close()

		if err := rows.Err(); err != nil {
			tx.Rollback()
			return fmt.Errorf("error iterating vector rows: %w", err)
		}

		var annChunkID int
		if err := tx.QueryRow("SELECT COALESCE(MAX(id), 0) + 1 FROM vectors_ann_chunks").Scan(&annChunkID); err != nil {
			tx.Rollback()
			return fmt.Errorf("error getting next chunk ID: %w", err)
		}

		var annChunkData []byte
		for i, vectorID := range vectorIDs {
			embedding := BytesToFloat32(embeddings[i])

			var annData []byte
			if method == "mrl" {
				annData = ExtractMRL(embedding, size)
			} else if method == "binary" {
				annData = QuantizeBinary(embedding)
			}

			if _, err := tx.Exec(
				"INSERT INTO vectors_ann_index (vectors_id, chunk_id, chunk_position) VALUES (?, ?, ?)",
				vectorID, annChunkID, i); err != nil {
				tx.Rollback()
				return fmt.Errorf("error inserting ANN index for vector %d: %w", vectorID, err)
			}

			annChunkData = append(annChunkData, annData...)
		}

		if _, err := tx.Exec(
			"INSERT INTO vectors_ann_chunks (id, chunk) VALUES (?, ?)",
			annChunkID, annChunkData); err != nil {
			tx.Rollback()
			return fmt.Errorf("error inserting ANN chunk: %w", err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("error committing transaction: %w", err)
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
