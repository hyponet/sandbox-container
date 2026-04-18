# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Sandbox Container is a Go HTTP server that provides an isolated execution environment for AI agents. It exposes REST APIs for running bash commands, file operations, code execution (Python/JavaScript), and skills management. Sessions are isolated per agent+session ID under `/data/agents/<agent_id>/sessions/<session_id>/`.

## Build & Run

```bash
go build -ldflags="-s -w" -o sandbox-server .
go run .                              # listens on :9090
```

Docker:

```bash
docker build -t sandbox-container .
docker run -d -p 9090:9090 -v sandbox-data:/data/agents -v sandbox-skills:/data/skills -v sandbox-logs:/var/log/sandbox sandbox-container
```

## Testing

```bash
go test ./...                         # all tests
go test ./handler/...                 # single package
go test ./handler/ -run TestBashExec -v  # single test
```

Go 1.25, module `github.com/hyponet/sandbox-container`. Uses **gin-gonic/gin** (HTTP), **google/uuid** (session IDs), **goccy/go-yaml** (skills metadata), **bytedance/sonic** (JSON).

## Architecture

```
main.go → gin router with middleware chain
  ├── middleware/  - Audit logging (→ /var/log/sandbox/audit.log), API key auth (SANDBOX_API_KEY env)
  ├── session/     - Manager creates/cleans up session dirs; TTL cleanup goroutine (24h default, 10min interval)
  ├── handler/     - Gin handlers: bash, file, code, skill, sandbox (each receives *session.Manager)
  ├── model/       - Shared request/response structs
  └── client/      - Go SDK for consuming the API (httptest-based integration tests)
```

**Request flow:** Request → AuditLogger → AuthRequired (if route uses auth) → Handler → SessionManager resolves paths → Response

**Session isolation:** `session.Manager` resolves all file/command paths relative to `/data/agents/<agent_id>/sessions/<session_id>/`. Path traversal (`..`) is blocked. Skills are accessed via symlink `sessions/<sid>/skills → ../../skills`.

**Async bash:** Commands run in async mode write output to thread-safe buffers; output is read incrementally via offset.

## Environment Variables

- `SANDBOX_API_KEY` - Comma-separated API keys for Bearer token auth. If unset, auth is disabled.

## API Routes

All routes are under `/v1/`. Sandbox info endpoints (`/v1/sandbox`, `/v1/sandbox/packages/*`) require no auth. All other endpoints use auth middleware.

| Group | Key Endpoints |
|-------|--------------|
| Bash | `POST /v1/bash/exec`, `/output`, `/write`, `/kill`; session management under `/v1/bash/sessions/*` |
| File | `POST /v1/file/read`, `/write`, `/replace`, `/search`, `/find`, `/grep`, `/glob`, `/upload`, `/list`; `GET /v1/file/download` |
| Code | `POST /v1/code/execute`, `GET /v1/code/info` |
| Skills (global) | `POST /v1/skills/create`, `/import`, `/list`, `/delete`, `/tree`, `/file/read`, `/file/write`, `/file/update`, `/file/mkdir` |
| Skills (agent) | `POST /v1/skills/agents/:agent_id/list` (frontmatter), `/v1/skills/agents/:agent_id/load` (body) |
| Sessions | `GET /v1/sessions`, `GET /v1/sessions/:session_id/audits`, `DELETE /v1/sessions/:session_id` |

## CI

`.github/workflows/docker.yml` builds and pushes Docker image to GHCR on push to main or tags (linux/amd64).
