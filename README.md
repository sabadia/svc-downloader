## svc-downloader — File Downloader Microservice

### Overview
`svc-downloader` is a standalone HTTP microservice for resilient, segmented file downloads with queueing, rate limiting, retries/backoff, and progress events via Server-Sent Events (SSE). It stores metadata in BadgerDB and downloads files to the local filesystem.

- **Module**: `github.com/sabadia/svc-downloader`
- **Default port**: `8089` (override with `PORT` env)
- **Data dir**: `./data/badger` (created automatically)

### Key Features
- **Queue-based scheduling** with per-queue concurrency and optional queue-level rate limits
- **Automatic workers** that periodically start queued downloads
- **Segmented downloads** with range requests and resume support when the server provides `Accept-Ranges`
- **Retry & backoff** with jitter for robust transfers
- **Per-download, per-queue, and global rate limiting** hooks
- **Local filestore** with temp segment files merged on completion; optional checksum verification (`md5`, `sha256`)
- **Powerful filtering** for listing and counting downloads by status, queue, tags, and search
- **SSE events** for live status updates (`/events`)
- **Simple HTTP API** built with Huma + Chi

### High-level Architecture
- **API layer**: `internal/api` (Huma/Chi routes)
- **Services**: `internal/service`
  - `DownloadService`: lifecycle and control plane for downloads
  - `QueueService`: queue CRUD, pause/resume, bulk operations, stats
  - `WorkerManager`: background scheduler that starts queued downloads up to queue concurrency (tick every 2s)
  - `DownloadRunner`: executes segmented transfers and emits progress/completion/failure events
- **Storage**:
  - **Repository**: `internal/repository` (BadgerDB) for downloads, segments, queues, stats
  - **FileStore**: `internal/filestore` for local temp parts and final file merge
- **Networking**: `internal/transport` HTTP client with HEAD fallback, redirects, TLS, proxies, cookies/headers
- **Events**: in-memory publisher (`internal/events`) with SSE endpoint
- **Rate limiting**: simple token-bucket impl (`internal/ratelimit`)

### Data Model (selected)
- **Download**
  - `id`, `url`, `queue_id`, `priority`, `tags[]`
  - `request` (URL, optional mirrors, headers/cookies via `extra`)
  - `config` (max connections, timeouts, proxy/TLS/auth, retries/backoff, rate limit, accept-ranges toggle)
  - `file` (path, filename, temp dir, overwrite/unique, checksum)
  - `status` (`pending|queued|running|paused|completed|failed|cancelled`)
  - `bytes_total`, `bytes_completed`, `progress`, timestamps
- **Queue**
  - `id`, `name`, `concurrency`, `rate_limit`, `default`, `paused`, `retry_policy`, `config`
- **QueueStats**
  - `queue_id`, `num_pending`, `num_running`, `num_completed`, `num_failed`, `bytes_per_sec`

### Configuration
- **Environment**
  - `PORT`: HTTP listen port (default: 8089)
- **Defaults (hard-coded in code)**
  - BadgerDB path: `./data/badger`
  - Default queue on first run: `main` with `concurrency=32`, `default=true`
  - Graceful shutdown timeout: 10s

### Running
- Prerequisites: Go 1.25+
- From the microservice root:
```bash
# Run in dev
go run ./cmd/server

# Build binary
go build -o ./bin/svc-downloader ./cmd/server
./bin/svc-downloader

# Change port
PORT=9090 go run ./cmd/server
```

### How it works (brief)
- Workers tick every 2 seconds, compute queue stats, and start up to `concurrency - running` new downloads per queue
- For each download:
  - Try `HEAD`; if disabled or fails, do a `GET` with `Range: 0-` to infer metadata
  - Plan segments by `max_connections` (default 4), or single stream if `Accept-Ranges` is not supported
  - Stream ranges to temp part files, update progress, and publish events
  - Merge parts to the final file; verify checksum if configured

### API
Base URL: `http://localhost:8089`

#### Downloads
- **Enqueue**: `POST /downloads`
```json
{
  "url": "https://example.com/file.zip",
  "file": { "path": "./downloads", "filename": "file.zip" },
  "config": {
    "max_connections": 4,
    "follow_redirects": true,
    "redirects_limit": 10,
    "rate_limit": 0,
    "retry": {"max_retries": 3, "retry_delay": "3s", "jitter": "2s"}
  },
  "queue_id": "main",
  "priority": 0,
  "tags": ["example"]
}
```
- **Get**: `GET /downloads/{id}`
- **List**: `GET /downloads?status=queued&queue=main&tags=linux,iso&q=ubuntu&limit=50&offset=0&order=created_at&desc=true`
- **Count**: `GET /downloads/count?status=running&queue=main`
- **Control**:
  - `POST /downloads/{id}/start`
  - `POST /downloads/{id}/pause`
  - `POST /downloads/{id}/resume`
  - `POST /downloads/{id}/cancel`
  - `DELETE /downloads/{id}` (deletes download and files)
- **Update**:
  - `PUT /downloads/{id}/config` (body: `DownloadConfig`)
  - `PUT /downloads/{id}/request` (body: `RequestOptions`)
  - `POST /downloads/{id}/priority` (body: `{ "priority": 10 }`)
  - `POST /downloads/{id}/tags` (body: `{ "tags": ["a","b"] }`)
  - `DELETE /downloads/{id}/tags` (body: `{ "tags": ["a","b"] }`)
  - `POST /downloads/{id}/queue` (body: `{ "queue_id": "fast" }`)

Example curl (enqueue + watch progress + get):
```bash
# Enqueue
curl -sS -X POST http://localhost:8089/downloads \
  -H 'Content-Type: application/json' \
  -d '{
    "url": "https://speed.hetzner.de/100MB.bin",
    "file": {"path": "./downloads", "filename": "100MB.bin", "unique_filename": true},
    "config": {"max_connections": 4, "follow_redirects": true}
  }'

# SSE events (live)
curl -N http://localhost:8089/events

# Get by id
curl -sS http://localhost:8089/downloads/<ID>
```

SSE event format:
```
event: started
data: {"id":"<ID>","status":"running"}
```
(Keep-alive `: comments` are sent every 30s.)

#### Queues
- **Create**: `POST /queues` (body: `Queue`)
- **Get**: `GET /queues/{id}`
- **List**: `GET /queues`
- **Update**: `PUT /queues/{id}` (body: `Queue`)
- **Delete**: `DELETE /queues/{id}`
- **Pause/Resume**:
  - `POST /queues/{id}/pause`
  - `POST /queues/{id}/resume`
- **Stats**: `GET /queues/{id}/stats`
- **Bulk**:
  - `POST /queues/{id}/bulk/delete`
  - `POST /queues/{id}/bulk/reassign` (body: `{ "to": "other-queue" }`)
  - `POST /queues/{id}/bulk/priority` (body: `{ "priority": 10 }`)
  - `POST /queues/{id}/bulk/status` (body: `{ "status": "paused" }`)

Example curl (create a queue):
```bash
curl -sS -X POST http://localhost:8089/queues \
  -H 'Content-Type: application/json' \
  -d '{
    "id": "fast",
    "name": "fast",
    "concurrency": 10,
    "rate_limit": 0,
    "default": false
  }'
```

### Notes & Behavior
- If the target file exists and `overwrite=false` but `unique_filename=true`, the server will append `(n)` to the filename.
- If the server does not support `Accept-Ranges` or `Content-Length` is unknown, downloading falls back to a single segment.
- `DisableHead` in `DownloadConfig` forces HEAD-fallback strategy.
- Rate limiting uses a simple token bucket; keys used include `global`, the queue id, and the download id.
- Duration fields in JSON are numbers in nanoseconds (Go time.Duration). Example: 3s => 3000000000.
- Worker tick interval is 2s; queue `concurrency` defaults to 1 if unset.

### Development
- Entrypoint: `cmd/server/main.go`
- Routing registration: `internal/api/register.go`
- API routes: `internal/api/routes`
- Business logic: `internal/service`
- Repository: `internal/repository`
- Transport: `internal/transport`

### Limitations / Future Work
- Persistence for events is in-memory only; SSE subscribers receive live events
- No authentication/authorization on the API
- No built-in OpenAPI file emission in this repo (Huma can generate if wired)
- Single local filestore backend
