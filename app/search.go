// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

func Search(query string, limit int) ([]SearchResult, error) {
	start := time.Now()
	var results []SearchResult

	lexical, err := SearchLexical(query, limit)
	if err != nil {
		return nil, err
	}
	results = append(results, lexical...)

	if len(lexical) <= limit {
		semantic, err := SearchSemantic(query, limit)
		if err != nil {
			return nil, err
		}
		results = append(results, semantic...)
	}

	res := searchOptimize(results, limit)

	if options.log {
		log.Printf("Search: %q took %v", query, time.Since(start))
	}

	return res, nil
}

func SearchSemantic(query string, limit int) ([]SearchResult, error) {
	var results []SearchResult

	if ai {
		vectors, err := db.SearchVectors(query, limit)
		if err != nil {
			return nil, err
		}
		for _, vector := range vectors {
			results = append(results, vector)
		}
	}

	return results, nil
}

func SearchLexical(query string, limit int) ([]SearchResult, error) {
	var results []SearchResult
	var err error

	results, err = SearchTitle(query, limit)
	if err != nil {
		return nil, err
	}

	contents, err := db.SearchContent(query, limit)
	if err != nil {
		return nil, err
	}
	for _, content := range contents {
		results = append(results, content)
	}

	return searchOptimize(results, limit), nil
}

func SearchTitle(query string, limit int) ([]SearchResult, error) {
	var results []SearchResult

	titles, err := db.SearchTitle(query, limit)
	if err != nil {
		return nil, err
	}
	for _, title := range titles {
		results = append(results, title)
	}

	return results, nil
}

func SearchWordDistance(word string, limit int) ([]SearchResult, error) {
	return db.SearchWordDistance(word, limit)
}

func SearchCli() error {
	reader := bufio.NewReader(os.Stdin)
	articles := make(map[int]int)

	for {
		fmt.Print("> ")
		query, _ := reader.ReadString('\n')
		query = strings.TrimSpace(query)
		if query == "" {
			return nil
		}

		queryIdx, err := strconv.Atoi(query)
		if err == nil {
			if articleID, exists := articles[queryIdx]; exists {
				article, err := db.ArticleGet(articleID)
				if err != nil {
					log.Fatal("CLI error: ", err)
				}

				fmt.Printf("\033[1;30m\n%s\n\033[0m", article.Title)
				for _, section := range article.Sections {
					if section.Title != "" {
						fmt.Printf("\033[1;30m\n%s\n\033[0m\n", section.Title)
					} else {
						fmt.Println()
					}
					fmt.Println(section.Content)
				}

				query = ""
			}
		}

		if query != "" {
			results, err := Search(query, options.limit)
			if err != nil {
				log.Fatal("CLI error: ", err)
			}

			articles = make(map[int]int)
			for i, result := range results {
				articles[i+1] = result.ArticleID
				if options.log {
					fmt.Printf("% 3d [%s] [%.0f] %s\n", i+1, result.Type, result.Power, result.Title)
				} else {
					fmt.Printf("% 3d [%s] %s\n", i+1, result.Type, result.Title)
				}
			}
		}
	}
}

func searchOptimize(results []SearchResult, limit int) []SearchResult {
	seen := make(map[int]bool)
	accumulatedResults := []SearchResult{}

	for _, result := range results {
		if !seen[result.ArticleID] {
			seen[result.ArticleID] = true
			accumulatedResults = append(accumulatedResults, result)
		} else {
			for i := range accumulatedResults {
				if accumulatedResults[i].ArticleID == result.ArticleID {
					p1 := accumulatedResults[i].Power
					p2 := result.Power
					accumulatedResults[i].Power = p1 + p2 - (p1 * p2 / 100.0)
					break
				}
			}
		}
	}

	sort.Slice(accumulatedResults, func(i, j int) bool {
		return accumulatedResults[i].Power > accumulatedResults[j].Power
	})

	if len(accumulatedResults) > limit {
		accumulatedResults = accumulatedResults[:limit]
	}

	return accumulatedResults
}
