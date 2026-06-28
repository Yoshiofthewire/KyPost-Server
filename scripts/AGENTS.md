# Scripts

## Purpose

Container initialization, process orchestration, Ollama model management, mail auth utilities, and Playwright E2E tests.

## Ownership

All files under `scripts/`. CI config lives in `.github/workflows/playwright.yml` (also owned here).

## Local Contracts

### Startup Sequence

`entrypoint.sh` → `bootstrap.sh` (one-shot, priority 1) → `supervisord` → `api`, `daemon`, `ollama`, `ollama-model` (parallel, priority 10)

- `entrypoint.sh`: creates required directories, chowns to `llamalab` user, launches `supervisord`
- `bootstrap.sh`: generates scrypt-hashed admin credentials and writes `admin.env`; **must not be re-run on an existing install** — it overwrites credentials
- `start-ollama.sh`: launches Ollama daemon on port 11434
- `pull-ollama-model.sh`: pulls the model named by `OLLAMA_MODEL` (docker-compose default: `nemotron-3-nano:4b`); requires Ollama daemon to be running first

### Supervisord Programs (from `supervisord.conf` at repo root)

| Program | Type | Priority |
|---------|------|----------|
| bootstrap | one-shot | 1 |
| api | daemon | 10 |
| daemon | daemon | 10 |
| ollama | daemon | 10 |
| ollama-model | one-shot | 10 |

### E2E Tests

- Framework: Playwright (`package.json`, `playwright.config.ts`)
- Tests: `tests/example.spec.ts`
- CI: `.github/workflows/playwright.yml` triggers on push/PR

## Work Guidance

- Do not change the startup priority of `bootstrap` relative to `api`/`daemon` — credentials must exist before services start
- `pull-ollama-model.sh` is idempotent; safe to re-run
- Add new E2E tests under `tests/`; keep `example.spec.ts` as a smoke-test baseline
- Install Playwright deps: `cd scripts && npm install`
- Run tests: `cd scripts && npx playwright test`

## Verification

- `npx playwright test` must pass in CI
- `bootstrap.sh` must produce a valid `admin.env` with a non-empty scrypt hash

## Child DOX Index

No child AGENTS.md files.
