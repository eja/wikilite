// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"golang.org/x/net/html"
)

func WikiImport(path string) (err error) {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		err = wikiRemoteImport(path)
	} else {
		err = wikiLocalImport(path)
	}
	if err != nil {
		return
	}
	if err = db.SetupPut("version", Version); err != nil {
		return
	}
	if err = db.SetupPut("language", options.language); err != nil {
		return
	}
	if err = db.Optimize(); err != nil {
		return
	}
	if err = db.ProcessTitles(); err != nil {
		return
	}
	if err = db.ProcessContents(); err != nil {
		return
	}
	if err = db.ProcessVocabulary(); err != nil {
		return
	}

	return
}

func wikiImportFromReader(reader io.Reader, totalSize int64) error {
	bytesRead := int64(0)
	teeReader := io.TeeReader(reader, &byteCounter{&bytesRead})

	gzipReader, err := gzip.NewReader(teeReader)
	if err != nil {
		return fmt.Errorf("error creating gzip reader: %v", err)
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	return wikiProcessTarArchive(tarReader, totalSize, &bytesRead)
}

func wikiRemoteImport(url string) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("error downloading file: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP error: %s", resp.Status)
	}

	totalSize := resp.ContentLength
	return wikiImportFromReader(resp.Body, totalSize)
}

func wikiLocalImport(filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("error opening file: %v", err)
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("error getting file info: %v", err)
	}
	totalSize := fileInfo.Size()

	return wikiImportFromReader(file, totalSize)
}

func wikiProcessTarArchive(tarReader *tar.Reader, totalSize int64, bytesRead *int64) error {
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("error reading tar file: %v", err)
		}

		if header.Typeflag == tar.TypeReg {
			log.Printf("Processing file: %s\n", header.Name)

			if err := wikiProcessJSONLFile(tarReader); err != nil {
				log.Printf("Error processing file %s: %v\n", header.Name, err)
				continue // Continue with next file even if this one fails
			}

			if totalSize > 0 {
				percentage := float64(*bytesRead) / float64(totalSize) * 100
				log.Printf("Processed: %s %.2f%%\n", header.Name, percentage)
			}
		}
	}
	return nil
}

func wikiProcessJSONLFile(reader io.Reader) error {
	jsonDecoder := json.NewDecoder(reader)
	for {
		var art InputArticle
		if err := jsonDecoder.Decode(&art); err == io.EOF {
			break
		} else if err != nil {
			return fmt.Errorf("error decoding JSONL: %v", err)
		}

		if art.ArticleBody.HTML == "" {
			log.Println("Debug: article_body.html is empty or not present in this record")
			continue
		}

		output := wikiExtractContentFromHTML(art.ArticleBody.HTML, art.MainEntity.Identifier, art.Name, art.Identifier)

		if output != nil && db != nil {
			if err := db.ArticlePut(*output); err != nil {
				log.Printf("Error saving to database: %v\n", err)
				continue
			}
		}
	}
	return nil
}

func wikiHasExternalLink(node *html.Node) bool {
	if node.Type == html.ElementNode && node.Data == "a" {
		for _, attr := range node.Attr {
			if attr.Key == "class" && strings.Contains(attr.Val, "external") {
				return true
			}
		}
	}
	for c := node.FirstChild; c != nil; c = c.NextSibling {
		if wikiHasExternalLink(c) {
			return true
		}
	}
	return false
}

func wikiCollectTextFromNode(node *html.Node, depth int) string {
	var textContent string

	if node.Type == html.TextNode {
		textContent += node.Data
	} else if node.Type == html.ElementNode {
		switch node.Data {
		case "style", "script", "math", "table":
			return textContent
		case "sup":
			for _, attr := range node.Attr {
				if attr.Key == "class" && strings.Contains(attr.Val, "reference") {
					return textContent
				}
			}
		case "li":
			textContent = strings.Repeat("\t", depth) + "\u2022 "
			depth++

		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			textContent += wikiCollectTextFromNode(c, depth)
		}
	}
	return textContent
}

func wikiProcessTextElementWithText(textContent string, lastHeading *string, power *int, groupedItems *[]map[string]any) {
	trimmedText := strings.TrimSpace(textContent)
	if trimmedText == "" {
		return
	}
	subKey := *lastHeading
	found := false
	for i, item := range *groupedItems {
		if item["title"] == subKey {
			(*groupedItems)[i]["text"] = append((*groupedItems)[i]["text"].([]string), trimmedText)
			found = true
			break
		}
	}

	if !found {
		*groupedItems = append(*groupedItems, map[string]any{
			"title": *lastHeading,
			"pow":   *power,
			"text":  []string{trimmedText},
		})
	}
}

func wikiProcessTextElement(node *html.Node, lastHeading *string, power *int, groupedItems *[]map[string]any) {
	textContent := wikiCollectTextFromNode(node, 0)
	wikiProcessTextElementWithText(textContent, lastHeading, power, groupedItems)
}

func wikiExtractContentFromHTML(htmlContent string, articleID string, articleTitle string, identifier int) *OutputArticle {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		log.Printf("Error parsing HTML: %v\n", err)
		return nil
	}

	var lastHeading string
	var power int

	groupedItems := []map[string]any{}

	var extractText func(*html.Node)
	extractText = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "table", "style", "script", "math":
				return
			case "sup":
				for _, attr := range n.Attr {
					if attr.Key == "class" && strings.Contains(attr.Val, "reference") {
						return
					}
				}
			case "ul", "ol":
				var liTexts []string
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					if c.Data == "li" {
						if wikiHasExternalLink(c) {
							continue
						}
						if c.Parent != nil {
							for _, attr := range c.Parent.Attr {
								if attr.Key == "class" && strings.Contains(attr.Val, "references") {
									continue
								}
							}
						}
						textContent := wikiCollectTextFromNode(c, 0)
						if strings.TrimSpace(textContent) != "" {
							liTexts = append(liTexts, "\n"+textContent)
						}
						c.FirstChild = nil
					}
				}
				if len(liTexts) > 0 {
					wikiProcessTextElementWithText(strings.Join(liTexts, ""), &lastHeading, &power, &groupedItems)
				}
			case "p":
				wikiProcessTextElement(n, &lastHeading, &power, &groupedItems)
			case "h1", "h2", "h3", "h4", "h5", "h6":
				textContent := wikiCollectTextFromNode(n, 0)
				if strings.TrimSpace(textContent) != "" {
					lastHeading = textContent
					power = extractNumberFromString(n.Data)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			extractText(c)
		}
	}
	extractText(doc)

	var items []map[string]any

	for _, item := range groupedItems {
		texts := item["text"].([]string)
		if len(texts) > 0 {
			var contentBuilder strings.Builder
			for _, text := range texts {
				if text != item["title"] {
					contentBuilder.WriteString(text)
					contentBuilder.WriteString("\n\n")
				}
			}
			fullContent := strings.TrimSpace(contentBuilder.String())
			if fullContent != "" {
				items = append(items, map[string]any{
					"title":   item["title"],
					"pow":     item["pow"],
					"content": fullContent,
				})
			}
		}
	}

	if len(items) == 0 {
		return nil
	}

	output := OutputArticle{
		Title:  articleTitle,
		Entity: articleID,
		Items:  items,
		ID:     identifier,
	}
	return &output
}
