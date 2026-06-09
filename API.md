# WikiLite API Documentation

## Base URL
Standard REST API:
```
http://{host}:{port}/api
```
Model Context Protocol (MCP) Endpoint:
```
http://{host}:{port}/mcp
```
Default Host/Port: `http://localhost:35248`

## API Endpoints

### 1. Combined Search
Performs a search across lexical and semantic (if enabled).

**Endpoint:** `/search`  
**Methods:** GET, POST

#### Parameters
- `query` (required): Search query string
- `limit` (optional): Maximum number of results (default: 5)

#### GET Request
```
GET /api/search?query=linux&limit=5
```

#### POST Request
```json
POST /api/search
Content-Type: application/json

{
  "query": "linux",
  "limit": 5
}
```

#### Response
```json
{
  "status": "success",
  "time": 1.234,
  "results": [
    {
      "article_id": 123,
      "title": "Linux",
      "text": "Linux was created in 1991...",
      "type": "T",
      "power": 1.234
    }
  ]
}
```

### 2. Title Search
Searches only article titles using full-text search.

**Endpoint:** `/search/title`  
**Methods:** GET, POST

#### Parameters
- `query` (required): Search query string
- `limit` (optional): Maximum number of results (default: 5)

#### GET Request
```
GET /api/search/title?query=linux&limit=5
```

#### POST Request
```json
POST /api/search/title
Content-Type: application/json

{
  "query": "linux",
  "limit": 5
}
```

### 3. Lexical Search
Searches using full-text search.

**Endpoint:** `/search/lexical`  
**Methods:** GET, POST

#### Parameters
- `query` (required): Search query string
- `limit` (optional): Maximum number of results (default: 5)

#### GET Request
```
GET /api/search/lexical?query=linux&limit=5
```

#### POST Request
```json
POST /api/search/lexical
Content-Type: application/json

{
  "query": "linux",
  "limit": 5
}
```

### 4. Semantic Search
Searches using vector embeddings. Requires AI to be enabled.

**Endpoint:** `/search/semantic`  
**Methods:** GET, POST

#### Parameters
- `query` (required): Search query string
- `limit` (optional): Maximum number of results (default: 5)

#### GET Request
```
GET /api/search/semantic?query=linux&limit=5
```

#### POST Request
```json
POST /api/search/semantic
Content-Type: application/json

{
  "query": "linux",
  "limit": 5
}
```

### 5. Search Word Distance
Calculate the Levenshtein distance between the provided word and the entries in the internal database vocabulary to find the closest match.

**Endpoint:** `/search/distance`  
**Methods:** GET, POST

#### Parameters
- `query` (required): Search word
- `limit` (optional): Maximum number of results (default: 5)

#### GET Request
```
GET /api/search/distance?query=linux&limit=5
```

#### POST Request
```json
POST /api/search/distance
Content-Type: application/json

{
  "query": "linux",
  "limit": 5
}
```

### 6. Get Article
Retrieves a complete article by ID.

**Endpoint:** `/article`  
**Methods:** GET, POST

#### Parameters
- `id` (required): Article ID

#### GET Request
```
GET /api/article?id=123
```

#### POST Request
```json
POST /api/article
Content-Type: application/json

{
  "id": 123
}
```

#### Response
```json
{
  "status": "success",
  "time": 1.234,
  "article": {
    "id": 123,
    "title": "Linux",
    "entity": "Q388",
    "sections": [
      {
        "id": 1234,
        "title": "History",
        "content": "Linux was created in 1991..."
      },
      {
        "id": 12345,
        "title": "Design Philosophy",
        "content": "Linux follows Unix philosophy..."
      }
    ]
  }
}
```

### 7. Model Context Protocol (MCP)
Provides bidirectional communication over Server-Sent Events (SSE) and Streamable HTTP for integrating with compatible AI applications and development tools.

**Endpoint:** `/mcp`  
**Methods:** GET, POST

#### Parameters
- `session_id` (required for non-initialization requests): Unique session identifier, passed either via the `Mcp-Session-Id` header or the `session_id` query parameter.

#### GET Request (SSE Connection)
Used to establish the stateful, unidirectional server-to-client stream.
```
GET /mcp?session_id=a1b2c3d4e5f6
```

#### POST Request (JSON-RPC Handshake)
Establishes the initial connection. Does not require a session ID header.
```json
POST /mcp
Content-Type: application/json

{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": "2024-11-05",
    "capabilities": {},
    "clientInfo": {
      "name": "example-client",
      "version": "1.0.0"
    }
  }
}
```

#### Response
Returns `200 OK` alongside the assigned session ID inside the `Mcp-Session-Id` header.
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "protocolVersion": "2024-11-05",
    "capabilities": {
      "tools": {
        "listChanged": false
      },
      "prompts": {
        "listChanged": false
      }
    },
    "serverInfo": {
      "name": "wikilite",
      "version": "1.6.9"
    }
  }
}
```

#### POST Request (Tool Call Example)
Executes a tool on the server. Requires the returned `Mcp-Session-Id` header.
```json
POST /mcp
Mcp-Session-Id: a1b2c3d4e5f6
Content-Type: application/json

{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/call",
  "params": {
    "name": "search",
    "arguments": {
      "query": "linux",
      "limit": 1
    }
  }
}
```

#### Response
```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "Search results for 'linux' (limit 1):\n\n1. **Linux** (Article ID: 123)\n   Match Score: 100.00%\n   Snippet: Linux was created in 1991...\n\n"
      }
    ],
    "isError": false
  }
}
```

## Common Response Format

### Success Response
```json
{
  "status": "success",
  "time": 1.234,
  "results": [...],  // For search endpoints
  "article": [...]   // For article endpoint
}
```

### Error Response
```json
{
  "status": "error",
  "message": "Error description"
}
```

## Result Types
Search results include a `type` field indicating the source:
- `T`: Title match
- `C`: Content match
- `V`: Vector match

## Error Codes
The API uses standard HTTP status codes:
- `200`: Success
- `400`: Bad Request (invalid parameters)
- `500`: Internal Server Error

## Command Line Examples

### Using curl

1. Combined Search:
```bash
curl 'http://localhost:35248/api/search?query=linux&limit=5'
```

2. Title Search:
```bash
curl -X POST http://localhost:35248/api/search/title \
  -H 'Content-Type: application/json' \
  -d '{"query": "linux"}'
```

3. Get Article:
```bash
curl 'http://localhost:35248/api/article?id=123'
```

4. Initialize MCP Session Handshake:
```bash
curl -X POST http://localhost:35248/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"protocolVersion": "2024-11-05", "capabilities": {}, "clientInfo": {"name": "test", "version": "1.0"}}}'
```

## Configuration Requirements

### Semantic Search
To use semantic search, you have two options:
1. **Remote Server**:  
  You can pass the remote server URL using the `--ai-api-url` flag. This allows the system to connect to a remote server where the vector search functionality is hosted.
2. **Local Model**:
  Alternatively, local semantic search execution can be performed using the GGUF model embedded into the database. Only the qwen3-embeddings-q8.0 format is supported for querying, and it cannot be used for database indexing or embedding processing.

## Notes
- All search endpoints support both GET and POST methods
- The `limit` parameter is shared across all search types
- Semantic search requires additional configuration and services
- Results are deduplicated across search types in combined search
