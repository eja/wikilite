# Wikilite

Wikilite is a self-contained tool for creating a local SQLite database of Wikipedia articles, indexed with FTS5 for efficient lexical searching with optional semantic search capabilities through embedded embeddings. Built with Go, Wikilite provides command-line tools, a web interface, and a native Android application for offline access, browsing, and searching of Wikipedia content.

## Features

* **Lexical Search**: Utilizes FTS5 for efficient keyword-based searching within the SQLite database, ideal for exact word and phrase matching.
* **Optional Semantic Search**: Implements ANN quantization and MRL (Matryoshka Representation Learning) with text embeddings to find semantically similar content, effectively handling misspellings, morphological variations, and synonymy.
* **Flexible Embedding Options**: Supports native Qwen3 embedding generation directly in pure Go. Alternatively, can delegate embedding generation to external OpenAI-compatible APIs.
* **Cross-Platform**: Available for Linux, macOS, Windows, and as a native Android application.
* **Minimal Deployment**: Requires only the Wikilite executable and the database file on POSIX platforms.
* **Offline Operation**: Complete functionality without internet connectivity.
* **Dual Interfaces**: Command-line interface for terminal usage and web interface for browser-based access.
* **Interactive Wizard**: When started without command-line options, Wikilite enters an interactive mode that guides users through database setup and search operations.

## Installation

### Source Compilation
* Clone the repository: `git clone https://github.com/eja/wikilite.git`
* Build the binary: `make`
* Check the available options: `./build/bin/wikilite --help`

### Pre-built Binaries & Android App
Pre-compiled binaries for Linux, macOS, and Windows are available in the [latest release](https://github.com/eja/wikilite/releases/latest).

A native [Android application](https://github.com/eja/wikilite/releases/latest/download/wikilite-android.apk) is also available in the releases.
*   **External Storage Support**: If a `wikilite.db` file is already present in the external SD card, the Android app will detect and use it directly.
*   **In-App Download**: If no database is found on launch, the app provides an option to download a pre-built database.

## Usage

Wikilite can be used in several ways:

**Interactive Mode** (recommended for new users):
```bash
./wikilite
```
This launches a wizard that guides you through database installation, CLI search, and web interface setup.

**Direct Command Line**:
```bash
./wikilite --cli --db <file.db>
```

**Web Interface Only**:
```bash
./wikilite --web --db <file.db>
```
Access the interface at `http://localhost:35248`

## API Documentation

Wikilite provides a comprehensive RESTful API supporting both GET and POST methods. Key endpoints include:

* `/api/search`: Combined search across titles, content, and vectors
* `/api/search/title`: Title-specific search
* `/api/search/lexical`: Full-text search of titles and content
* `/api/search/semantic`: Vector-based semantic search
* `/api/search/distance`: Vocabulary distance search
* `/api/article`: Article retrieval by ID

All search endpoints support pagination via the `limit` parameter and return consistent JSON formatting. Complete API documentation is available in the [API specification](API.md).

## Semantic Search Implementation

The semantic search functionality identifies content with similar semantic meaning rather than relying solely on lexical matching. This provides enhanced search capabilities for:

* Query misspellings and typographical errors
* Conceptual similarity despite different terminology
* Synonym and related term matching
* Morphological variations (plurals, verb tenses)

Semantic search complements the FTS5 lexical search to deliver more comprehensive results.

### Embedding Modes

Wikilite supports two methods for generating and processing embeddings:

1. **Native (Pure Go)**: Runs locally without external dependencies using built-in support for Qwen3 embeddings.
2. **External API**: Delegates embedding generation to an external server (such as llama.cpp or an OpenAI-compatible service) via command-line flags.

#### Configuration Flags

* **Enable External API**: Pass the `-ai-api` flag to route embedding generation to an external service.
* **Endpoint URL**: Specify the API URL with `-ai-api-url` (defaults to `http://localhost:11434/v1/embeddings` for local llama.cpp instances).
* **Authentication**: Use `-ai-api-key` to supply your API authorization key if required.
* **Model Selection**: Define the target embedding model name using `-ai-model`.
* **ANN Tuning**: Adjust Approximate Nearest Neighbor settings using `-ai-ann`, `-ai-ann-mode [mrl/binary]`, and `-ai-ann-size`.
* **Synchronization**: Run `-ai-sync` to generate the missing embeddings for your database.

For example, to run an interactive CLI search utilizing a custom local llama.cpp instance for embeddings:
```bash
./wikilite -cli -db wikilite.db -ai-api -ai-api-url "http://localhost:11434/v1/embeddings" -ai-model "qwen3-embeddings"
```

## Pre-built Databases

Pre-configured databases for multiple languages are available on [Hugging Face](https://huggingface.co/datasets/eja/wikilite/tree/main). These can be installed directly through the setup command, the interactive wizard, or downloaded and extracted manually.

Databases in the "lexical" directory support full-text search only, while others include both lexical and semantic search capabilities.

## Acknowledgments

* **Wikipedia**: For providing the valuable data that powers Wikilite.
* **SQLite**: For providing the robust database engine that enables fast and efficient local data storage.
* **Qwen**: For the open-source text embedding models that power the native semantic search capabilities of Wikilite.
