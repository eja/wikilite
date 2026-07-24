// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package main

import (
	"context"
	"fmt"
	"log"
	"runtime"
	"strings"
	"sync"
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

	err := sqlitex.Execute(conn, "INSERT INTO article_search(rowid, title) SELECT id, title FROM articles", nil)
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

	err := sqlitex.Execute(conn, "INSERT INTO section_search(rowid, title, content) SELECT id, title, content FROM sections", nil)
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

	err := sqlitex.Execute(conn, "INSERT OR IGNORE INTO vocabulary SELECT term FROM article_search_vocabulary", nil)
	if err != nil {
		return fmt.Errorf("error populating vocabulary table: %v", err)
	}

	err = sqlitex.Execute(conn, "INSERT OR IGNORE INTO vocabulary SELECT term FROM section_search_vocabulary", nil)
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

	var pendingSectionIDs []int
	var problematicIDs []int

	err = func() error {
		conn := h.pool.Get(context.Background())
		if conn == nil {
			return fmt.Errorf("failed to get connection")
		}
		defer h.pool.Put(conn)

		err = sqlitex.Execute(conn, `
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

				err = sqlitex.Execute(conn, query, &sqlitex.ExecOptions{
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

				subBatchSize := 64
				for i := 0; i < len(sections); i += subBatchSize {
					endIdx := min(i+subBatchSize, len(sections))
					chunk := sections[i:endIdx]

					var texts []string
					for _, s := range chunk {
						fullSectionText := s.aTitle + " - " + s.sTitle + "\n\n" + s.content
						texts = append(texts, options.aiModelPrefixSave+fullSectionText)
					}

					var embeddings [][]float32
					var err error

					if options.aiApi {
						embeddings, err = aiApiEmbeddingsBatch(texts)
					} else {
						embeddings = make([][]float32, len(chunk))
						for idx, text := range texts {
							embeddings[idx], err = localAiEmbeddings(text)
							if err != nil {
								break
							}
						}
					}

					if err != nil {
						log.Printf("Embedding generation error for batch starting at index %d: %v", i, err)
						for _, s := range chunk {
							problematicIDs = append(problematicIDs, s.id)
						}
						continue
					}

					for idx, s := range chunk {
						err = sqlitex.Execute(conn, "INSERT OR REPLACE INTO vectors (id, embedding) VALUES (?, ?)", &sqlitex.ExecOptions{
							Args: []any{s.id, Float32ToBytes(embeddings[idx])},
						})
						if err != nil {
							log.Printf("Error inserting vector for section %d: %v", s.id, err)
							problematicIDs = append(problematicIDs, s.id)
						}
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

		return nil
	}()

	if err != nil {
		return err
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
	size := 0
	size = options.aiAnnSize
	if err := h.SetupPut("annSize", fmt.Sprintf("%d", size)); err != nil {
		return err
	}

	if size == 0 {
		return fmt.Errorf("invalid quantization size")
	}

	log.Printf("Loading pending vector IDs for ANN processing using MRL and size %d...", size)

	conn := h.pool.Get(context.Background())
	if conn == nil {
		return fmt.Errorf("failed to get connection")
	}
	defer h.pool.Put(conn)

	err := sqlitex.Execute(conn, "DELETE FROM vectors_ann_index", nil)
	if err != nil {
		return err
	}
	err = sqlitex.Execute(conn, "DELETE FROM vectors_ann_chunks", nil)
	if err != nil {
		return err
	}
	err = sqlitex.Execute(conn, "DELETE FROM vectors_ann_centroids", nil)
	if err != nil {
		return err
	}
	err = sqlitex.Execute(conn, "DELETE FROM vectors_ann_centroid_chunks", nil)
	if err != nil {
		return err
	}

	type clusterItem struct {
		id  int
		emb []float32
	}
	var mrlItems []clusterItem

	err = sqlitex.Execute(conn, "SELECT id, embedding FROM vectors ORDER BY id", &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			embBytes := make([]byte, stmt.ColumnLen(1))
			stmt.ColumnBytes(1, embBytes)
			fullEmb := BytesToFloat32(embBytes)

			currentSize := size
			if len(fullEmb) < currentSize {
				currentSize = len(fullEmb)
			}

			emb := make([]float32, currentSize)
			copy(emb, fullEmb[:currentSize])
			l2Norm(emb)

			mrlItems = append(mrlItems, clusterItem{
				id:  int(stmt.ColumnInt64(0)),
				emb: emb,
			})
			return nil
		},
	})
	if err != nil {
		return err
	}

	totalCount := len(mrlItems)
	if totalCount == 0 {
		log.Printf("No vectors to process")
		return nil
	}

	k := totalCount / VectorsPerCentroid
	if k < 1 {
		k = 1
	}

	centroids := make([][]float32, k)
	for i := range k {
		centroids[i] = make([]float32, size)
		copy(centroids[i], mrlItems[(i*totalCount)/k].emb)
	}

	log.Printf("Clustering vectors into %d centroids...", k)

	assignments := make([]int, totalCount)
	for iter := 0; iter < 10; iter++ {
		clusterStart := time.Now()

		numWorkers := options.aiThreads
		if numWorkers <= 0 {
			numWorkers = runtime.NumCPU()
		}

		var wg sync.WaitGroup
		chunkSize := (totalCount + numWorkers - 1) / numWorkers

		for w := 0; w < numWorkers; w++ {
			startIdx := w * chunkSize
			if startIdx >= totalCount {
				break
			}
			endIdx := startIdx + chunkSize
			if endIdx > totalCount {
				endIdx = totalCount
			}

			wg.Add(1)
			go func(start, end int) {
				defer wg.Done()
				for i := start; i < end; i++ {
					item := mrlItems[i]
					bestIdx := 0
					bestSim := float32(-1.0)
					for cIdx, c := range centroids {
						sim := dot(item.emb, c)
						if sim > bestSim {
							bestSim = sim
							bestIdx = cIdx
						}
					}
					assignments[i] = bestIdx
				}
			}(startIdx, endIdx)
		}
		wg.Wait()

		nextCentroids := make([][]float32, k)
		counts := make([]int, k)
		for i := range k {
			nextCentroids[i] = make([]float32, size)
		}

		for i, item := range mrlItems {
			cIdx := assignments[i]
			for d := range size {
				nextCentroids[cIdx][d] += item.emb[d]
			}
			counts[cIdx]++
		}

		for i := range k {
			if counts[i] > 0 {
				l2Norm(nextCentroids[i])
				centroids[i] = nextCentroids[i]
			} else {
				copy(centroids[i], mrlItems[(i*7)%totalCount].emb)
			}
		}

		log.Printf("Centroid clustering iteration %d/10 completed in %v", iter+1, time.Since(clusterStart))
	}

	groups := make([][]clusterItem, k)
	for i, item := range mrlItems {
		cIdx := assignments[i]
		groups[cIdx] = append(groups[cIdx], item)
	}

	startTime := time.Now()
	processedCount := 0

	err = func() error {
		var err error
		deferFn := sqlitex.Transaction(conn)
		defer deferFn(&err)

		for cIdx, group := range groups {
			centroidBytes := Float32ToBytes(centroids[cIdx])
			err = sqlitex.Execute(conn, "INSERT OR REPLACE INTO vectors_ann_centroids (id, centroid) VALUES (?, ?)", &sqlitex.ExecOptions{
				Args: []any{cIdx + 1, centroidBytes},
			})
			if err != nil {
				return err
			}

			for gStart := 0; gStart < len(group); gStart += VectorsPerCentroid {
				gEnd := gStart + VectorsPerCentroid
				if gEnd > len(group) {
					gEnd = len(group)
				}
				subGroup := group[gStart:gEnd]

				var annChunkID int
				err = sqlitex.Execute(conn, "SELECT COALESCE(MAX(id), 0) + 1 FROM vectors_ann_chunks", &sqlitex.ExecOptions{
					ResultFunc: func(stmt *sqlite.Stmt) error {
						annChunkID = int(stmt.ColumnInt64(0))
						return nil
					},
				})
				if err != nil {
					return err
				}

				var annChunkData []byte
				for i, item := range subGroup {
					annData := Float32ToBytes(item.emb)

					err = sqlitex.Execute(conn, "INSERT INTO vectors_ann_index (vectors_id, chunk_id, chunk_position) VALUES (?, ?, ?)", &sqlitex.ExecOptions{
						Args: []any{item.id, annChunkID, i},
					})
					if err != nil {
						return err
					}

					annChunkData = append(annChunkData, annData...)
				}

				err = sqlitex.Execute(conn, "INSERT INTO vectors_ann_chunks (id, chunk) VALUES (?, ?)", &sqlitex.ExecOptions{
					Args: []any{annChunkID, annChunkData},
				})
				if err != nil {
					return err
				}

				err = sqlitex.Execute(conn, "INSERT INTO vectors_ann_centroid_chunks (centroid_id, chunk_id) VALUES (?, ?)", &sqlitex.ExecOptions{
					Args: []any{cIdx + 1, annChunkID},
				})
				if err != nil {
					return err
				}

				processedCount += len(subGroup)
				progress := float64(processedCount) / float64(totalCount) * 100
				elapsed := time.Since(startTime)
				var remainingTime time.Duration
				if progress > 0 {
					estimatedTotalTime := time.Duration(float64(elapsed) / (progress / 100.0))
					remainingTime = estimatedTotalTime - elapsed
				}

				log.Printf("ANN progress: %.2f%%, Processed: %d/%d, Remaining: %s", progress, processedCount, totalCount, remainingTime.Truncate(time.Second))
			}
		}
		return nil
	}()
	if err != nil {
		return err
	}

	log.Printf("ANN processing completed in %s", time.Since(startTime))
	return nil
}
