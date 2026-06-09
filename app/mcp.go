// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Result  any    `json:"result,omitempty"`
	Error   any    `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type mcpSession struct {
	id       string
	sendChan chan string
}

var (
	mcpSessions   = make(map[string]*mcpSession)
	mcpSessionsMu sync.RWMutex
)

func generateSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func registerSession(sessionID string) *mcpSession {
	mcpSessionsMu.Lock()
	defer mcpSessionsMu.Unlock()
	sess := &mcpSession{
		id:       sessionID,
		sendChan: make(chan string, 100),
	}
	mcpSessions[sessionID] = sess
	return sess
}

func getSession(sessionID string) *mcpSession {
	mcpSessionsMu.RLock()
	defer mcpSessionsMu.RUnlock()
	return mcpSessions[sessionID]
}

func removeSession(sessionID string) {
	mcpSessionsMu.Lock()
	defer mcpSessionsMu.Unlock()
	delete(mcpSessions, sessionID)
}

func (s *WebServer) handleMCP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Mcp-Session-Id")
	w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method == "POST" {
		var req jsonRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON-RPC request", http.StatusBadRequest)
			return
		}

		sessionID := r.Header.Get("Mcp-Session-Id")
		if sessionID == "" {
			sessionID = r.URL.Query().Get("session_id")
		}

		if req.Method == "initialize" {
			if sessionID == "" {
				sessionID = generateSessionID()
			}
			registerSession(sessionID)
			w.Header().Set("Mcp-Session-Id", sessionID)
			w.Header().Set("Content-Type", "application/json")

			resp := handleJSONRPC(req)
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if sessionID == "" {
			http.Error(w, "Missing session_id", http.StatusBadRequest)
			return
		}

		sess := getSession(sessionID)
		if sess == nil {
			http.Error(w, "Session not found", http.StatusNotFound)
			return
		}

		resp := handleJSONRPC(req)
		if resp.JSONRPC != "" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		} else {
			w.WriteHeader(http.StatusAccepted)
		}
		return
	}

	if r.Method == "GET" {
		sessionID := r.Header.Get("Mcp-Session-Id")
		if sessionID == "" {
			sessionID = r.URL.Query().Get("session_id")
		}

		if sessionID == "" {
			http.Error(w, "Missing session_id", http.StatusBadRequest)
			return
		}

		sess := getSession(sessionID)
		if sess == nil {
			http.Error(w, "Session not found", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		msgURL := fmt.Sprintf("%s://%s/mcp?session_id=%s", scheme, r.Host, sessionID)

		fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", msgURL)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}

		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()

		ctx := r.Context()

		for {
			select {
			case msg := <-sess.sendChan:
				fmt.Fprintf(w, "event: message\ndata: %s\n\n", msg)
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
			case <-ticker.C:
				fmt.Fprint(w, ":ping\n\n")
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
			case <-ctx.Done():
				return
			}
		}
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func handleJSONRPC(req jsonRPCRequest) jsonRPCResponse {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
	}

	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"tools": map[string]any{
					"listChanged": false,
				},
				"prompts": map[string]any{
					"listChanged": false,
				},
			},
			"serverInfo": map[string]any{
				"name":    Name,
				"version": Version,
			},
		}

	case "notifications/initialized":
		return jsonRPCResponse{}

	case "ping":
		resp.Result = map[string]any{}

	case "prompts/list":
		resp.Result = map[string]any{
			"prompts": []any{},
		}

	case "prompts/get":
		resp.Error = jsonRPCError{
			Code:    -32602,
			Message: "Prompt not found",
		}

	case "tools/list":
		resp.Result = map[string]any{
			"tools": []map[string]any{
				{
					"name":        "search",
					"description": "Search the local Wikilite database using lexical or semantic options.",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"query": map[string]any{
								"type":        "string",
								"description": "The search query to find relevant Wikipedia articles.",
							},
							"limit": map[string]any{
								"type":        "integer",
								"description": "Optional maximum number of search results to return.",
								"default":     25,
							},
						},
						"required": []string{"query"},
					},
				},
				{
					"name":        "get_article",
					"description": "Retrieve the full text and all sections of a Wikipedia article by its integer ID.",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id": map[string]any{
								"type":        "integer",
								"description": "The unique integer ID of the article to retrieve.",
							},
						},
						"required": []string{"id"},
					},
				},
			},
		}

	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp.Error = jsonRPCError{
				Code:    -32602,
				Message: "Invalid params",
			}
			return resp
		}

		resp.Result = handleToolCall(params.Name, params.Arguments)

	default:
		resp.Error = jsonRPCError{
			Code:    -32601,
			Message: fmt.Sprintf("Method not found: %s", req.Method),
		}
	}

	return resp
}

func handleToolCall(name string, args map[string]any) map[string]any {
	switch name {
	case "search":
		query, _ := args["query"].(string)
		if query == "" {
			return map[string]any{
				"content": []map[string]any{
					{
						"type": "text",
						"text": "Error: query parameter is empty or missing.",
					},
				},
				"isError": true,
			}
		}

		limit := options.limit
		if limitVal, ok := args["limit"]; ok {
			if f, ok := limitVal.(float64); ok {
				limit = int(f)
			} else if i, ok := limitVal.(int); ok {
				limit = i
			}
		}

		results, err := Search(query, limit)
		if err != nil {
			return map[string]any{
				"content": []map[string]any{
					{
						"type": "text",
						"text": fmt.Sprintf("Error performing search: %v", err),
					},
				},
				"isError": true,
			}
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Search results for '%s' (limit %d):\n\n", query, limit))
		if len(results) == 0 {
			sb.WriteString("No articles found matching the query.")
		} else {
			for i, r := range results {
				sb.WriteString(fmt.Sprintf("%d. **%s** (Article ID: %d)\n", i+1, r.Title, r.ArticleID))
				sb.WriteString(fmt.Sprintf("   Match Score: %.2f%%\n", r.Power))
				if r.Snippet != "" {
					sb.WriteString(fmt.Sprintf("   Snippet: %s\n", r.Snippet))
				}
				sb.WriteString("\n")
			}
		}

		return map[string]any{
			"content": []map[string]any{
				{
					"type": "text",
					"text": sb.String(),
				},
			},
			"isError": false,
		}

	case "get_article":
		idVal, ok := args["id"]
		if !ok {
			return map[string]any{
				"content": []map[string]any{
					{
						"type": "text",
						"text": "Error: id parameter is missing.",
					},
				},
				"isError": true,
			}
		}

		var id int
		if f, ok := idVal.(float64); ok {
			id = int(f)
		} else if i, ok := idVal.(int); ok {
			id = i
		} else {
			return map[string]any{
				"content": []map[string]any{
					{
						"type": "text",
						"text": "Error: id must be an integer.",
					},
				},
				"isError": true,
			}
		}

		article, err := db.ArticleGet(id)
		if err != nil {
			return map[string]any{
				"content": []map[string]any{
					{
						"type": "text",
						"text": fmt.Sprintf("Error retrieving article ID %d: %v", id, err),
					},
				},
				"isError": true,
			}
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("# %s\n", article.Title))
		if article.Entity != "" {
			sb.WriteString(fmt.Sprintf("Entity Identifier: %s\n", article.Entity))
		}
		sb.WriteString("\n")

		for _, sec := range article.Sections {
			if sec.Title != "" {
				sb.WriteString(fmt.Sprintf("## %s\n", sec.Title))
			}
			sb.WriteString(sec.Content)
			sb.WriteString("\n\n")
		}

		return map[string]any{
			"content": []map[string]any{
				{
					"type": "text",
					"text": sb.String(),
				},
			},
			"isError": false,
		}

	default:
		return map[string]any{
			"content": []map[string]any{
				{
					"type": "text",
					"text": fmt.Sprintf("Error: Unknown tool %s", name),
				},
			},
			"isError": true,
		}
	}
}
