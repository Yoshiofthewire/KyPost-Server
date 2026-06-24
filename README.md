<img src="./llamalabel.png" alt="Llama Labels" />

# llama Labels

llama Labels is a Dockerized IMAP keyword auto-labeler.

It polls unread inbox mail, classifies each message with an internal Ollama model (`nemotron-3-nano:4b` by default), and applies matching IMAP keywords.

## Overview

Runtime is a single container managed by `supervisord`, running:

- API server (`llama-lab --mode server`)
- Polling daemon (`llama-lab --mode daemon`)
- Internal Ollama service (`ollama serve`)
- One-shot model pull worker (`ollama pull qwen3:1.7b`)

Classification flow:

1. Fetch unread inbox messages from IMAP (`INBOX` by default).
2. Redact configured sensitive content.
3. Build a prompt from sender/subject/body plus tuning context.
4. Send prompt to Ollama (`/api/generate`).
5. Match model output against allowed labels.
6. Apply the label as an IMAP keyword.
7. Persist checkpoint and decisions to avoid unnecessary reprocessing.

## Requirements

- Docker
- Docker Compose

Optional (local development outside Docker):

- Go 1.26+
- Node.js 20+
- npm

## Quick Start

1. Clone the repository.

2. (Optional) copy env defaults:

```bash
cp .env.example .env
```

3. Build and run:

```bash
docker compose up --build -d
```

4. Open UI:

- http://localhost:5866

5. Login with bootstrap account:

- Username: `admin`
- Password: `ChangeMeNow123!`

Password change is required on first login.

6. In Config, save IMAP settings (host, port, username, password/app-password, mailbox) and run IMAP test.

7. In Tuning, sync/build labels and save `TUNING.md`.

## Environment Variables

Primary variables:

- `OLLAMA_BASE_URL` (default `http://127.0.0.1:11434`)
- `OLLAMA_MODEL` (default `qwen3:1.7b`)
- `OLLAMA_MODELS_HOST_DIR` (default `./share/ollama/models`, host path bind-mounted for persistent model cache)
- `IMAP_HOST` (optional default source; UI-saved config is preferred)
- `IMAP_PORT` (default `993`)
- `IMAP_USERNAME`
- `IMAP_PASSWORD`
- `IMAP_MAILBOX` (default `INBOX`)
- `IMAP_CONFIG_FILE` (default `/llama_lab/private/imap-config.json`)
- `IMAP_CONFIG_KEY_FILE` (default `/llama_lab/private/imap-config.key`)
- `TUNING_FILE` (default `/llama_lab/config/TUNING.md`)
- `TZ` (default `America/New_York`)

Notes:

- The image sets `OLLAMA_MODELS=/llama_lab/ollama-models`.
- `docker-compose.yml` bind-mounts `${OLLAMA_MODELS_HOST_DIR:-./share/ollama/models}` to that path so models persist across container rebuilds and recreates.
- Ollama API can be exposed by uncommenting `11434:11434` in `docker-compose.yml`.

Create the shared models directory once before first run:

```bash
mkdir -p share/ollama/models
```

## Data and Volumes

Named volumes map to:

- `/llama_lab/config`
- `/llama_lab/private`
- `/llama_lab/logs`
- `/llama_lab/state`

Host bind mount:

- `${OLLAMA_MODELS_HOST_DIR:-./share/ollama/models}` -> `/llama_lab/ollama-models`

Important files:

- `/llama_lab/config/config.yaml`
- `/llama_lab/config/admin.env`
- `/llama_lab/private/imap-config.json`
- `/llama_lab/private/imap-config.key`
- `/llama_lab/config/TUNING.md`
- `/llama_lab/state/state.json`
- `/llama_lab/state/decisions.json`
- `/llama_lab/ollama-models/*`

## UI Pages

- Login
- Config
- Tuning
- Labels
- Decisions
- Logs
- Health
- Status

## API Endpoints (high-use)

Auth/session:

- `POST /api/auth/login`
- `GET /api/auth/me`
- `POST /api/auth/logout`
- `POST /api/auth/password`

Runtime/status:

- `GET /api/status`
- `GET /api/health`
- `POST /api/health/repair`

Config/data:

- `GET|PUT /api/config`
- `GET /api/labels`
- `GET /api/decisions`
- `GET|PUT /api/tuning`

IMAP:

- `GET|POST|DELETE /api/imap/config`
- `POST /api/imap/test`

Llama/auth:

- `GET|POST /api/llama/auth`
- `POST /api/llama/test`

Logs:

- `GET /api/logs?file=<name>.log&lines=<n>`
- `GET /api/logs/list`

## Build and Dev Checks

Backend:

```bash
cd backend
go build -buildvcs=false ./...
go test ./...
```

Frontend:

```bash
cd frontend
npm install
npm run build
```

## Logging

Main logs:

- `app.log` - API/app-level events
- `daemon.log` - poller lifecycle and tick summaries
- `llama.log` - model/runtime activity and output lines
- `llama.err.log` - model/runtime error lines
- `llama-server.log` - classify/warmup trace lines

Log UI reads from `/api/logs` and `/api/logs/list`.

## Operational Notes

- Poller tick timeout is long enough for large inbox sweeps.
- Classify calls are serialized and paced to reduce model busy/flaky responses.
- AI-credits exhaustion is tracked as a sticky health/state flag and auto-clears on successful classify.
- IMAP configuration is encrypted at rest using files under `/llama_lab/private`.
- Use app-specific passwords when your mail provider requires them.
- Keywords are created by applying them; provider support for custom IMAP keywords can vary.

## Troubleshooting

### Ollama/model issues

- Check logs:

```bash
docker compose logs -f llama-lab
```

- Verify model pull happened (`qwen3:1.7b`).
- If needed, restart services:

```bash
docker compose restart
```

### IMAP connectivity issues

- Confirm IMAP settings are saved in Config.
- Verify host, port, username, password, and mailbox values.
- Use the IMAP test action in Config (`POST /api/imap/test`).
- Check `daemon.log` and `app.log` for auth, TLS, and keyword errors.

### No labels applied

- Confirm labels are present in allowlist/tuning.
- Confirm unread inbox has eligible messages.
- Check Decisions page and `daemon.log` tick summary counts.

## Project Structure

- `backend/` - Go API server, poller, adapters, state/health
- `frontend/` - React + Vite UI
- `scripts/` - runtime/bootstrap helpers
- `Dockerfile` - single image (backend, frontend, Ollama runtime)
- `docker-compose.yml` - local orchestration
- `supervisord.conf` - in-container process supervision
