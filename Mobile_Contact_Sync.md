# Llama Mail Mobile Contact Sync — Backend Integration Guide

This document describes how the (separate-repo) Llama Labels mobile app should
sync contacts with a self-hosted Llama Mail server. It mirrors
`iOS_Mobile_notify.md`'s shape: concrete request/response JSON, an error
table, and a deployment checklist — written so a fresh Claude session working
in the mobile app's repo can implement against it with no other context.

## Summary

The server now manages a per-user address book ("Contacts") that can reach a
phone or computer two ways:

1. **Recommended: a lightweight two-way JSON sync endpoint**
   (`/api/contacts/sync`), authenticated with the same `subscriberId` /
   `subscriberHash` pairing mechanism already used for native push
   (`iOS_Mobile_notify.md` Part 3). The mobile app pulls server-side changes
   and pushes its own local creates/edits/deletes in the same round trip.
2. **Alternative: a real CardDAV account** (`/dav/{username}/contacts/`),
   for users who'd rather point their OS's native Contacts app (or any
   CardDAV client — Nextcloud, Thunderbird, etc.) directly at the server,
   bypassing the custom app entirely.

Both paths read and write the same underlying address book — a contact
created via CardDAV shows up in the next JSON sync pull, and vice versa.

## Architecture Overview

```
Mobile App
  ├─ (recommended) GET/POST /api/contacts/sync?sub=&hash=  → JSON delta, two-way
  │     (reuses the existing native-push pairing: subscriberId + subscriberHash)
  │
  └─ (alternative) CardDAV account → https://<server>/dav/{username}/contacts/
        (HTTP Basic Auth with an app-specific CardDAV password, RFC 6352)
```

Both surfaces are backed by the same per-user `contacts.Store`
(`backend/internal/contacts/`) on the server — there is exactly one address
book per user, no separate "mobile" vs "web" data.

---

## Part 1: Prerequisite — Device Pairing (unchanged, reused as-is)

The sync endpoint's `sub`/`hash` are exactly the `subscriberId` and
`subscriberHash` already minted by the native push pairing flow described in
`iOS_Mobile_notify.md`:

1. The signed-in user's browser calls `GET /api/notifications/pairing` and
   shows a QR code containing (among other things) `sub` and `hash`.
2. The mobile app scans it and calls `POST /api/notifications/native/register`
   to complete pairing (this also registers a push token if the app supports
   push; contact sync does not require push to be configured).
3. From then on, the app has a stable `subscriberId` + `subscriberHash` pair
   it can use for **both** the native-pull notification endpoint and the
   contact-sync endpoint below — no separate pairing step for contacts.

If the app already implements push pairing, there is nothing new to build
here; reuse the stored `sub`/`hash`.

---

## Part 2: Two-Way JSON Sync Contract (recommended path)

```
GET  /api/contacts/sync?sub=<subscriberId>&hash=<subscriberHash>&since=<cursor>
POST /api/contacts/sync?sub=<subscriberId>&hash=<subscriberHash>
```

Both are **unauthenticated by web session** — like
`GET /api/notifications/native/pull`, the caller proves ownership via
`sub`/`hash` in the query string, validated against an HMAC the server holds
(`PAIRING_SECRET`). There is no bearer token, no cookie.

### GET — pull the delta since a cursor

Omit `since` (or pass `since=0`) on the very first sync to get a full
snapshot. On every later call, pass the `cursor` value returned by the
previous call.

Request:

```
GET /api/contacts/sync?sub=d2d08b7f-...&hash=9f7642d9...&since=5
```

Response `200`:

```json
{
  "cursor": 7,
  "tooOld": false,
  "changed": [
    {
      "uid": "68116d4f-0033-4dd1-9ac6-843f2e02bee7",
      "rev": 6,
      "createdAt": "2026-07-07T20:43:25Z",
      "updatedAt": "2026-07-07T20:45:20Z",
      "fn": "Grace Hopper",
      "givenName": "Grace",
      "familyName": "Hopper",
      "org": "US Navy",
      "emails": [{ "label": "home", "value": "grace@example.com" }],
      "phones": [],
      "notes": ""
    }
  ],
  "deleted": [
    { "uid": "2d92d18c-a76c-4dfd-998d-58bed47b1d89", "rev": 3, "deleted": true }
  ]
}
```

- `cursor` — the highest revision now known to the server. Persist this
  per-device and send it back as `since` next time. Cursors are **per
  device**, not per account: two phones on the same account each track their
  own `since` independently and will both see the same underlying changes,
  just possibly at different points in their own polling cadence.
- `changed` — full contact objects that are new or updated since `since`.
- `deleted` — tombstone stubs (`uid` + `rev` only — no PII) for anything
  removed since `since`. Apply these as local deletes.
- `tooOld` — see "Handling `tooOld`" below.

### POST — push local changes, get the merged delta back

```json
{
  "baseCursor": 5,
  "changes": [
    { "uid": "", "rev": 0, "fn": "New Contact", "emails": [{ "value": "new@example.com" }] },
    { "uid": "abc123", "rev": 39, "fn": "Edited Name" },
    { "uid": "def456", "rev": 38, "deleted": true }
  ]
}
```

- Each entry in `changes` is either a **create** (`uid` empty), an **update**
  (existing `uid`, contact fields set), or a **delete** (existing `uid`,
  `"deleted": true`, no other fields needed).
- `rev` should be the revision the device last saw for that contact (`0` for
  a locally-created, never-synced contact). The server does **not** reject
  changes on a `rev` mismatch — see the conflict policy below.
- The response has the **exact same shape as GET**, computed relative to
  `baseCursor`: it reflects the post-merge state, including server-assigned
  `uid`/`rev` for anything the device just created. Use it to reconcile local
  IDs (a device-generated temp ID must be replaced with the server's `uid`
  from the matching `changed[]` entry — match by content/order, since the
  server does not echo back a client-supplied correlation ID in v1).

### Conflict policy: last-write-wins (v1)

If two devices (or a device and the web UI) edit the same contact before
either has seen the other's change, the server does **not** reject the
later write — whichever change reaches the server last simply wins, bumping
the revision again. There is no merge, no `409 Conflict`. This is a
deliberate v1 simplification: contacts are low-contention (one person's
address book, rarely edited on two devices in the same instant), so the
added complexity of real conflict resolution isn't worth it yet. If your app
needs to warn users about this, the only signal available is "the contact I
just tried to sync came back with a different `rev`/content than I sent."

### The `Contact` JSON shape

| Field | Type | Notes |
|-------|------|-------|
| `uid` | string | Stable identity. Empty in a create request; always populated in responses. |
| `rev` | number | Monotonic per-user revision. Also the CardDAV ETag source (`"rev-<rev>"`). |
| `deleted` | bool | Only ever `true` in tombstone stubs (`deleted[]`); omitted otherwise. |
| `createdAt` / `updatedAt` | string (RFC 3339 UTC) | Server-assigned. |
| `fn` | string | **Required.** Full/formatted display name. |
| `givenName`, `familyName`, `middleName`, `prefix`, `suffix` | string | All optional. |
| `nickname`, `org`, `title`, `notes`, `birthday` | string | Optional. `birthday` is `YYYY-MM-DD`. |
| `emails`, `phones` | array of `{label?, value}` | `label` is a free-form string (`"home"`, `"work"`, `"mobile"`, ...), optional. |
| `addresses` | array of `{label?, street?, city?, region?, postalCode?, country?}` | All optional. |

Only `fn` is required when creating/updating a contact. Everything else may
be omitted.

**Contact photos are not supported in v1.** There is no `photo` field, and
none is round-tripped via CardDAV either. Do not assume photo sync works.

### Handling `tooOld: true`

The server only keeps deleted-contact tombstones for a limited retention
window (30 days) before permanently purging them. If a device's `since`
cursor is older than that window, the server cannot guarantee `deleted[]` is
complete — it returns `"tooOld": true` and **omits** `changed`/`deleted`
entirely rather than risk silently under-reporting deletions.

When you see `tooOld: true`: discard the locally stored cursor, wipe (or
reconcile-by-full-replace) the local contact cache, and re-request with
`since=0` to get a fresh full snapshot. This should be rare in practice
(it only bites a device that hasn't synced in 30+ days).

### Recommended client behavior

- Persist `cursor` locally (e.g. alongside the paired device's stored
  `sub`/`hash`).
- Sync on: app foreground, after any local edit (push immediately rather
  than waiting for the next poll), and periodically in the background where
  the OS allows.
- Treat `deleted[]` as authoritative — always remove locally, even if the
  device has a pending local edit to that same contact (the delete wins).
- If the app is also paired for native push, an optional future enhancement
  is a `"type": "contacts-changed"` hint in a push payload's `data` map to
  trigger an immediate sync rather than waiting for the next poll — **not
  implemented today**; polling alone is fully sufficient for v1.

---

## Part 3: Alternative — Native CardDAV Account

Instead of (or in addition to) the JSON sync above, a user can add the
server as a standard CardDAV address book in their OS:

- **URL:** `https://<server>/dav/{username}/contacts/`
  (e.g. `https://mail.example.com/dav/alice/contacts/`)
- **Auth:** HTTP Basic Auth using an **app-specific CardDAV password** —
  generated from the web UI's Contacts page ("Generate Password" under
  "CardDAV Access"), shown exactly once at creation time. **Never use the
  account's normal login password here** — the server does not accept it for
  CardDAV.
- Discovery via `/.well-known/carddav` is supported, so most clients (macOS
  Contacts, iOS Contacts via a third-party CardDAV-capable app, Nextcloud,
  Thunderbird/CardBook) only need the base server URL and the username —
  they'll discover the principal, home-set, and address book paths
  automatically.
- This hands the OS's native contacts app full read/write access to the
  address book via vCard — a different trust/UX model than the in-app
  pull/push sync (the mobile app itself doesn't need to implement anything
  for this path; it's entirely OS/CardDAV-client-driven).
- Known limitation: server-side `addressbook-query` filtering
  (`CARDDAV:filter`) is not implemented — the server returns the full address
  book for any query and lets the client filter locally. This is safe (no
  missing results) but not bandwidth-optimal for very large address books.

If you're building an in-app contacts UI (recommended), use Part 2's JSON
endpoints instead — they're purpose-built for that and avoid needing a vCard
parser in the app at all. Part 3 exists for users who prefer their OS's
native contacts app over anything in-app.

---

## Part 4: Error Handling

| Status | Cause | Notes |
|--------|-------|-------|
| `400` | Malformed `since`, or a malformed request body | `since` values that fail to parse are treated as `0` server-side for GET, but a malformed POST body is rejected outright |
| `401` | `sub`/`hash` missing, or `hash` doesn't match the expected HMAC for `sub` | Re-pair the device (Part 1) — a stale or tampered pairing was rejected |
| `401` (unknown subscriber) | `sub` doesn't map to any known user | The device paired against a server that has since lost that state (e.g. restored from an old backup); re-pair |
| `503` | Server has no `PAIRING_SECRET` configured | Contact sync (and native push) are both unavailable until the self-hoster sets that env var |

There is deliberately no `409 Conflict` — see the last-write-wins policy in
Part 2.

---

## Part 5: Deployment Checklist (mobile app repo)

- [ ] Reuse the existing pairing `sub`/`hash` storage (Part 1); no new pairing
      flow needed for contacts.
- [ ] Implement local persistence for the sync `cursor` (per device).
- [ ] Implement a local change queue: buffer creates/edits/deletes made
      offline, flush them via `POST /api/contacts/sync` on reconnect.
- [ ] Implement delta merge: apply `changed[]` as upsert-by-`uid`, `deleted[]`
      as remove-by-`uid`.
- [ ] Handle `tooOld: true` by discarding the cursor and re-fetching a full
      snapshot (`since=0`).
- [ ] Reconcile server-assigned `uid` for locally-created contacts after a
      successful POST (match by position/content in the response's
      `changed[]`, since there is no client-supplied correlation ID in v1).
- [ ] Test against a brand-new account (empty address book, first sync should
      return an empty `changed`/`deleted` with `cursor: 0`).
- [ ] Test with 1000+ contacts to confirm the full-snapshot path (`since=0`)
      performs acceptably — the server returns the entire non-deleted list in
      one response with no pagination in v1.
- [ ] Do **not** implement contact-photo sync — the server has no `photo`
      field in v1; confirm the UI degrades gracefully (initials avatar, etc.)
      rather than assuming a photo URL exists.
- [ ] If offering the CardDAV alternative (Part 3) from within the app (e.g.
      a "copy this URL/password into your Contacts app" flow), surface the
      app-specific password generation UI from the web app rather than
      re-implementing password generation in the mobile app — the mobile app
      only needs to display instructions, not call `/api/contacts/dav-password`
      itself.

---

## Operational Notes

- **Cursor scope:** per device, not per account. Two devices on the same
  account each maintain their own `since`/`cursor` independently.
- **Tombstone retention:** 30 days. A device that hasn't synced in longer
  than that will get `tooOld: true` on its next attempt and must do a full
  resync.
- **No push-triggered sync in v1:** the app must poll (on foreground + a
  reasonable background interval). A future enhancement could piggyback a
  "contacts changed" hint on the existing native push payload, but this is
  not implemented today.
- **Photos are out of scope for v1** on both the JSON and CardDAV paths.

## Summary

Contact sync reuses the same pairing infrastructure already built for native
push (`iOS_Mobile_notify.md`), adding one new two-way endpoint
(`/api/contacts/sync`) that mirrors the existing pull-notification shape
(bounded delta, monotonic cursor, plain HTTP, no relay). A parallel, fully
standard CardDAV surface (`/dav/{username}/contacts/`) is available for users
who'd rather sync through their OS's native contacts app instead of (or
alongside) the custom mobile app. Both read and write the same address book,
so a contact created on one path is immediately visible on the other.

`Mobile_Mail_Relay.md`'s `GET /api/inbox` adopted this same `since`/`cursor`
delta pattern (its Part 5) — read that doc if you're wiring up the mail sync
loop alongside this one; the client-side shape (persist cursor, merge
deltas, periodic full resync) is intentionally identical, though mail's
`removed` list is a window-fallout signal rather than a permanent tombstone
feed like this doc's `deleted`/`tooOld`.
