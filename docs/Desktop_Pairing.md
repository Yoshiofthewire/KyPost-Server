# Desktop Pairing Implementation Guide

## Overview

Desktop pairing allows users to initiate pairing with a desktop application. The feature generates a pairing code that can be used by desktop clients to authenticate and pair with the user's account.

## Current Implementation Status

✅ **Completed:**
- Frontend UI with "Pair Desktop App" button
- Backend endpoint: `POST /api/notifications/desktop/pair`
- Pairing code generation (6-byte random, formatted as XXXX-XXXX)
- Logging of pairing initiation events

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

### Backend (server.go)

The `handleDesktopPair` handler:
1. Validates user authentication via session cookie
2. Generates a 6-byte random code
3. Formats code as `XXXX-XXXX` (hex, uppercase)
4. Logs the pairing event with user ID only (code not logged for security)
5. Returns the code to the frontend

**Security notes:**
- Pairing code is NOT logged (prevents credential leakage via logs)
- Code returned only in JSON response to authenticated user
- Code is sensitive and should be treated as a credential

**Current limitations:**
- Code is generated on-demand, not persisted (suitable for testing/demo)
- No expiration tracking or validation yet
- No rate limiting

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

### Phase 2: Persistent Pairing State
- Store pairing codes in user's state store with 15-minute expiration
- Add `SetDesktopPairingCode()` and validation methods to `state.Store`
- Implement cleanup of expired codes

### Phase 3: Desktop App Registration
- Desktop app exchanges pairing code for access token (similar to mobile register)
- Store paired desktop sessions with metadata (app version, last seen, etc.)
- Implement session revocation

### Phase 4: Advanced Features
- QR code for desktop app enrollment
- Deep linking: `llamalabels://desktop-pair?code=XXXX-XXXX`
- Multiple simultaneous desktop pairings per user
- List and manage paired desktop sessions
- Activity logging for security audits

## Security Considerations

- ✅ Codes are random (6 bytes = 48 bits entropy)
- ✅ Requires valid session to initiate pairing
- ⚠️ TODO: Add rate limiting per user
- ⚠️ TODO: Add code expiration validation
- ⚠️ TODO: Add verification on the receiving end (not just code generation)
- ⚠️ TODO: Audit logging of all pairing events

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
