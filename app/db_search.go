// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func Snippet(value string) string {
	if utf8.RuneCountInString(value) > 160 {
		runes := []rune(value)
		return string(runes[:160]) + "..."
	}
	return value
}

func normalizeBM25(score float64) float64 {
	rawScore := -score
	if rawScore < 0 {
		rawScore = 0
	}
	const c = 0.1
	return (1.0 - math.Exp(-c*rawScore)) * 100.0
}

func sanitizeFTSQuery(query string) string {
	words := strings.Fields(query)
	if len(words) == 0 {
		return ""
	}
	var sanitized []string
	for _, w := range words {
		clean := strings.ReplaceAll(w, `"`, `""`)
		sanitized = append(sanitized, `"`+clean+`"`)
	}
	return strings.Join(sanitized, " ")
}

func (h *DBHandler) SearchTitle(searchQuery string, limit int) ([]SearchResult, error) {
	conn := h.pool.Get(context.Background())
	if conn == nil {
		return nil, fmt.Errorf("failed to get connection")
	}
	defer h.pool.Put(conn)

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

	sanitized := sanitizeFTSQuery(searchQuery)
	var results []SearchResult
	err := sqlitex.ExecuteTransient(conn, sqlQuery, &sqlitex.ExecOptions{
		Args: []any{sanitized, limit},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			var result SearchResult
			result.ArticleID = int(stmt.ColumnInt64(0))
			result.Title = stmt.ColumnText(1)
			result.Snippet = stmt.ColumnText(2)
			result.Power = normalizeBM25(stmt.ColumnFloat(3))

			var textContent string
			contentQuery := `SELECT content FROM sections WHERE article_id = ? ORDER BY id LIMIT 1`
			sqlitex.ExecuteTransient(conn, contentQuery, &sqlitex.ExecOptions{
				Args: []any{result.ArticleID},
				ResultFunc: func(subStmt *sqlite.Stmt) error {
					textContent = subStmt.ColumnText(0)
					return nil
				},
			})
			if textContent != "" {
				result.Text = Snippet(textContent)
			}
			if result.Snippet == "" {
				result.Snippet = Snippet(result.Text)
			}
			result.Type = "T"
			results = append(results, result)
			return nil
		},
	})
	if err != nil {
		return nil, err
	}

	log.Printf("Search title: %s (%v)", searchQuery, time.Since(start))
	return results, nil
}

func (h *DBHandler) SearchContent(searchQuery string, limit int) ([]SearchResult, error) {
	conn := h.pool.Get(context.Background())
	if conn == nil {
		return nil, fmt.Errorf("failed to get connection")
	}
	defer h.pool.Put(conn)

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
		LIMIT ?
	`

	sanitized := sanitizeFTSQuery(searchQuery)
	var results []SearchResult
	err := sqlitex.ExecuteTransient(conn, sqlQuery, &sqlitex.ExecOptions{
		Args: []any{sanitized, limit},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			var result SearchResult
			result.ArticleID = int(stmt.ColumnInt64(0))
			result.Title = stmt.ColumnText(1)
			sectionID := int(stmt.ColumnInt64(2))
			result.Text = stmt.ColumnText(3)
			result.Snippet = stmt.ColumnText(4)
			result.Power = normalizeBM25(stmt.ColumnFloat(5))

			if result.Text == "" {
				var contentFlate []byte
				sqlitex.ExecuteTransient(conn, "SELECT content_flate FROM sections WHERE id = ?", &sqlitex.ExecOptions{
					Args: []any{sectionID},
					ResultFunc: func(subStmt *sqlite.Stmt) error {
						contentFlate = make([]byte, subStmt.ColumnLen(0))
						subStmt.ColumnBytes(0, contentFlate)
						return nil
					},
				})
				if len(contentFlate) > 0 {
					if content, err := TextInflate(contentFlate); err == nil {
						result.Text = content
					}
				}
			}

			if result.Snippet == "" {
				result.Snippet = Snippet(result.Text)
			}
			result.Type = "C"
			results = append(results, result)
			return nil
		},
	})
	if err != nil {
		return nil, err
	}

	log.Printf("Search content: %s (%v)", searchQuery, time.Since(start))
	return results, nil
}

func (h *DBHandler) SearchWordDistance(inputWord string, limit int) ([]SearchResult, error) {
	conn := h.pool.Get(context.Background())
	if conn == nil {
		return nil, fmt.Errorf("failed to get connection")
	}
	defer h.pool.Put(conn)

	start := time.Now()
	var allMatches []SearchResult
	seen := make(map[string]bool)

	batchSize := 100000
	offset := 0

	for {
		processed := 0
		err := sqlitex.ExecuteTransient(conn, "SELECT term FROM vocabulary LIMIT ? OFFSET ?", &sqlitex.ExecOptions{
			Args: []any{batchSize, offset},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				word := stmt.ColumnText(0)
				if seen[word] {
					return nil
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
				return nil
			},
		})
		if err != nil {
			return nil, err
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

	queryEmbedding, err := aiEmbeddings(options.aiModelPrefixSearch + query)
	if err != nil {
		return nil, err
	}

	var topAnnResults []VectorDistance
	if hasAnn {
		annLimit := limit
		if hasVectors {
			annLimit = limit * limit
		}
		var err error
		topAnnResults, err = h.SearchAnn(queryEmbedding, options.aiAnnMode, options.aiAnnSize, annLimit)
		if err != nil {
			return nil, err
		}
	}

	conn := h.pool.Get(context.Background())
	if conn == nil {
		return nil, fmt.Errorf("failed to get connection")
	}
	defer h.pool.Put(conn)

	start := time.Now()
	topResults := make([]VectorDistance, 0, limit)
	sqlQuery := "SELECT id, embedding FROM vectors"

	if hasAnn {
		var vectorsIDsString []string
		for _, v := range topAnnResults {
			var vectorsID int64
			err := sqlitex.ExecuteTransient(conn, "SELECT vectors_id FROM vectors_ann_index WHERE chunk_id = ? AND chunk_position = ? LIMIT 1", &sqlitex.ExecOptions{
				Args: []any{v.ChunkRowID, v.ChunkPosition},
				ResultFunc: func(stmt *sqlite.Stmt) error {
					vectorsID = stmt.ColumnInt64(0)
					return nil
				},
			})
			if err != nil {
				return nil, err
			}

			if !hasVectors {
				topResults = append(topResults, VectorDistance{ID: vectorsID, Distance: v.Distance})
			} else {
				vectorsIDsString = append(vectorsIDsString, strconv.FormatInt(vectorsID, 10))
			}
		}
		if hasVectors {
			sqlQuery += " WHERE id IN (" + strings.Join(vectorsIDsString, ",") + ")"
		}
	}

	if hasVectors {
		err := sqlitex.ExecuteTransient(conn, sqlQuery, &sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				ID := stmt.ColumnInt64(0)
				embeddingBlob := make([]byte, stmt.ColumnLen(1))
				stmt.ColumnBytes(1, embeddingBlob)
				embedding := BytesToFloat32(embeddingBlob)
				similarity := dot(queryEmbedding, embedding)

				if len(topResults) < limit {
					topResults = append(topResults, VectorDistance{ID: ID, Distance: similarity})
				} else {
					minIndex := -1
					minSimilarity := float32(2.0)
					for i := range topResults {
						if topResults[i].Distance < minSimilarity {
							minSimilarity = topResults[i].Distance
							minIndex = i
						}
					}
					if minIndex >= 0 && similarity > minSimilarity {
						topResults[minIndex] = VectorDistance{ID: ID, Distance: similarity}
					}
				}
				return nil
			},
		})
		if err != nil {
			return nil, err
		}
	}

	sort.Slice(topResults, func(i, j int) bool {
		return topResults[i].Distance > topResults[j].Distance
	})

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
		err := sqlitex.ExecuteTransient(conn, sqlQuery, &sqlitex.ExecOptions{
			Args: []any{vd.ID},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				result.ArticleID = int(stmt.ColumnInt64(0))
				result.Title = stmt.ColumnText(1)
				sectionContent = stmt.ColumnText(2)
				return nil
			},
		})
		if err != nil {
			return nil, err
		}

		result.Text = sectionContent
		result.Snippet = Snippet(sectionContent)
		result.Type = "V"

		affinity := (float64(vd.Distance) + 1.0) / 2.0 * 100.0
		if affinity < 0 {
			affinity = 0
		} else if affinity > 100 {
			affinity = 100
		}
		result.Power = affinity

		results = append(results, result)
	}

	log.Printf("Search vector: %s (%v)", query, time.Since(start))
	return results, nil
}

func (h *DBHandler) SearchAnn(vectors []float32, mode string, size int, limit int) ([]VectorDistance, error) {
	conn := h.pool.Get(context.Background())
	if conn == nil {
		return nil, fmt.Errorf("failed to get connection")
	}
	defer h.pool.Put(conn)

	start := time.Now()
	chunkSize := 0
	if mode == "mrl" {
		chunkSize = size * 4
	} else if mode == "binary" {
		chunkSize = (len(vectors) + 7) / 8
	} else {
		return nil, fmt.Errorf("invalid ANN mode")
	}

	var quantizedQuery []byte
	if mode == "binary" {
		quantizedQuery = QuantizeBinary(vectors)
	}

	var mrlQuery []float32
	if mode == "mrl" {
		mrlQuery = make([]float32, len(vectors))
		copy(mrlQuery, vectors)
		if len(mrlQuery) > size {
			mrlQuery = mrlQuery[:size]
		}
		l2Norm(mrlQuery)
	}

	topAnnResults := make([]VectorDistance, 0, limit)

	err := sqlitex.ExecuteTransient(conn, "SELECT id, chunk FROM vectors_ann_chunks", &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			chunkRowID := stmt.ColumnInt64(0)
			chunkBlob := make([]byte, stmt.ColumnLen(1))
			stmt.ColumnBytes(1, chunkBlob)

			for position := 0; position < len(chunkBlob); position += chunkSize {
				var result VectorDistance
				embeddingBlob := chunkBlob[position : position+chunkSize]

				var distance float32
				if mode == "mrl" {
					storedMRL := BytesToFloat32(embeddingBlob)
					distance = dot(mrlQuery, storedMRL)
				}
				if mode == "binary" {
					hammingDist, err := HammingDistance(quantizedQuery, embeddingBlob)
					if err != nil {
						return err
					}
					totalBits := float32(len(quantizedQuery) * 8)
					matchingBits := totalBits - hammingDist
					distance = (matchingBits/totalBits)*2.0 - 1.0
				}

				result.ChunkRowID = chunkRowID
				result.ChunkPosition = position / chunkSize
				result.Distance = distance

				if len(topAnnResults) < limit {
					topAnnResults = append(topAnnResults, result)
				} else {
					minIndex := -1
					minDistance := float32(2.0)
					if mode == "binary" {
						minDistance = float32(len(quantizedQuery) * 8)
					}
					for i := range topAnnResults {
						if topAnnResults[i].Distance < minDistance {
							minDistance = topAnnResults[i].Distance
							minIndex = i
						}
					}
					if minIndex >= 0 && distance > minDistance {
						topAnnResults[minIndex] = result
					}
				}
			}
			return nil
		},
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(topAnnResults, func(i, j int) bool {
		return topAnnResults[i].Distance > topAnnResults[j].Distance
	})

	log.Printf("Search ANN time: %v", time.Since(start))
	return topAnnResults, nil
}
