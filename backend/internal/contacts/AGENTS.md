# Contacts

## Purpose

Per-user address book storage: `Contact` records with a stable `UID`, a
monotonic per-user `Rev` used as both the CardDAV ETag/sync-token source and
the mobile-sync cursor, and tombstoned (not hard) deletes so incremental sync
consumers (CardDAV `sync-collection`, the mobile `/api/contacts/sync`
endpoint) can observe deletions.

## Ownership

All code under `backend/internal/contacts/`. Consumed by `api/` (web CRUD
handlers, the CardDAV backend, the mobile sync endpoints); never imported by
`processor/` or the daemon today.

## Local Contracts

- `Store` is instantiated per user directory (`contacts.New(userStateDir)`),
  mirroring `state.Store` — one file, `contacts.json`, sibling to `state.json`
  and `decisions.json` in `$STATE_DIR/users/<userID>/`.
- Every read and mutation re-reads `contacts.json` from disk first
  (`refreshFromDiskLocked`), then writes atomically via
  `fsutil.AtomicWriteFile` — required because the API and daemon processes
  share no memory (see root `backend/AGENTS.md`), even though only `api/`
  touches contacts today.
- `Contact.Rev` is bumped by `Store.Upsert`/`Store.Delete` on every mutation;
  `Contact.ETag()` derives `"rev-<Rev>"` from it — there is no separately
  stored ETag field.
- Deletes tombstone (`Contact.Deleted = true`, PII fields cleared) rather than
  removing the record, so `ChangedSince` can report deletions to sync
  clients. Tombstones are permanently purged by `Store.GC` after
  `defaultTombstoneRetention` (30 days); `ChangedSince` returns `tooOld=true`
  when a caller's cursor predates the GC watermark, signaling "your delta may
  be missing deletions — discard the cursor and re-fetch a full snapshot".
- Conflict/concurrency policy (e.g. CardDAV `If-Match`, mobile-sync
  last-write-wins) is decided by callers in `api/`, not by `Store` itself —
  `Store.Upsert`/`Store.Delete` always apply the write unconditionally. Read
  the current record first if a conflict check is needed.

## Work Guidance

- Keep this package free of HTTP/CardDAV/vCard concerns — those live in
  `api/contacts_handlers.go` and `api/dav_server.go`, which translate to/from
  `Contact`.
- Any new sync-relevant field must participate in `Contact.tombstone()`'s
  clear-list if it carries PII, so deletes don't leak stale data.

## Verification

- `go vet ./internal/contacts/...` must pass.
- Unit tests should cover: create/update/delete, tombstone field-clearing,
  `ChangedSince` cursor semantics (including `tooOld` after GC), and GC
  actually removing old tombstones while preserving live contacts.

## Child DOX Index

No child AGENTS.md files.
