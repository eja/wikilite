// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

var (
	globalTok *bpeTokenizer
	globalMdl *qwen3Model
	globalMu  sync.RWMutex
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

type modelFS interface {
	ReadFile(name string) ([]byte, error)
	ListFiles() ([]string, error)
}

type diskFS string

func (d diskFS) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(string(d), name))
}

func (d diskFS) ListFiles() ([]string, error) {
	entries, err := os.ReadDir(string(d))
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() {
			files = append(files, e.Name())
		}
	}
	return files, nil
}

type memFS map[string][]byte

func (m memFS) ReadFile(name string) ([]byte, error) {
	data, ok := m[name]
	if !ok {
		base := filepath.Base(name)
		if data, ok = m[base]; ok {
			return data, nil
		}
		return nil, os.ErrNotExist
	}
	return data, nil
}

func (m memFS) ListFiles() ([]string, error) {
	var files []string
	for k := range m {
		files = append(files, k)
	}
	return files, nil
}

func loadTarGzModel(data []byte) (memFS, error) {
	gzr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	m := make(memFS)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag == tar.TypeReg {
			var buf bytes.Buffer
			if _, err := io.Copy(&buf, tr); err != nil {
				return nil, err
			}
			name := filepath.Base(hdr.Name)
			m[name] = buf.Bytes()
		}
	}
	return m, nil
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

func aiInternal() bool {
	return !options.aiApi
}

func aiApiEmbeddings(input string) (output []float32, err error) {
	url := options.aiApiUrl
	payload := aiEmbeddingRequest{
		Model:          options.aiModel,
		Input:          input,
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

	client := &http.Client{}
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

func localAiEnabled() bool {
	return true
}

func localAiInit(modelPath string) error {
	globalMu.Lock()
	defer globalMu.Unlock()

	var fs modelFS
	if dbData := db.AiModelLoad(); len(dbData) > 0 {
		mfs, err := loadTarGzModel(dbData)
		if err != nil {
			return fmt.Errorf("failed to extract model archive from DB: %w", err)
		}
		fs = mfs
	} else if modelPath != "" {
		fs = diskFS(modelPath)
	} else {
		return fmt.Errorf("no model path specified and no model found in database")
	}

	tok, err := loadTokenizer(fs)
	if err != nil {
		return err
	}
	mdl, err := loadModel(fs)
	if err != nil {
		return err
	}
	globalTok = tok
	globalMdl = mdl
	return nil
}

func localAiEmbeddings(input string) ([]float32, error) {
	globalMu.RLock()
	tok := globalTok
	mdl := globalMdl
	globalMu.RUnlock()
	if tok == nil || mdl == nil {
		return nil, fmt.Errorf("model not initialized")
	}
	ids := tok.encode(input)
	if len(ids) == 0 {
		return nil, fmt.Errorf("empty input")
	}
	return mdl.embed(ids), nil
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
