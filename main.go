// Copyright (C) 2024 by Ubaldo Porcheddu <ubaldo@eja.it>

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
)

const Version = "0.0.15"

type Config struct {
	importPath       string //https://dumps.wikimedia.org/other/enterprise_html/runs/...
	dbPath           string
	web              bool
	webHost          string
	webPort          int
	ai               bool
	aiApiKey         string
	aiModelEmbedding string
	aiModelLLM       string
	aiUrl            string
	qdrant           bool
	qdrantHost       string
	qdrantPort       int
	qdrantCollection string
}

var (
	db      *DBHandler
	options *Config
)

func parseConfig() (*Config, error) {
	options = &Config{}
	flag.BoolVar(&options.ai, "ai", false, "Enable AI")
	flag.StringVar(&options.aiModelEmbedding, "ai-model-embedding", "bge-m3", "AI embedding model")
	flag.StringVar(&options.aiModelLLM, "ai-model-llm", "gemma2", "AI LLM model")
	flag.StringVar(&options.aiUrl, "ai-url", "http://localhost:11434/v1/", "AI base url")
	flag.StringVar(&options.aiApiKey, "ai-api-key", "", "AI API key")
	flag.StringVar(&options.dbPath, "db", "wikilite.db", "SQLite database path")
	flag.StringVar(&options.importPath, "import", "", "URL or file path to import")
	flag.BoolVar(&options.web, "web", false, "Enable web interface")
	flag.StringVar(&options.webHost, "web-host", "localhost", "Web server host")
	flag.IntVar(&options.webPort, "web-port", 35248, "Web server port")
	flag.BoolVar(&options.qdrant, "qdrant", false, "Enable Qdrant")
	flag.StringVar(&options.qdrantHost, "qdrant-host", "localhost", "Qdrant server host")
	flag.IntVar(&options.qdrantPort, "qdrant-port", 6334, "Qdrant server port")
	flag.StringVar(&options.qdrantCollection, "qdrant-collection", "wikilite", "Qdrant collection")

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

	if options.ai {
		err = db.ProcessEmbeddings()
		if err != nil {
			log.Fatalf("Error processing database embeddings: %v\n", err)
		}
	}

	if options.qdrant {
		embedding, err := db.GetEmbedding(1)
		if err != nil {
			log.Fatalf("Error getting embedding from database: %v", err)
		}
		if embedding != nil {
			embeddingSize := len(embedding.Hash) * len(embedding.Hash)
			qdrant, err := qdrantInit(options.qdrantHost, options.qdrantPort, options.qdrantCollection, embeddingSize)
			if err != nil {
				log.Fatalf("Failed to initialize qdrant: %v", err)
			}

			for {
				embedding, err := db.GetEmbedding(1)
				if embedding == nil {
					break
				}

				err = qdrantUpsertPoint(qdrant.PointsClient, qdrant.Collection, embedding.Hash, embedding.Vectors)
				if err != nil {
					log.Printf("Error upserting point to qdrant: %v", err)
					continue
				}

				err = db.UpdateEmbeddingStatus(embedding.Hash, 2)
				if err != nil {
					log.Fatalf("Error updating embedding status in database: %v", err)
				}

			}
		}
	}

	if options.web {
		server, err := NewWebServer(db)
		if err != nil {
			log.Fatalf("Error creating web server: %v\n", err)
		}

		if err := server.Start(options.webHost, options.webPort); err != nil {
			log.Fatalf("Error starting web server: %v\n", err)
		}
	}

}
