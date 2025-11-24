// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package main

type SearchResult struct {
	ArticleID int     `json:"article_id,omitempty"`
	Title     string  `json:"title,omitempty"`
	Text      string  `json:"text"`
	Type      string  `json:"type,omitempty"`
	Power     float64 `json:"power"`
	Snippet   string  `json:"snippet"`
}

type ArticleResultSection struct {
	ID      int    `json:"id"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

type ArticleResult struct {
	ID       int                    `json:"id"`
	Title    string                 `json:"title,omitempty"`
	Entity   string                 `json:"entity,omitempty"`
	Sections []ArticleResultSection `json:"sections,omitempty"`
}

type OutputArticle struct {
	Title  string                   `json:"title"`
	Entity string                   `json:"entity"`
	Items  []map[string]interface{} `json:"items"`
	ID     int                      `json:"id"`
}

type InputArticle struct {
	MainEntity struct {
		Identifier string `json:"identifier"`
	} `json:"main_entity"`
	Name        string `json:"name"`
	ArticleBody struct {
		HTML string `json:"html"`
	} `json:"article_body"`
	Identifier int `json:"identifier"`
}

type VectorDistance struct {
	ID            int64
	ChunkRowID    int64
	ChunkPosition int
	Distance      float32
}
