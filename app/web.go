// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

type APIRequest struct {
	Query string `json:"query,omitempty"`
	Limit int    `json:"limit,omitempty"`
	ID    int    `json:"id,omitempty"`
}

type APIResponse struct {
	Status  string          `json:"status"`
	Message string          `json:"message,omitempty"`
	Results *[]SearchResult `json:"results,omitempty"`
	Article *ArticleResult  `json:"article,omitempty"`
	Time    float64         `json:"time"`
}

type WebServer struct {
	template *template.Template
}

func NewWebServer() (*WebServer, error) {
	tmpl := template.New("").Funcs(template.FuncMap{
		"safeHTML": func(s string) template.HTML {
			return template.HTML(s)
		},
	})

	var err error
	tmpl, err = tmpl.ParseFS(assets, "assets/templates/*")
	if err != nil {
		return nil, fmt.Errorf("error parsing templates: %v", err)
	}

	return &WebServer{
		template: tmpl,
	}, nil
}

func (s *WebServer) executeTemplate(w http.ResponseWriter, templateName string, data interface{}) {
	err := s.template.ExecuteTemplate(w, templateName, data)
	if err != nil {
		http.Error(w, fmt.Sprintf("error executing template: %v", err), http.StatusInternalServerError)
		return
	}
}

func (s *WebServer) handleHTMLSearch(w http.ResponseWriter, r *http.Request) {
	var err error
	var query string
	var limit int
	var results []SearchResult

	if r.Method == "POST" {
		query = r.FormValue("query")
		limit, _ = strconv.Atoi(r.FormValue("limit"))
	}

	if limit <= 0 {
		limit = options.limit
	}

	if query != "" {
		results, err = Search(query, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	s.executeTemplate(w, "search.html", struct {
		Query    string
		Limit    int
		Results  []SearchResult
		HasQuery bool
		Language string
		AI       bool
	}{
		Query:    query,
		Limit:    limit,
		Results:  results,
		HasQuery: query != "",
		Language: options.language,
		AI:       ai,
	})
}

func (s *WebServer) handleHTMLArticle(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		value := r.FormValue("id")
		id, err := strconv.Atoi(value)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result, err := db.ArticleGet(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		s.executeTemplate(w, "article.html", struct {
			Language string
			Result   ArticleResult
		}{
			Language: options.language,
			Result:   result,
		})
	}
}

func (s *WebServer) sendAPIError(w http.ResponseWriter, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(APIResponse{
		Status:  "error",
		Message: message,
	})
}

func (s *WebServer) handleGenericAPISearch(w http.ResponseWriter, r *http.Request, searchFunc func(query string, limit int) ([]SearchResult, error)) {
	w.Header().Set("Content-Type", "application/json")

	var request APIRequest
	var query string
	var limit int = options.limit
	var err error

	startTime := time.Now()

	if r.Method == "POST" {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			s.sendAPIError(w, "Invalid JSON request", http.StatusBadRequest)
			return
		}
		query = request.Query
		if request.Limit > 0 {
			limit = request.Limit
		}
	} else {
		query = r.URL.Query().Get("query")
		if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
			limit, err = strconv.Atoi(limitStr)
			if err != nil {
				s.sendAPIError(w, "Invalid limit parameter", http.StatusBadRequest)
				return
			}
		}
	}
	log.Printf("API %s search: %s", r.Method, query)

	if query == "" {
		s.sendAPIError(w, "Query parameter is required", http.StatusBadRequest)
		return
	}

	results, err := searchFunc(query, limit)
	if err != nil {
		s.sendAPIError(w, fmt.Sprintf("Search error: %v", err), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(APIResponse{
		Status:  "success",
		Results: &results,
		Time:    time.Since(startTime).Seconds(),
	})
}

func (s *WebServer) handleAPISearch(w http.ResponseWriter, r *http.Request) {
	s.handleGenericAPISearch(w, r, Search)
}

func (s *WebServer) handleAPISearchTitle(w http.ResponseWriter, r *http.Request) {
	s.handleGenericAPISearch(w, r, SearchTitle)
}

func (s *WebServer) handleAPISearchLexical(w http.ResponseWriter, r *http.Request) {
	s.handleGenericAPISearch(w, r, SearchLexical)
}

func (s *WebServer) handleAPISearchWordDistance(w http.ResponseWriter, r *http.Request) {
	s.handleGenericAPISearch(w, r, SearchWordDistance)
}

func (s *WebServer) handleAPISearchSemantic(w http.ResponseWriter, r *http.Request) {
	if !ai {
		s.sendAPIError(w, "Semantic search is not enabled", http.StatusBadRequest)
		return
	}
	s.handleGenericAPISearch(w, r, SearchSemantic)
}

func (s *WebServer) handleAPIArticle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var request APIRequest
	var id int
	var err error

	startTime := time.Now()

	if r.Method == "POST" {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			s.sendAPIError(w, "Invalid JSON request", http.StatusBadRequest)
			return
		}
		id = request.ID
	} else {
		idStr := r.URL.Query().Get("id")
		if idStr == "" {
			s.sendAPIError(w, "ID parameter is required", http.StatusBadRequest)
			return
		}
		id, err = strconv.Atoi(idStr)
		if err != nil {
			s.sendAPIError(w, "Invalid ID parameter", http.StatusBadRequest)
			return
		}
	}
	log.Printf("API %s article: %d", r.Method, id)

	article, err := db.ArticleGet(id)
	if err != nil {
		s.sendAPIError(w, fmt.Sprintf("Error retrieving article: %v", err), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(APIResponse{
		Status:  "success",
		Article: &article,
		Time:    time.Since(startTime).Seconds(),
	})
}

func (s *WebServer) handleHome(w http.ResponseWriter, r *http.Request) {
	s.handleHTMLSearch(w, r)
}

func (s *WebServer) Start(host string, port int) error {

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleHome)
	mux.HandleFunc("/article", s.handleHTMLArticle)

	mux.HandleFunc("/api/search", s.handleAPISearch)
	mux.HandleFunc("/api/search/title", s.handleAPISearchTitle)
	mux.HandleFunc("/api/search/lexical", s.handleAPISearchLexical)
	mux.HandleFunc("/api/search/semantic", s.handleAPISearchSemantic)
	mux.HandleFunc("/api/search/distance", s.handleAPISearchWordDistance)
	mux.HandleFunc("/api/article", s.handleAPIArticle)

	subFS, err := fs.Sub(assets, "assets/static")
	if err != nil {
		panic(fmt.Errorf("failed to access embedded subdirectory: %w", err))
	}

	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(subFS))))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", Name+"/"+Version)
		mux.ServeHTTP(w, r)
	})

	address := fmt.Sprintf("%s:%d", host, port)
	if options.webTlsPrivate != "" && options.webTlsPublic != "" {
		if _, err := os.Stat(options.webTlsPrivate); err != nil {
			return fmt.Errorf("failed to open private certificate")
		} else if _, err := os.Stat(options.webTlsPublic); err != nil {
			return fmt.Errorf("failed to open public certificate")
		} else {
			log.Println("Starting server on https://" + address)
			if err := http.ListenAndServeTLS(address, options.webTlsPublic, options.webTlsPrivate, nil); err != nil {
				return err
			}
		}
	} else {
		log.Println("Starting server on http://" + address)
		if err := http.ListenAndServe(address, handler); err != nil {
			return err
		}
	}

	return nil
}

func WebStart(host string, port int) error {
	server, err := NewWebServer()
	if err != nil {
		return err
	}

	if err := server.Start(host, port); err != nil {
		return err
	}

	return nil
}
