// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package main

import (
	"database/sql"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"
)

func Snippet(value string) string {
	if len(value) > 160 {
		return value[:160] + "..."
	}
	return value
}

func (h *DBHandler) SearchTitle(searchQuery string, limit int) ([]SearchResult, error) {
	start := time.Now()
	sqlQuery := `
		SELECT 
			rowid, 
			title, 
			snippet(article_search, 0, '<mark>', '</mark>', '...', 16) as snippet,
			bm25(article_search) AS power
		FROM article_search
		WHERE article_search MATCH ?
		ORDER BY power ASC
		LIMIT ?
	`

	rows, err := h.db.Query(sqlQuery, searchQuery, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var result SearchResult
		var snippet sql.NullString
		if err := rows.Scan(
			&result.ArticleID,
			&result.Title,
			&snippet,
			&result.Power,
		); err != nil {
			return nil, err
		}

		var content sql.NullString
		contentQuery := `SELECT content FROM sections WHERE article_id = ? ORDER BY id LIMIT 1`
		err = h.db.QueryRow(contentQuery, result.ArticleID).Scan(&content)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		if content.Valid {
			result.Text = Snippet(content.String)
		}

		if snippet.Valid {
			result.Snippet = snippet.String
		} else {
			result.Snippet = Snippet(result.Text)
		}

		result.Type = "T"
		results = append(results, result)
	}

	log.Printf("Search title: %s (%v)", searchQuery, time.Since(start))

	return results, nil
}

func (h *DBHandler) SearchContent(searchQuery string, limit int) ([]SearchResult, error) {
	start := time.Now()
	sqlQuery := `
		SELECT
			s.article_id,
			a.title,
			s.id,
			s.content,
			snippet(section_search, 1, '<mark>', '</mark>', '...', 64) as snippet,
			bm25(section_search) as power
		FROM section_search
		JOIN sections s ON section_search.rowid = s.id
		JOIN articles a ON s.article_id = a.id
		WHERE section_search MATCH ?
		ORDER BY power
		LIMIT ?;
	`
	rows, err := h.db.Query(sqlQuery, searchQuery, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var result SearchResult
		var content sql.NullString
		var sectionId int
		var snippet sql.NullString
		if err := rows.Scan(
			&result.ArticleID,
			&result.Title,
			&sectionId,
			&content,
			&snippet,
			&result.Power,
		); err != nil {
			return nil, err
		}

		if content.Valid {
			result.Text = content.String
		} else {
			var content_flate []byte
			if err := h.db.QueryRow("SELECT content_flate FROM sections WHERE id = ?", sectionId).Scan(&content_flate); err == nil && content_flate != nil {
				if content, err := TextInflate(content_flate); err == nil {
					result.Text = content
				}
			}
		}

		if snippet.Valid {
			result.Snippet = snippet.String
		} else {
			result.Snippet = Snippet(result.Text)
		}

		result.Type = "C"
		results = append(results, result)
	}

	log.Printf("Search content: %s (%v)", searchQuery, time.Since(start))

	return results, nil
}

func (h *DBHandler) SearchWordDistance(inputWord string, limit int) ([]SearchResult, error) {
	start := time.Now()
	var allMatches []SearchResult
	seen := make(map[string]bool)

	batchSize := 100 * 1000
	offset := 0

	for {
		rows, err := h.db.Query("SELECT term FROM vocabulary LIMIT ? OFFSET ?", batchSize, offset)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		processed := 0
		for rows.Next() {
			var word string
			if err := rows.Scan(&word); err != nil {
				return nil, err
			}

			if seen[word] {
				continue
			}
			seen[word] = true

			distance := LevenshteinDistance(inputWord, word)

			allMatches = append(allMatches, SearchResult{Text: word, Power: float64(distance)})

			if len(allMatches) > limit {
				sort.Slice(allMatches, func(i, j int) bool {
					return allMatches[i].Power < allMatches[j].Power
				})
				allMatches = allMatches[:limit]
			}

			processed++
		}

		if processed < batchSize {
			break
		}
		offset += batchSize
	}

	sort.Slice(allMatches, func(i, j int) bool {
		return allMatches[i].Power < allMatches[j].Power
	})

	log.Printf("Search word distance: %s (%v)", inputWord, time.Since(start))

	return allMatches, nil
}

func (h *DBHandler) SearchVectors(query string, limit int) ([]SearchResult, error) {
	hasAnn := db.AiHasANN()
	hasVectors := db.AiHasVectors()

	if !hasAnn && !hasVectors {
		log.Println("Warning, embeddings search requested but not available")
		return nil, nil
	}

	start := time.Now()
	topResults := make([]VectorDistance, 0, limit)
	sqlQuery := "SELECT id, embedding FROM vectors"

	queryEmbedding, err := aiEmbeddings(options.aiModelPrefixSearch + query)
	if err != nil {
		return nil, err
	}

	if hasAnn {
		annLimit := limit
		if hasVectors {
			annLimit = limit * limit
		}
		topAnnResults, err := h.SearchAnn(queryEmbedding, options.aiAnnMode, options.aiAnnSize, annLimit)
		if err != nil {
			return nil, err
		}
		var vectors_ids []int64
		var vectors_ids_string []string
		for _, v := range topAnnResults {
			var vectors_id int64
			if err := h.db.QueryRow("SELECT vectors_id FROM vectors_ann_index WHERE chunk_id = ? AND chunk_position = ? LIMIT 1", v.ChunkRowID, v.ChunkPosition).Scan(&vectors_id); err != nil {
				return nil, err
			}
			vectors_ids = append(vectors_ids, vectors_id)
			vectors_ids_string = append(vectors_ids_string, strconv.FormatInt(vectors_id, 10))
		}
		if hasVectors {
			sqlQuery += " WHERE id IN (" + strings.Join(vectors_ids_string, ",") + ")"
		} else {
			for _, id := range vectors_ids {
				topResults = append(topResults, VectorDistance{ID: id, Distance: float32(0)})
			}
		}

	}

	if hasVectors {
		rows, err := h.db.Query(sqlQuery)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var embeddingBlob []byte
			var ID int64
			if err := rows.Scan(&ID, &embeddingBlob); err != nil {
				return nil, err
			}
			embedding := BytesToFloat32(embeddingBlob)
			distance, err := EuclideanDistance(queryEmbedding, embedding)
			if err != nil {
				return nil, err
			}

			if len(topResults) <= limit {
				topResults = append(topResults, VectorDistance{ID: ID, Distance: float32(distance)})
			} else {
				maxIndex := -1
				maxDistance := float32(-1)
				for i := range topResults {
					if topResults[i].Distance > maxDistance {
						maxDistance = topResults[i].Distance
						maxIndex = i
					}
				}
				if maxIndex >= 0 && float32(distance) < maxDistance {
					topResults[maxIndex] = VectorDistance{ID: ID, Distance: float32(distance)}
				}
			}
		}
	}

	var results []SearchResult
	for _, vd := range topResults {
		sqlQuery := `
			SELECT
				a.id,
				a.title,
				s.content
			FROM articles a
			JOIN sections s ON a.id = s.article_id
			WHERE s.id = ?
		`

		var result SearchResult
		var sectionContent string
		err := h.db.QueryRow(sqlQuery, vd.ID).Scan(
			&result.ArticleID,
			&result.Title,
			&sectionContent,
		)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}

		result.Text = sectionContent
		result.Snippet = Snippet(sectionContent)
		result.Type = "V"
		result.Power = float64(vd.Distance)
		results = append(results, result)
	}

	log.Printf("Search vector: %s (%v)", query, time.Since(start))

	return results, nil
}

func (h *DBHandler) SearchAnn(vectors []float32, mode string, size int, limit int) ([]VectorDistance, error) {
	start := time.Now()
	chunkSize := 0
	if mode == "mrl" {
		chunkSize = size * 4
	} else if mode == "binary" {
		chunkSize = (len(vectors) + 7) / 8
	} else {
		return nil, fmt.Errorf("invalid ANN mode")
	}

	rows, err := h.db.Query("SELECT id, chunk FROM vectors_ann_chunks")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	topAnnResults := make([]VectorDistance, 0, limit)

	for rows.Next() {
		var chunkRowID int64
		var chunkBlob []byte
		if err := rows.Scan(&chunkRowID, &chunkBlob); err != nil {
			return nil, err
		}

		for position := 0; position < len(chunkBlob); position += chunkSize {
			var result VectorDistance
			embeddingBlob := chunkBlob[position:(position + chunkSize)]

			var distance float32
			if mode == "mrl" {
				mrlQuery := vectors
				if len(mrlQuery) > size {
					mrlQuery = mrlQuery[:size]
				}

				storedMRL := BytesToFloat32(embeddingBlob)
				distance, err = EuclideanDistance(mrlQuery, storedMRL)
			}
			if options.aiAnnMode == "binary" {
				quantizedQuery := QuantizeBinary(vectors)
				distance, err = HammingDistance(quantizedQuery, embeddingBlob)
			}

			result.ChunkRowID = chunkRowID
			result.ChunkPosition = position / chunkSize
			result.Distance = distance

			if len(topAnnResults) <= limit {
				topAnnResults = append(topAnnResults, result)
			} else {
				maxIndex := -1
				maxDistance := float32(-1)
				for i := range topAnnResults {
					if topAnnResults[i].Distance > maxDistance {
						maxDistance = topAnnResults[i].Distance
						maxIndex = i
					}
				}
				if maxIndex >= 0 && distance < maxDistance {
					topAnnResults[maxIndex] = result
				}
			}
		}
	}
	log.Printf("Search ANN time: %v", time.Since(start))
	return topAnnResults, nil
}
