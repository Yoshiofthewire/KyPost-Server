# Server-side contact deduplication & merge

**Date:** 2026-07-12
**Status:** Approved, pending implementation

## Problem

Each user has one local `contacts.Store` (`contacts.json`) that receives records
from three sources, each assigning its own `UID`:

1. Web CRUD (`handleContacts`).
2. Mobile sync — offline-created contacts with client-assigned UIDs
   (`handleContactsSync`).
3. The outbound CardDAV **client** pull, which imports an external address book
   (`handleContactsCardDAVClientSync`).

The same person therefore arrives under different UIDs, producing duplicates.
We need server-side logic to find duplicates and merge them, cleanly through the
sync model (monotonic `Rev`, tombstones) so every CardDAV/mobile client
converges on the merged result.

A secondary goal: introduce the first **server-side-only contact fields** so
iOS/Android can pick up extra data later. Merge provenance is that first field.

## Decisions (locked during brainstorming)

- **Trigger:** on-demand `POST /api/contacts/dedupe`, returns a report. Auto-triggering can be added later.
- **Match key:** two contacts are candidates if they share any normalized email OR any normalized phone. Name alone matches if the contact is otherwise empty.
- **Merge policy:** union multi-value fields; scalar fields take the most-recently-updated non-empty value; survivor is the oldest contact.
- **Provenance:** survivor records absorbed UIDs; each loser tombstone points at its survivor.
- **Transitivity + name guard:** merge connected components; a component of 3+ (chained/bridged) merges only if all members share one normalized formatted name, else the whole component is left untouched.

## Architecture

Per `contacts/AGENTS.md`, the package stays free of HTTP/CardDAV/vCard concerns.
Matching/merge is pure data logic on `Contact`, so it lives in the package.

- **`backend/internal/contacts/dedupe.go`** (new)
  - `normalizeEmail(string) string` — trim + lowercase.
  - `normalizePhone(string) string` — strip to digits; return last 10 when the
    result has ≥10 digits, else the full digit string. Empty when no digits.
    `ponytail:` naive heuristic (no libphonenumber); ceiling = phone numbers that
    differ only outside the last 10 digits collide, and extensions are ignored.
  - `findDuplicateGroups([]Contact) [][]int` — connected components over
    live contacts; edge = shared normalized email or phone.
  - `mergeGroup([]Contact) (survivor Contact, absorbedUIDs []string)` — applies
    the merge policy below to one group.
  - `groupShouldMerge(members []Contact) bool` — size 2 → true; size ≥3 → true
    only if all members share one non-empty normalized formatted name.
- **`backend/internal/contacts/store.go`** — `Store.Dedupe() (DedupeReport, error)`:
  under the lock, refresh from disk, run grouping on **live** (non-deleted)
  contacts, apply merges (bump `seq` once per survivor update and once per loser
  tombstone), persist once, return the report.
- **`backend/internal/api/contacts_handlers.go`** — thin `handleContactsDedupe`:
  authenticate, resolve the user's store, call `store.Dedupe()`, `writeJSON` the
  report. Register `POST /api/contacts/dedupe` beside the other contacts routes.

## Data model changes (`contacts.go`)

Two new fields on `Contact` — the first server-side-only fields:

```go
MergedUIDs []string `json:"mergedUIDs,omitempty"` // survivor: UIDs it absorbed
MergedInto string   `json:"mergedInto,omitempty"` // loser tombstone: survivor UID
```

- Plain JSON on `Contact`, so they ride existing CardDAV/mobile sync untouched;
  clients ignore unknown keys today and can surface them later.
- `tombstone()` adds `MergedUIDs` to its clear-list (metadata, not needed on a
  deleted record). `MergedInto` is NOT cleared by `tombstone()`; the merge sets
  it explicitly after calling `tombstone()`.

## Matching detail

- Blank normalized values never index and never match (do not merge everyone
  with an empty email).
- Only **live** (`!Deleted`) contacts participate; tombstones are excluded.
- Build a value→contacts index for normalized emails and phones; union contacts
  that share any value (union-find), yielding connected components.
- Name edge: two contacts also join if they share a non-empty normalized
  formatted name AND at least one of them is otherwise empty (no emails and no
  phones), so name-only imports fold into their fuller counterpart.

## Merge policy

For each group selected by `groupShouldMerge`:

- **Survivor = oldest:** earliest `CreatedAt`; tie-break lowest `Rev`, then UID
  string. Keeps its own `UID` and `CreatedAt`.
- **Multi-value** (`Emails`, `Phones`, `Addresses`): unioned across members,
  de-duped — emails/phones by normalized value (keep first-seen original
  `Value`/`Label`), addresses by whole-struct equality.
- **Scalars** (`FormattedName`, `GivenName`, `FamilyName`, `MiddleName`,
  `Prefix`, `Suffix`, `Nickname`, `Org`, `Title`, `Notes`, `Birthday`): walk
  members newest→oldest by `UpdatedAt`; first non-empty value wins per field.
- **Provenance:** survivor `MergedUIDs = union(existing, absorbed UIDs)`, sorted.
- **Rev bookkeeping:** survivor gets a fresh `Rev` (new `seq`); each loser is
  tombstoned with a fresh `Rev` and `MergedInto = survivor.UID`. All sync
  clients then drop losers and keep the updated survivor.
- **Idempotent:** a second run finds no live duplicates (survivors already
  unioned, losers are tombstones excluded from input) → no-op.

## Response shape

```json
{
  "mergedCount": 3,
  "groups": [
    { "survivor": "uid-A", "absorbed": ["uid-B", "uid-C"] }
  ]
}
```

`mergedCount` = total losers tombstoned. Empty `groups` when nothing merged.

## Testing (`dedupe_test.go`, assert-based, `go vet ./internal/contacts/...` clean)

- Email/phone normalization, including `+1` vs bare last-10 match and blank exclusion.
- Direct pair (size 2) merges on shared email; on shared phone.
- Chain A–B–C with one shared normalized name → merges into one.
- Chain A–B–C with differing names → skipped, all three untouched.
- Size-3 sharing one email but three different names (family case) → skipped.
- Scalar most-recent-wins, with blank fields filled from older members.
- Multi-value union de-dupes emails/phones/addresses.
- Provenance: survivor `MergedUIDs` set, loser `MergedInto` set and `Deleted`.
- Survivor is the oldest contact.
- Idempotency: second `Dedupe()` returns `mergedCount == 0`.

## DOX

Update `backend/internal/contacts/AGENTS.md` (new dedupe surface, new
sync-relevant fields and their tombstone participation) and, if the route list
is documented, `backend/internal/api` docs, after implementation.

## Out of scope

- Preview/confirm two-step flow (endpoint applies immediately).
- Auto-triggering after CardDAV import (can be layered on later).
- Un-merge / split.
- Fuzzy name matching or libphonenumber-grade phone parsing.
