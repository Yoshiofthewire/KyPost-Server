# Llama Mail Mobile Mail Relay — Backend Integration Guide

This document describes how the (separate-repo) Llama Labels mobile app
should read and send mail through a self-hosted Llama Mail server. It
mirrors `iOS_Mobile_notify.md` and `Mobile_Contact_Sync.md`'s shape:
concrete request/response JSON, an error table, and a deployment checklist
— written so a fresh Claude session working in the mobile app's repo can
implement against it with no other context.

## Summary

The mobile app **never connects to IMAP/SMTP directly and never holds mail
credentials**. It calls the same backend REST endpoints the web frontend
already uses for reading, organizing, and sending mail — the backend holds
the account's encrypted IMAP/SMTP credentials and proxies every operation.
This mirrors the precedent already set by the web client (which also has
zero direct mail-server connectivity) and by contact sync
(`Mobile_Contact_Sync.md`), which extends the same "backend as relay, client
never touches the origin protocol" philosophy to mobile specifically.

Authentication reuses the existing native-push pairing mechanism
(`subscriberId` / `subscriberHash`, `iOS_Mobile_notify.md` Part 3) — the
same credential contacts sync already reuses. There is no separate mobile
login and no app-specific mail password.

**Account setup is out of scope for mobile.** A user must configure their
IMAP/SMTP account once via the web UI (`/api/imap/config`) before mobile can
read or send anything. Mobile never views or sets raw host/username/password
— that endpoint stays cookie-only and mobile should never call it.

## Architecture Overview

```
Mobile App
  ├─ GET  /api/inbox?sub=&hash=&limit=&mailbox=          → unread mail, grouped by label
  ├─ GET  /api/inbox/folders?sub=&hash=&parent=           → list folders
  ├─ POST/PUT/DELETE /api/inbox/folders?sub=&hash=        → create/rename/delete folders
  ├─ POST /api/inbox/actions?sub=&hash=                   → read/archive/spam/delete/move
  ├─ POST /api/mail/draft?sub=&hash=                      → save a draft
  └─ POST /api/mail/send?sub=&hash=                       → send mail (SMTP) + save to Sent
        (all reuse the pairing subscriberId + subscriberHash from
         iOS_Mobile_notify.md Part 3 — no separate pairing step for mail)
```

All six endpoints are backed by the same per-user encrypted IMAP/SMTP
credential file and the same `imapadapter.Client` the web frontend already
uses (`backend/internal/adapters/imap/`) — there is exactly one mail account
per user, no separate "mobile" vs "web" state.

---

## Part 1: Prerequisite — Device Pairing (unchanged, reused as-is)

Exactly as in `Mobile_Contact_Sync.md` Part 1: if the app already implements
push pairing, reuse the stored `sub`/`hash` — there is nothing new to build
for pairing. The same pair authenticates native push pull, contact sync, and
now mail.

Each request below accepts **either**:
- A web session cookie (`llama_session`) — not applicable to mobile, listed
  for completeness since the same endpoints serve the web frontend.
- `sub=<subscriberId>&hash=<subscriberHash>` as query params — the mobile
  path, validated against an HMAC the server holds (`PAIRING_SECRET`), same
  as contact sync and native pull.

---

## Part 2: Endpoint Contracts

### GET /api/inbox — unread mail

```
GET /api/inbox?sub=<id>&hash=<hash>&limit=100&mailbox=INBOX
GET /api/inbox?sub=<id>&hash=<hash>&limit=100&mailbox=INBOX&since=<cursor>
```

- `limit` — optional, default `500`, max `5000`. **Mobile should pass a
  small value** (e.g. `50`–`100`).
- `mailbox` — optional; omit for the default inbox.
- `since` — optional, added in v2 (see Part 5). Omit for a full snapshot
  (the shape below); pass the `cursor` a previous call returned to get a
  delta instead. `since=0` also gets a delta response (just with everything
  reported `"new"`) — useful for forcing a resync without losing the
  `delta` response shape. This mirrors `Mobile_Contact_Sync.md`'s
  `since`/`cursor` pattern, applied to mail.

#### Full snapshot (no `since`)

Response `200`:

```json
{
  "tabs": ["Work", "Personal", "Uncategorized"],
  "byTab": {
    "Work": [
      {
        "messageId": "<abc123@example.com>",
        "sender": "alice@example.com",
        "sentTo": "me@example.com",
        "cc": "",
        "bcc": "",
        "subject": "Project update",
        "body": "...",
        "label": "Work",
        "status": "unread",
        "atUtc": "2026-07-07T20:43:25Z"
      }
    ],
    "Personal": [],
    "Uncategorized": []
  }
}
```

Every entry has a full `body`, exactly as before — nothing about this shape
changed. What changed is where the data comes from: the server now serves
this from an internal cache warmed by its own background mail poller
whenever it can (server-side detail, invisible to mobile — see Part 5), and
only falls back to a live fetch when the cache can't yet cover the request.
Either way the response shape and latency contract from mobile's point of
view are unchanged.

If the account isn't configured yet (see Part 4), this returns `200` with
an empty tab scaffold rather than an error — the same graceful-degradation
behavior the web frontend relies on.

#### Delta (`since=<cursor>`)

Response `200`:

```json
{
  "tabs": ["Work", "Personal", "Uncategorized"],
  "byTab": {
    "Work": [
      {
        "messageId": "1044",
        "sender": "alice@example.com",
        "subject": "Project update",
        "body": "...",
        "label": "Work",
        "status": "unread",
        "atUtc": "2026-07-07T20:43:25Z",
        "changeType": "new"
      },
      {
        "messageId": "1039",
        "sender": "bob@example.com",
        "subject": "Re: Invoice",
        "label": "Work",
        "status": "read",
        "atUtc": "2026-07-06T18:02:11Z",
        "changeType": "updated"
      }
    ],
    "Personal": [],
    "Uncategorized": []
  },
  "delta": true,
  "cursor": 42,
  "removed": ["1021", "1017"]
}
```

- `delta` — always `true` on this shape; absent entirely on a full-snapshot
  response. Use its presence, not the request params you sent, to decide how
  to parse the response.
- `cursor` — the new high-water mark. Persist it and send it back as `since`
  on the next poll, exactly like contact sync's `cursor`.
- Each entry's `changeType` is either:
  - `"new"` — a message mobile hasn't seen before. `body` is populated;
    insert it.
  - `"updated"` — a message mobile already has (same `messageId` as an
    earlier `"new"`). **`body` is intentionally omitted** — only
    `status`/`label`/other metadata changed (e.g. marked read elsewhere).
    Keep the `body` you already cached locally and merge in the other
    fields.
- `removed` — `messageId`s that dropped out of the current top-`limit`
  window since your last poll (read further back than `limit`, moved,
  deleted — the reason is deliberately not distinguished). Remove them from
  your local list. This is **not** a tombstone feed: if you miss a poll
  entirely, a message could disappear from `removed` coverage. See Part 5's
  self-heal guidance.

### GET/POST/PUT/DELETE /api/inbox/folders — folder management

```
GET    /api/inbox/folders?sub=&hash=&parent=<optional>
POST   /api/inbox/folders?sub=&hash=        { "parent": "", "name": "Travel" }
PUT    /api/inbox/folders?sub=&hash=        { "folder": "Travel", "name": "Trips" }
DELETE /api/inbox/folders?sub=&hash=&folder=Travel
```

`GET` response `200`:

```json
{ "parent": "", "folders": [{ "path": "Work", "deletable": true }] }
```

`POST`/`PUT`/`DELETE` all respond `200` with `{"ok": true, ...}` echoing the
affected folder. Built-in mailboxes (Inbox, Sent, Drafts, Trash, etc.) can't
be renamed or deleted — expect `400` if attempted.

### POST /api/inbox/actions — bulk read/archive/spam/delete/move

```json
{
  "action": "archive",
  "messageIds": ["<abc123@example.com>", "<def456@example.com>"],
  "mailbox": "INBOX",
  "targetMailbox": "Archive"
}
```

- `action` — one of `delete`, `archive`, `spam`, `read`, `move`.
- `targetMailbox` is required only for `action: "move"`.

Response `200`:

```json
{
  "ok": true,
  "action": "archive",
  "processed": 2,
  "failed": [],
  "targetMailbox": ""
}
```

`failed` lists any `messageId`s that errored individually (`{"messageId": "...", "error": "..."}`);
`ok` is `false` if `failed` is non-empty, but successfully processed IDs
still take effect — treat this as a partial-success response, not all-or-nothing.

### POST /api/mail/draft — save a draft

```json
{
  "to": "bob@example.com, carol@example.com",
  "cc": "",
  "bcc": "",
  "subject": "Draft subject",
  "body": "...",
  "mode": "plain"
}
```

- `to`/`cc`/`bcc` are **comma-separated strings**, not arrays.
- `mode` — `"plain"` (default), `"html"`, or `"markup"` (sent as
  `text/markdown`).
- `to` must contain at least one valid recipient or this returns `400`.

Response `200`: `{"ok": true}`.

### POST /api/mail/send — send mail

Same request body shape as draft. Sends via the account's configured SMTP
server, then best-effort saves a copy to Sent.

Response `200`:

```json
{ "ok": true, "sentSaved": true, "warning": "" }
```

If the send succeeds but saving to Sent fails, `sentSaved` is `false` and
`warning` explains why — the send itself still happened; don't treat this as
a failure to the user, just surface the warning.

---

## Part 3: Error Handling

| Status | Cause | Notes |
|--------|-------|-------|
| `400` | Malformed JSON body, missing/invalid `to` recipient, missing `action`/`messageIds`, or an unsupported `action` value | Body validation failures — fix the request |
| `400` | Account not configured yet — exact body text differs per endpoint: `"imap configuration is required"` (folders, actions), `"imap configuration is required before saving drafts"` (draft), `"imap configuration is required before sending"` (send) | Direct the user to the web UI (see Part 4). Match on the `imap configuration is required` prefix rather than the full string. `GET /api/inbox` degrades to a `200` empty scaffold instead of erroring. |
| `401` | `sub`/`hash` missing, or `hash` doesn't match the expected HMAC for `sub` | Re-pair the device (Part 1) |
| `401` (unknown subscriber) | `sub` doesn't map to any known user | Device paired against a server that lost that state (e.g. restored from an old backup); re-pair |
| `503` | Server has no `PAIRING_SECRET` configured | Mail relay (and native push, contact sync) are all unavailable until the self-hoster sets that env var. Only returned when `sub`/`hash` were actually supplied — a plain unauthenticated request without them gets a normal `401`. |
| `502` | Upstream IMAP/SMTP failure (server unreachable, auth rejected by the mail provider, etc.) | Transient — safe to retry with backoff |
| `503` (folders/actions/draft only) | IMAP client not configured/available for another reason | Distinct from the `400` "not configured yet" case — this means configuration exists but the client couldn't be built |

---

## Part 4: Account Setup Is Web-Only

`/api/imap/config` and `/api/imap/test` are **cookie-only** — they are not
reachable with `sub`/`hash` and mobile should never call them. If any mail
endpoint above returns `imap configuration is required`, the correct mobile
UX is a "set up your mail account on the web app first" empty state, not a
form to enter host/username/password in the mobile app itself. This is
deliberate: mail credentials are decrypted only inside the backend process
and are never returned to any client, including mobile.

---

## Part 5: Delta/Cursor Sync (v2)

`GET /api/inbox` now supports the same `since`/`cursor` delta pattern as
contact sync (`Mobile_Contact_Sync.md`) — see Part 2's "Delta" response
shape. The **no-`since` full snapshot is still fully supported and remains
the default** — passing `since` is opt-in, not required.

**Recommended mobile behavior:**

- Persist the `cursor` from every response (delta or, going forward, treat
  a fresh install as `since=0`) and send it back as `since` on the next
  poll.
- Pass a small `limit` (50–100) rather than the default 500, same as before.
- Fetch on app foreground and pull-to-refresh, plus a `since`-based poll
  loop if you want incremental updates without a full re-render each time.
- Handle `changeType: "updated"` entries by merging fields into your local
  copy rather than replacing it — `body` is deliberately omitted on those
  (see Part 2) since you already have it.
- Handle `removed` by dropping those `messageId`s locally.

**Self-heal instead of a `tooOld` flag:** unlike contact sync, there is no
`tooOld` signal here — the server doesn't retain enough history to detect
"your cursor is definitely stale," only "here's what changed since your
cursor, plus what fell out of the current window since last time I computed
that." In the rare case of two devices (or web + mobile) polling the same
account concurrently, one device's cursor can miss a `removed` notification
that happened between its polls. The fix is cheap and client-side:
**periodically send `since=0`** (e.g. once a day, or on a manual
pull-to-refresh) to force a full resync and self-correct any drift — the
response still comes back as a `delta:true` shape (everything reported
`"new"`, `removed` empty), so your parsing code doesn't need a special case
for it.

**Server-side note, not a mobile concern:** the backend also warms an
internal cache from its own background mail poller (which already runs
every ~90s for notifications/labeling), so the no-`since` snapshot path is
often served without a live IMAP round trip at all. This only affects
server-side latency/load, not the wire contract above — mentioned here only
so you understand why the first load after account setup might be a bit
slower than subsequent ones (before the cache is warm) but is otherwise
identical from the client's point of view.

---

## Part 6: Deployment Checklist (mobile app repo)

- [ ] Reuse the existing pairing `sub`/`hash` storage (Part 1); no new
      pairing flow needed for mail.
- [ ] Never implement an IMAP/SMTP client or store mail credentials
      on-device — every mail operation goes through the six endpoints above.
- [ ] Handle the "not configured yet" state (`400` with
      `imap configuration is required`) by directing the user to set up
      their account on the web app — do not attempt to collect host/
      username/password in the mobile app.
- [ ] Poll `GET /api/inbox` with a bounded `limit` (50–100), on foreground
      and pull-to-refresh at minimum.
- [ ] Persist the `cursor` from each response and send it back as `since` on
      the next poll to get delta responses (Part 5); periodically send
      `since=0` to self-heal (Part 5's `removed` staleness note).
- [ ] Parse responses by checking for `delta: true`, not by remembering
      whether you sent `since` — merge `changeType: "updated"` entries
      (keep the locally cached `body`) and drop `removed` message IDs.
- [ ] Treat `POST /api/inbox/actions`'s response as partial-success: check
      `failed[]` even when `ok` is `false`, since `processed` IDs still took
      effect.
- [ ] Remember `to`/`cc`/`bcc` are comma-separated strings in requests, not
      JSON arrays (differs from `/api/inbox`'s response shape, which uses
      flat strings too, but from contacts sync's array-of-objects shape).
- [ ] Surface `mail/send`'s `warning` field to the user as a non-blocking
      notice when `sentSaved` is `false` — the send already succeeded.
- [ ] Test against a freshly-paired device with no IMAP account configured
      yet: `GET /api/inbox` should return `200` with an empty tab scaffold;
      the other five endpoints should return `400`.

---

## Operational Notes

- **No mail credentials ever reach mobile.** `/api/imap/config`'s `GET`
  response (web-only) omits the password field even for the web client.
- **Auth reuses pairing, not a new secret.** Losing pairing state means
  re-pairing (Part 1) — there is no separate "mail token" to manage.
- **Delta sync (v2, Part 5)** mirrors `Mobile_Contact_Sync.md`'s
  `since`/`cursor` pattern — read that doc's Part 2 for the sibling
  implementation if you're building both sync loops in the mobile app at
  once, since the client-side patterns (persist cursor, merge deltas,
  periodic full resync) are intentionally the same shape for mail and
  contacts.

## Summary

Mail relay adds no new backend capability — it reuses the exact endpoints
the web frontend already calls (`/api/inbox`, `/api/inbox/folders`,
`/api/inbox/actions`, `/api/mail/draft`, `/api/mail/send`) and layers in
mobile's existing pairing credential (`subscriberId`/`subscriberHash`) as an
alternate to the session cookie. Account setup (`/api/imap/config`,
`/api/imap/test`) stays cookie-only and web-only by design, so mail
credentials never leave the backend process. `GET /api/inbox` additionally
supports `Mobile_Contact_Sync.md`-style `since`/`cursor` delta sync (Part 5)
as of v2.
