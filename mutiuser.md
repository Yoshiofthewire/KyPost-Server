# Multi-User Implementation Plan (Claude Handoff)

## Objective
Convert the application from single-admin global state to a true multi-user model with role-based access control.

Required behavior:
- Admin can create, edit, and delete users.
- Non-admin users cannot edit users.
- Non-admin users cannot view logs.
- Non-admin users can pair their own devices.
- Non-admin users can tune their own prompts.
- Non-admin users can make their own configuration changes, except Remote LLM settings.

## Product Decisions Already Confirmed
- User-scoped configuration: yes.
- Mailbox model: each user has their own IMAP account.
- Admin visibility model: admin manages users and system settings, not automatic cross-user inbox/content access.
- User deletion: soft-delete (deactivate) first, optional purge later.
- Password model: users can change their own password; admins can reset any user password.

## Current-State Constraints (Important)
- Backend auth is currently single-admin from `admin.env`.
- Sessions are currently in-memory (`token -> expiresAt`), not durable.
- Most state is global today (`state.json`, `decisions.json`, IMAP config, Novu subscriber id, notification subscriptions).
- Poller processes a single mailbox stream today.
- Frontend route guards are auth-only, not role-aware.

## Implementation Phases

### Phase 0: Contracts and Migration Safety
1. Define a strict endpoint/field permission matrix.
2. Add one-time migration marker and backups for existing auth/state files.
3. Decide final error contract for 401/403 responses.

Deliverable:
- Written RBAC matrix and migration checklist committed with code.

### Phase 1: Identity and Session Foundation
1. Introduce multi-user store with role field (`admin`, `user`).
2. Migrate auth model from single admin to user table (bootstrap from legacy `admin.env` for compatibility).
3. Change session data model to `token -> {userId, role, expiresAt}`.
4. Keep cookie/session behavior compatible with existing clients.
5. Add user lifecycle ops:
   - create user
   - edit role
   - deactivate/reactivate
   - reset password (admin)

Deliverable:
- Stable login/session identity context with user and role everywhere.

### Phase 2: Authorization Enforcement
1. Add middleware levels:
   - authenticated
   - admin-only
   - user-scoped self access
2. Restrict logs endpoints to admin-only.
3. Restrict user-management endpoints to admin-only.
4. Keep Remote LLM settings admin-only and global.
5. Enforce field-level config permissions (user-editable vs admin-global).

Deliverable:
- Server-side policy enforcement (frontend gating is additive, not primary security).

### Phase 3: Per-User Persistence + Migration
1. Namespace state/secrets by user.
2. Migrate these global items into per-user ownership:
   - IMAP credentials
   - notification subscriptions
   - Novu subscriber identity
   - user tuning prompt
   - mailbox checkpoint + processed set
3. Map current single-user data to admin user on first migration.
4. Keep soft-deleted users blocked from authentication, retain data until explicit purge.

Deliverable:
- Per-user storage model with backward-compatible migration path.

### Phase 4: Multi-User Poller Refactor
1. Refactor poller from one mailbox to per-active-user execution.
2. Load per-user IMAP config, checkpoint, processed state, tuning, and notification preferences.
3. Apply bounded concurrency.
4. Ensure fault isolation: one failing mailbox does not block others.
5. Preserve global safety controls (rate limits, scan interval) with fairness.

Deliverable:
- Reliable multi-tenant processing loop.

### Phase 5: Frontend RBAC + Admin UX
1. Extend auth payload with `role` and stable user identity.
2. Add role-aware route guards and nav filtering.
3. Add admin-only Users page:
   - create/edit/deactivate/reactivate/reset-password
4. Hide and hard-block Logs for non-admin users.
5. Split config UX by scope:
   - User-owned settings
   - Admin-only global/system settings (including Remote LLM)
6. Ensure notifications/device pairing operate in signed-in user context only.

Deliverable:
- UX that matches backend policy and avoids accidental privilege exposure.

### Phase 6: Test, Rollout, and Docs
1. Backend tests:
   - auth and role checks
   - user lifecycle
   - migration from legacy single-admin
   - per-user state isolation
   - poller isolation/fairness
2. Frontend tests:
   - route/nav permissions
   - 401/403 handling
   - Users page workflows
3. E2E tests with 1 admin + 2 users:
   - non-admin cannot view logs
   - non-admin cannot manage users
   - non-admin can pair own devices
   - non-admin can tune own prompt
   - non-admin can change own allowed config
   - non-admin cannot change Remote LLM settings
4. Update operational docs for migration and rollback.

Deliverable:
- Production-ready rollout package with verification and recovery plan.

## Permission Matrix (Target)

### Admin
Allowed:
- User management CRUD (with soft-delete/deactivate default).
- Logs access.
- Global system config updates.
- Remote LLM config updates.
- Health repair and platform-level operations.

Not implied:
- Automatic access to user inbox/content unless explicitly implemented.

### User
Allowed:
- Own IMAP account setup and email operations.
- Own device pairing/subscriptions.
- Own notification preferences.
- Own tuning prompt.
- Own allowed config fields.

Denied:
- User management endpoints.
- Logs endpoints.
- Remote LLM config changes.
- Any cross-user data access.

## Data Ownership Model (Target)
- Global/system-owned:
  - Remote LLM endpoint/auth/path
  - system rate limits
  - scan interval
  - redaction patterns
  - label allowlist/mappings (unless later intentionally made user-scoped)
  - VAPID key material
- User-owned:
  - IMAP/SMTP credentials
  - mailbox state (checkpoint, processed set)
  - notification subscriptions
  - Novu subscriber id and pairing state
  - tuning prompt
  - user-specific preferences

## API Changes (Suggested)

Auth/User:
- `GET /api/auth/me` -> include `userId`, `username`, `role`, `mustChangePassword`.
- `POST /api/users` (admin)
- `GET /api/users` (admin)
- `PUT /api/users/:id` (admin)
- `POST /api/users/:id/reset-password` (admin)
- `POST /api/users/:id/deactivate` (admin)
- `POST /api/users/:id/reactivate` (admin)

Policy behavior:
- All protected endpoints evaluate role and user scope server-side.
- Config update endpoint enforces field-level rules for users.

## High-Risk Areas to Treat Carefully
1. One-time migration correctness and rollback.
2. Poller fairness and resource contention under many users.
3. Notification fanout correctness after per-user split.
4. Session invalidation behavior when roles/users change.
5. Avoiding accidental exposure via legacy endpoints still returning global data.

## Rollout Strategy
1. Ship Phase 1+2 behind compatibility-safe defaults.
2. Run migration in staging with backup/restore drills.
3. Ship per-user persistence and poller refactor after migration validation.
4. Enable frontend RBAC UX once backend enforcement is complete.
5. Promote with E2E acceptance gates.

## Acceptance Criteria
- Admin can create/edit/deactivate/reactivate/delete users.
- Non-admin cannot access user-management APIs or UI.
- Non-admin cannot access logs APIs or UI.
- Non-admin can pair their own devices only.
- Non-admin can tune their own prompts only.
- Non-admin can update their own allowed config fields only.
- Non-admin cannot update Remote LLM settings.
- Existing single-user deployments migrate without data loss.
- One failing user mailbox does not block others.

## Suggested File Touchpoints
Backend:
- `backend/internal/api/server.go`
- `backend/internal/state/store.go`
- `backend/internal/processor/poller.go`
- `backend/internal/config/config.go`
- `backend/internal/app/app.go`

Frontend:
- `frontend/src/App.tsx`
- `frontend/src/pages/LoginPage.tsx`
- `frontend/src/pages/ConfigPage.tsx`
- `frontend/src/pages/NotificationsPage.tsx`
- `frontend/src/api/config.ts`
- (new) admin user-management page/component(s)

Docs:
- `README.md`
- relevant AGENTS.md docs if ownership/contracts change

## Verification Commands
Backend:
- `cd backend && go build -buildvcs=false ./...`
- `cd backend && go test ./...`

Frontend:
- `cd frontend && npm run build`

End-to-end:
- run role-policy scenarios with at least admin + two standard users.

## Notes for Claude
- Prefer minimum-diff, root-cause fixes over scattered per-endpoint patches.
- Keep server-side enforcement as source of truth; frontend gating is UX only.
- Preserve backward compatibility during migration from `admin.env`.
- Do not assume admin should read user email content unless explicitly requested later.
