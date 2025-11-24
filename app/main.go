// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
)

const Name = "wikilite"
const Version = "0.27.3"

type Config struct {
	aiAnn               bool
	aiAnnMode           string
	aiAnnSize           int
	aiApi               bool
	aiApiKey            string
	aiApiUrl            string
	aiModel             string
	aiModelImport       string
	aiModelPrefixSave   string
	aiModelPrefixSearch string
	aiThreads           int
	aiSync              bool
	cli                 bool
	dbPath              string
	dbCompress          bool
	help                bool
	language            string
	limit               int
	log                 bool
	logFile             string
	setup               bool
	web                 bool
	webBrowser          bool
	webHost             string
	webPort             int
	webTlsPrivate       string
	webTlsPublic        string
	wikiImport          string //https://dumps.wikimedia.org/other/enterprise_html/runs/...
}

var (
	ai      bool
	db      *DBHandler
	options *Config
)

func parseConfig() (*Config, error) {
	options = &Config{}
	flag.BoolVar(&options.aiAnn, "ai-ann", false, "Produce ANN vectors")
	flag.StringVar(&options.aiAnnMode, "ai-ann-mode", "", "Approximate Nearest Neighbor mode [mrl/binary]")
	flag.IntVar(&options.aiAnnSize, "ai-ann-size", 0, "ANN MRL size")
	flag.BoolVar(&options.aiApi, "ai-api", false, "Use API for embeddings generation")
	flag.StringVar(&options.aiApiKey, "ai-api-key", "", "AI API key")
	flag.StringVar(&options.aiApiUrl, "ai-api-url", "http://localhost:11434/v1/embeddings", "AI API url")
	flag.StringVar(&options.aiModel, "ai-model", "", "AI embedding model name")
	flag.StringVar(&options.aiModelImport, "ai-model-import", "", "Import AI model from file path")
	flag.StringVar(&options.aiModelPrefixSave, "ai-model-prefix-save", "", "AI embedding model task prefix to import a document")
	flag.StringVar(&options.aiModelPrefixSearch, "ai-model-prefix-search", "", "AI embedding model task prefix to perform a search")
	flag.IntVar(&options.aiThreads, "ai-threads", 0, "Embedding generation threads (default all)")
	flag.BoolVar(&options.aiSync, "ai-sync", false, "Generate embeddings")

	flag.BoolVar(&options.cli, "cli", false, "Interactive CLI search")

	flag.StringVar(&options.dbPath, "db", "wikilite.db", "SQLite database path")
	flag.BoolVar(&options.dbCompress, "db-compress", false, "Compress the database")

	flag.StringVar(&options.language, "language", "en", "Language code")
	flag.IntVar(&options.limit, "limit", 5, "Maximum number of search results")
	flag.BoolVar(&options.log, "log", false, "Enable logging")
	flag.StringVar(&options.logFile, "log-file", "", "Log file path")
	flag.BoolVar(&options.setup, "setup", false, "Download prebuild database")
	flag.BoolVar(&options.help, "help", false, "This help")

	flag.BoolVar(&options.web, "web", false, "Enable web interface")
	flag.BoolVar(&options.webBrowser, "web-browser", false, "Open the default browser to the web server address")
	flag.StringVar(&options.webHost, "web-host", "localhost", "Web server host")
	flag.IntVar(&options.webPort, "web-port", 35248, "Web server port")
	flag.StringVar(&options.webTlsPrivate, "web-tls-private", "", "TLS private certificate")
	flag.StringVar(&options.webTlsPublic, "web-tls-public", "", "TLS public certificate")

	flag.StringVar(&options.wikiImport, "wiki-import", "", "Wikipedia URL or file path to import")

	flag.Usage = func() {
		fmt.Println("Copyright:", "2024-2025 by Ubaldo Porcheddu <ubaldo@eja.it>")
		fmt.Println("Version:", Version)
		fmt.Printf("Usage: %s [options]\n", os.Args[0])
		fmt.Println("Options:\n")
		flag.PrintDefaults()
		fmt.Println()
	}

	flag.Parse()

	if options.aiThreads == 0 {
		options.aiThreads = runtime.NumCPU()
	}

	return options, nil
}

func main() {

	options, err := parseConfig()
	if err != nil {
		log.Fatalf("Error parsing command line: %v\n", err)
	}

	if options.help {
		flag.Usage()
		os.Exit(0)
	}

	if len(os.Args) == 1 {
		autoStart()
	}

	if options.setup {
		if _, err := os.Stat(options.dbPath); err == nil {
			log.Println("A database is already present in the current directory, skipping setup.")
		} else {
			Setup()
		}
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

	if err := aiInit(); err != nil {
		log.Printf("AI initialization error: %v\n", err)
	} else {
		ai = true
	}

	if options.aiSync || options.wikiImport != "" || options.aiModelImport != "" || options.dbCompress {
		if err := db.PragmaImportMode(); err != nil {
			log.Fatalf("Error setting database in import mode: %v\n", err)
		}

		if options.aiModelImport != "" {
			if err = db.AiModelImport(options.aiModelImport); err != nil {
				log.Fatalf("Error importing model file into the database: %v\n", err)
			}
		}

		if options.wikiImport != "" {
			if err = WikiImport(options.wikiImport); err != nil {
				log.Fatalf("Error processing import: %v\n", err)
			}
		}

		if ai && options.aiSync {
			if err := db.ProcessEmbeddings(); err != nil {
				log.Fatalf("Error processing embeddings: %v\n", err)
			}
		}

		if options.dbCompress {
			if err := db.Compress(); err != nil {
				log.Fatalf("Error compressing the database: %v\n", err)
			}
		}

		if err := db.PragmaReadMode(); err != nil {
			log.Fatalf("Error setting database in read mode: %v\n", err)
		}
	}

	if options.cli {
		SearchCli()
	}

	if options.web {
		if options.webBrowser {
			OpenBrowser(fmt.Sprintf("http://localhost:%d/", options.webPort), 1)
		}
		if err := WebStart(options.webHost, options.webPort); err != nil {
			log.Fatalf("Error starting web server: %v\n", err)
		}
	}

}
