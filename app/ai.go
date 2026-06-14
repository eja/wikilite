// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"time"
)

type aiEmbeddingRequest struct {
	Model          string `json:"model"`
	Input          any    `json:"input"`
	EncodingFormat string `json:"encoding_format,omitempty"`
}

type aiEmbeddingResponse struct {
	Object string `json:"object"`
	Data   []struct {
		Object    string    `json:"object"`
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Model string `json:"model"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

func aiInit() (err error) {
	if !options.aiApi {
		if err := localAiInit(options.aiModel); err != nil {
			return fmt.Errorf("AI error loading local embedding model: %v", err)
		}
	}

	if _, err := aiEmbeddings("test"); err != nil {
		return fmt.Errorf("AI error loading embedding model: %v", err)
	}

	return nil
}

func aiEmbeddings(input string) ([]float32, error) {
	if options.aiApi {
		return aiApiEmbeddings(input)
	}
	return localAiEmbeddings(input)
}

func aiApiEmbeddings(input string) (output []float32, err error) {
	url := options.aiApiUrl
	payload := aiEmbeddingRequest{
		Model:          options.aiModel,
		Input:          []string{input},
		EncodingFormat: "float",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal embedding request: %v", err)
	}

	req, err := http.NewRequestWithContext(context.TODO(), "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if options.aiApiKey != "" {
		req.Header.Set("Authorization", "Bearer "+options.aiApiKey)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	var apiResp aiEmbeddingResponse
	if decodeErr := json.NewDecoder(resp.Body).Decode(&apiResp); decodeErr != nil {
		return nil, fmt.Errorf("failed to decode response (status %d): %v", resp.StatusCode, decodeErr)
	}

	if resp.StatusCode != http.StatusOK {
		if apiResp.Error.Message != "" {
			return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, apiResp.Error.Message)
		}
		return nil, fmt.Errorf("API request failed with status %d", resp.StatusCode)
	}

	if len(apiResp.Data) == 0 {
		return nil, fmt.Errorf("no embeddings returned")
	}

	return apiResp.Data[0].Embedding, nil
}

func aiApiEmbeddingsBatch(inputs []string) ([][]float32, error) {
	url := options.aiApiUrl
	payload := aiEmbeddingRequest{
		Model:          options.aiModel,
		Input:          inputs,
		EncodingFormat: "float",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal batch request: %v", err)
	}

	req, err := http.NewRequestWithContext(context.TODO(), "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if options.aiApiKey != "" {
		req.Header.Set("Authorization", "Bearer "+options.aiApiKey)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	var apiResp aiEmbeddingResponse
	if decodeErr := json.NewDecoder(resp.Body).Decode(&apiResp); decodeErr != nil {
		return nil, fmt.Errorf("failed to decode response (status %d): %v", resp.StatusCode, decodeErr)
	}

	if resp.StatusCode != http.StatusOK {
		if apiResp.Error.Message != "" {
			return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, apiResp.Error.Message)
		}
		return nil, fmt.Errorf("API request failed with status %d", resp.StatusCode)
	}

	if len(apiResp.Data) != len(inputs) {
		return nil, fmt.Errorf("returned embedding count (%d) mismatch with inputs (%d)", len(apiResp.Data), len(inputs))
	}

	sort.Slice(apiResp.Data, func(i, j int) bool {
		return apiResp.Data[i].Index < apiResp.Data[j].Index
	})

	results := make([][]float32, len(inputs))
	for i, d := range apiResp.Data {
		results[i] = d.Embedding
	}

	return results, nil
}

func localAiEnabled() bool {
	return true
}

func localAiInit(modelPath string) error {
	globalMu.Lock()
	defer globalMu.Unlock()

	if !options.aiCache {
		file, err := os.Open(options.dbPath)
		if err == nil {
			buf := make([]byte, 100)
			_, err = file.ReadAt(buf, 0)
			if err == nil {
				pageSize := int64(binary.BigEndian.Uint16(buf[16:18]))
				if pageSize == 1 {
					pageSize = 65536
				}
				rootPage, err := findSetupRootPage(file, pageSize)
				if err == nil {
					firstOverflowPage, blobSize, err := findGGUFCell(file, rootPage, pageSize)
					if err == nil {
						pages, err := mapOverflowPages(file, firstOverflowPage, pageSize)
						if err == nil && len(pages) > 0 {
							globalPageSize = pageSize
							globalBlobSize = blobSize
							globalPages = pages
							globalUseCustomStream = true
						}
					}
				}
			}
			file.Close()
		}
	}

	r, closeFn, err := openGGUFStream()
	if err != nil {
		return err
	}

	if closeFn != nil {
		defer closeFn()
	}

	p, err := NewGGUFParser(NewBufferedReadSeeker(r, 256*1024))
	if err != nil {
		return err
	}

	tok, err := loadTokenizer(p)
	if err != nil {
		return err
	}
	mdl, err := loadModel(p)
	if err != nil {
		return err
	}

	if !options.aiCache {
		p.r = nil
	}

	globalTok = tok
	globalMdl = mdl

	return nil
}

func localAiEmbeddings(input string) ([]float32, error) {
	globalMu.Lock()
	defer globalMu.Unlock()

	tok := globalTok
	mdl := globalMdl
	if tok == nil || mdl == nil {
		if !options.aiCache {
			r, closeFn, err := openGGUFStream()
			if err != nil {
				return nil, err
			}

			if closeFn != nil {
				defer closeFn()
			}

			p, err := NewGGUFParser(NewBufferedReadSeeker(r, 256*1024))
			if err != nil {
				return nil, err
			}

			tok, err = loadTokenizer(p)
			if err != nil {
				return nil, err
			}

			mdl, err = loadModel(p)
			if err != nil {
				return nil, err
			}

			if closeFn != nil {
				closeFn()
			}

			globalTok = tok
			globalMdl = mdl
		} else {
			return nil, fmt.Errorf("model not initialized")
		}
	}

	ids := globalTok.encode(input)
	if len(ids) == 0 {
		return nil, fmt.Errorf("empty input")
	}

	if !options.aiCache {
		r, closeFn, err := openGGUFStream()
		if err != nil {
			return nil, err
		}

		globalMdl.parser.r = NewBufferedReadSeeker(r, 256*1024)

		emb, err := globalMdl.embed(ids)

		if closeFn != nil {
			closeFn()
		}

		globalMdl.parser.r = nil

		return emb, err
	}

	emb, err := globalMdl.embed(ids)
	return emb, err
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
