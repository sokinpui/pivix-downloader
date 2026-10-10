# AGENT.md - Pixiv Downloader Backend

This file provides comprehensive guidelines and project structure context for AI agents working on the **Pixiv Downloader Backend** repository.

---

## 1. Project Overview

- **Description**: A high-availability, anti-blocking, resumable Pixiv automated batch download backend designed for decoupled frontend clients (Vue, React, Flutter, etc.).
- **Tech Stack**: 
  - Language: Go (Standard Library `net/http`)
  - Database: Pure Go SQLite (`modernc.org/sqlite`) — **Zero CGO dependencies**, enabling cross-compilation out of the box.
  - Real-time Communication: Server-Sent Events (SSE) for progress streaming.
- **Current State**: The backend implementation is fully structured and operational (see `internal/` directory).

---

## 2. Project Directory Structure (`internal/`)

```
pixiv-downloader/
├── go.mod
├── go.sum
├── main.go                       # Application entry point, dependency wiring, graceful shutdown
├── PLAN.md                       # Original design architecture document
├── README.md                     # User-facing manual and API summary
├── AGENT.md                      # AI agent guidelines (this file)
└── internal/
    ├── config/                   # Configuration management & database linkage
    ├── database/                 # SQLite initialization & automatic DDL migrations
    ├── model/                    # Data structures (Artwork, DownloadTask, Setting)
    ├── repository/               # Data access layer (CRUD ops)
    ├── pixiv/                    # Pixiv API client, link parsers, rate limiter, headers
    ├── engine/                   # Worker pool, task scheduler, atomic downloader
    ├── service/                  # Business logic (incremental bookmark sync, link parsing, submission)
    └── handler/                  # HTTP REST endpoints, JSON envelope responses, CORS, SSE Hub
```

---

## 3. Architecture & Core Principles

1. **Decoupled Backend**: No embedded frontend code. Communication occurs strictly via RESTful APIs and SSE.
2. **Zero CGO**: Always ensure `modernc.org/sqlite` is used for SQLite operations without enabling CGO, keeping builds simple (`CGO_ENABLED=0`).
3. **JSON Envelope Standard**: All HTTP responses follow this wrapper structure:
   ```json
   {
     "code": 0,
     "message": "success",
     "data": {}
   }
   ```
4. **Early Exit Bookmark Sync**: Incremental bookmark scans check local database states. Encountering $N$ consecutive already-completed artworks triggers an early stop to avoid triggering Pixiv rate limits / deep pagination blocks.
5. **Atomic File Writes**: Downloads are saved as `.tmp` files first, then renamed using `os.Rename` only upon successful completion to prevent corrupt or partial image fragments.
6. **Rate Limiting**: Pixiv requests include randomized sleep intervals (`300ms ~ 600ms`) to protect against ban waves.

---

## 4. Development & Build Instructions

- **Build binary**:
  ```bash
  go build -v .
  ```
- **Run server**:
  ```bash
  ./pixiv-downloader -port 8080 -session "YOUR_PHPSESSID" -user-id "YOUR_USER_ID"
  ```
- **Run tests (if applicable)**:
  ```bash
  go test ./...
  ```

---

## 5. Guidelines for AI Agents

- **Maintain Consistency**: Follow existing patterns in `internal/` for error handling, logging, and JSON response wrapping.
- **Database Changes**: If modifying schema, ensure DDL migrations inside `internal/database` handle existing tables gracefully (e.g., `CREATE TABLE IF NOT EXISTS`).
- **Concurrency Safety**: Use mutexes or channels where shared states (like the SSE Hub or Worker Pool) are accessed concurrently.
- **Documentation**: Keep `README.md` and `PLAN.md` updated if major architectural patterns or public API contracts change.
