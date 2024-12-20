// Copyright (C) 2024 by Ubaldo Porcheddu <ubaldo@eja.it>

package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
)

const Version = "0.0.26"

type Config struct {
	importPath       string //https://dumps.wikimedia.org/other/enterprise_html/runs/...
	dbPath           string
	web              bool
	webHost          string
	webPort          int
	ai               bool
	aiApiKey         string
	aiEmbeddingModel string
	aiEmbeddingSize  int
	aiEmbeddingBatch int
	aiLlmModel       string
	aiSync           bool
	aiUrl            string
	qdrant           bool
	qdrantHost       string
	qdrantPort       int
	qdrantSync       bool
	qdrantCollection string
	optimize         bool
	log              bool
	logFile          string
	cli              bool
	limit            int
	locale           string
	language         string
}

var (
	db      *DBHandler
	qd      *QdrantHandler
	options *Config
)

func parseConfig() (*Config, error) {
	options = &Config{}
	flag.BoolVar(&options.ai, "ai", false, "Enable AI")
	flag.BoolVar(&options.aiSync, "ai-sync", false, "AI embeddings sync")
	flag.IntVar(&options.aiEmbeddingSize, "ai-embedding-size", 1024, "AI embedding size")
	flag.IntVar(&options.aiEmbeddingBatch, "ai-embedding-batch", 1000, "AI embedding batch processing")
	flag.StringVar(&options.aiEmbeddingModel, "ai-embedding-model", "bge-m3", "AI embedding model")
	flag.StringVar(&options.aiLlmModel, "ai-llm-model", "gemma2", "AI LLM model")
	flag.StringVar(&options.aiUrl, "ai-url", "http://localhost:11434/v1/", "AI base url")
	flag.StringVar(&options.aiApiKey, "ai-api-key", "", "AI API key")
	flag.StringVar(&options.dbPath, "db", "wikilite.db", "SQLite database path")
	flag.StringVar(&options.importPath, "import", "", "URL or file path to import")
	flag.BoolVar(&options.web, "web", false, "Enable web interface")
	flag.StringVar(&options.webHost, "web-host", "localhost", "Web server host")
	flag.IntVar(&options.webPort, "web-port", 35248, "Web server port")
	flag.BoolVar(&options.qdrant, "qdrant", false, "Enable Qdrant")
	flag.StringVar(&options.qdrantHost, "qdrant-host", "localhost", "Qdrant server host")
	flag.BoolVar(&options.qdrantSync, "qdrant-sync", false, "Qdrant embeddings sync")
	flag.IntVar(&options.qdrantPort, "qdrant-port", 6334, "Qdrant server port")
	flag.StringVar(&options.qdrantCollection, "qdrant-collection", "wikilite", "Qdrant collection")
	flag.BoolVar(&options.optimize, "optimize", false, "Optimize the database")
	flag.BoolVar(&options.log, "log", false, "Enable logging")
	flag.StringVar(&options.logFile, "log-file", "", "Log file path")
	flag.BoolVar(&options.cli, "cli", false, "Interactive search")
	flag.IntVar(&options.limit, "limit", 5, "Maximum number of search results")
	flag.StringVar(&options.language, "language", "en", "Language")

	flag.Usage = func() {
		fmt.Println("Copyright:", "2024 by Ubaldo Porcheddu <ubaldo@eja.it>")
		fmt.Println("Version:", Version)
		fmt.Printf("Usage: %s [options]\n", os.Args[0])
		fmt.Println("Options:\n")
		flag.PrintDefaults()
		fmt.Println()
	}

	flag.Parse()

	return options, nil
}

func main() {
	options, err := parseConfig()
	if err != nil {
		log.Fatalf("Error parsing command line: %v\n\n", err)
	}

	if flag.NFlag() == 0 {
		flag.Usage()
		os.Exit(0)
	}

	if options.log || options.logFile != "" {
		if options.logFile != "" {
			logFile, err := os.OpenFile(options.logFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error opening log file: %v\n", err)
			}
			log.SetOutput(logFile)
		}
	} else {
		log.SetOutput(io.Discard)
	}

	db, err = NewDBHandler(options.dbPath)
	if err != nil {
		log.Fatalf("Error initializing database: %v\n", err)
	}
	defer db.Close()

	if options.importPath != "" {
		if strings.HasPrefix(options.importPath, "http://") || strings.HasPrefix(options.importPath, "https://") {
			err = wikiDownloadAndProcessFile(options.importPath)
		} else {
			err = wikiProcessLocalFile(options.importPath)
		}
		if err != nil {
			log.Fatalf("Error processing import: %v\n", err)
		}
	}

	if options.aiSync {
		err = db.ProcessEmbeddings()
		if err != nil {
			log.Fatalf("Error processing database embeddings: %v\n", err)
		}
	}

	if options.qdrant {
		qd, err = qdrantInit(options.qdrantHost, options.qdrantPort, options.qdrantCollection, options.aiEmbeddingSize)
		if err != nil {
			log.Fatalf("Failed to initialize qdrant: %v", err)
		}

		if options.qdrantSync {
			for {
				embedding, err := db.GetEmbedding(1)
				if embedding == nil {
					break
				}

				exists, err := qdrantCheckIfHashExists(qd.PointsClient, options.qdrantCollection, embedding.Hash)
				if !exists && err == nil {
					err = qdrantUpsertPoint(qd.PointsClient, qd.Collection, embedding.Hash, embedding.Vectors)
					if err != nil {
						log.Printf("Error upserting point to qdrant: %v", err)
						continue
					}
				}

				err = db.UpdateEmbeddingStatus(embedding.Hash, 2)
				if err != nil {
					log.Fatalf("Error updating embedding status in database: %v", err)
				}
			}
		}
	}

	if options.optimize {
		if err := db.ClearEmbeddings(); err != nil {
			log.Printf("Error clearing database embeddings: %v", err)
		}
	}

	if options.cli {
		searchCli()
	}

	if options.web {
		server, err := NewWebServer()
		if err != nil {
			log.Fatalf("Error creating web server: %v\n", err)
		}

		if err := server.Start(options.webHost, options.webPort); err != nil {
			log.Fatalf("Error starting web server: %v\n", err)
		}
	}

}
