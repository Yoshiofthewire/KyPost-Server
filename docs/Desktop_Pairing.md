# Desktop Pairing Implementation Guide

## Overview

Desktop pairing allows users to initiate pairing with a desktop application. The feature generates a pairing code that can be used by desktop clients to authenticate and pair with the user's account.

## Current Implementation Status

✅ **Phase 1 Completed:**
- Frontend UI with "Pair Desktop App" button
- Backend endpoint: `POST /api/notifications/desktop/pair`
- Pairing code generation (6-byte random, formatted as XXXX-XXXX)
- Persistent storage of pairing codes in user state
- 5-minute TTL with automatic expiration
- Code validation methods for backend consumers
- Security-conscious logging (code not exposed in logs)

## API Endpoint

### POST `/api/notifications/desktop/pair`

Initiates desktop pairing and returns a pairing code.

**Authentication:** Requires valid user session (uses `withAuth` middleware)

**Request Body:**
```json
{}
```

**Response (Success - 200 OK):**
```json
{
  "ok": true,
  "pairingCode": "A1B2-C3D4"
}
```

**Response (Error - 401):**
```json
{"error": "unauthorized"}
```

**Response (Error - 500):**
```
failed to generate pairing code
```

## Implementation Details

### Backend (server.go + state/store.go)

The `handleDesktopPair` handler:
1. Validates user authentication via session cookie
2. Generates a 6-byte random code (48 bits entropy)
3. Formats code as `XXXX-XXXX` (hex, uppercase)
4. Stores code in user's state store with 5-minute expiration
5. Logs the pairing event with user ID only (code not logged for security)
6. Returns the code to the frontend

**State Store Methods:**
- `SetDesktopPairingCode(code string, ttl time.Duration)` — Stores code with expiration
- `ValidateDesktopPairingCode(code string) bool` — Checks if code is valid/not expired
- `ConsumeDesktopPairingCode(code string) (bool, error)` — Validates and removes code

**Persistence:**
- Codes stored in user's state.json file (encrypted at rest if configured)
- Automatic cleanup of expired codes on load/persist
- Survives server restart (codes are persisted until TTL)

**TTL Details:**
- **Default:** 5 minutes (300 seconds)
- **Storage:** RFC3339 formatted timestamp in user state
- **Cleanup:** Expired codes removed when state is loaded or persisted

**Security notes:**
- ✅ Pairing code is NOT logged (prevents credential leakage via logs)
- ✅ Code returned only in JSON response to authenticated user
- ✅ Code stored only in user's isolated state directory
- ✅ Codes are cryptographically random (6 bytes)
- ⚠️ TODO: Add rate limiting per user per time window
- ⚠️ TODO: Add verification/exchange endpoint for desktop app

### Frontend (NotificationsPage.tsx)

The `pairDesktopApp()` function:
1. Shows loading state ("Pairing...")
2. Calls `POST /api/notifications/desktop/pair`
3. Displays the pairing code in the status message on success
4. Shows error message if request fails

**UI Integration:**
- Button location: "Mobile App Pairing" section (replacing "Revoke Paired Devices")
- "Revoke Paired Devices" moved to footer, left of "Unsubscribe This Device"
- Navigation label changed from "Notifications" to "Pairing"
- Page title changed to "Notifications and Pairing"

## Future Enhancements

### Phase 2: Desktop App Registration Endpoint ✅ NEXT
- Add `POST /api/notifications/desktop/register` endpoint
- Desktop app exchanges pairing code for access token (similar to mobile register)
- Validate code using `store.ConsumeDesktopPairingCode()`
- Return session token with appropriate claims
- Store paired desktop sessions with metadata (app version, last seen, etc.)

### Phase 3: Desktop Session Management
- Add `GET /api/notifications/desktop/sessions` — List paired desktop apps
- Add `DELETE /api/notifications/desktop/sessions/{id}` — Revoke specific session
- Add `POST /api/notifications/desktop/unpair` — Revoke all desktop pairings
- Track session metadata: app name, version, OS, last activity time

### Phase 4: Advanced Features
- QR code generation for desktop app enrollment
- Deep linking: `llamalabels://desktop-pair?code=XXXX-XXXX`
- Activity logging for security audits (who paired, when, from where)
- Rate limiting per user (e.g., max 5 pairing attempts per hour)
- Optional: Desktop app push notifications via established session

## Security Considerations

**Implemented:**
- ✅ Codes are cryptographically random (6 bytes = 48 bits entropy)
- ✅ Requires valid session to initiate pairing (authenticated endpoint)
- ✅ 5-minute time limit (automatic expiration)
- ✅ Codes stored in isolated per-user state directory
- ✅ Codes NOT exposed in server logs
- ✅ Automatic cleanup of expired codes on state operations

**Recommended Before Production:**
- ⚠️ Add rate limiting per user (e.g., 5 attempts per hour)
- ⚠️ Add HTTPS enforcement in frontend (for code transmission)
- ⚠️ Add second factor verification for sensitive operations
- ⚠️ Implement audit logging of all pairing/unpairing events
- ⚠️ Add brute-force detection on code validation endpoint
- ⚠️ Consider additional verification (email confirmation, etc.)

**Attack Scenarios:**
- **Code interception:** Limited by 5-min TTL; HTTPS required
- **Brute force:** 6 bytes = 281.5 trillion combinations; rate limit recommended
- **Session hijacking:** Uses standard authenticated session; no additional risk
- **Log exposure:** Codes never logged; only IDs and timestamps

## Related Files

- Frontend: [frontend/src/pages/NotificationsPage.tsx](../frontend/src/pages/NotificationsPage.tsx)
- Backend: [backend/internal/api/server.go](../backend/internal/api/server.go) - `handleDesktopPair` (line 1328)
- Mobile pairing reference: [backend/internal/api/server.go](../backend/internal/api/server.go) - `handleNotificationPairing` (line 1020)
- App navigation: [frontend/src/App.tsx](../frontend/src/App.tsx) - Line 26 (nav label)

## Testing

**Manual test:**
1. Navigate to "Pairing" page in settings
2. Click "Pair Desktop App" button
3. Verify pairing code displays (e.g., "A1B2-C3D4")
4. Check server logs for: `desktop pairing initiated user_id=... pairing_code=...`

**Error test:**
1. Make request without valid session
2. Verify error handling in frontend status message
