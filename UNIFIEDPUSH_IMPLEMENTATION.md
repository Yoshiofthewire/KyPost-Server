# UnifiedPush/NTFY Implementation Summary

## Overview
This implementation adds support for NTFY (UnifiedPush) as an alternative push notification transport to the existing FCM and APNs relays. This unblocks push notifications on de-Googled Android forks (GrapheneOS, LineageOS, etc.) and provides a foundation for future KDE Mobile and Ubuntu Touch clients, all of which use UnifiedPush.

## Key Design Decisions

1. **No relay needed for UnifiedPush**: Unlike FCM and APNs, UnifiedPush endpoints (e.g., `https://ntfy.sh/<topic>`) are public URLs with no shared credentials. The backend POSTs directly to these endpoints, bypassing the Cloudflare Worker relay infrastructure.

2. **Transport field distinction**: A new `Transport` field on `NativeDevice` (values: `"fcm"`, `"apns"`, `"unifiedpush"`) decouples push delivery mechanism from OS `Platform`. This allows Android devices to choose between FCM and UnifiedPush at registration time.

3. **MFA exclusion (temporary)**: MFA push-2FA challenges are excluded from UnifiedPush in this first cut because endpoint URLs are public and unencrypted payloads expose sensitive challenge metadata. Mail notifications are unaffected. Encryption support (RFC 8291 / Web-Push-compatible mode) is a follow-up to lift this restriction.

4. **Backward compatibility**: Devices with no explicit `Transport` derive it from `Platform` (legacy behavior): iOS/macOS → APNs, everything else → FCM. Existing devices need no migration.

## Backend Changes

### Core Modules

**`backend/internal/state/store.go`**
- Added `Transport string` field to `NativeDevice` struct (with `json:"transport,omitempty"`)

**`backend/internal/api/server.go`**
- Added `Transport` field to `nativeRegisterRequest` struct
- Added `normalizeNativeTransport(transport, platform string) string` function:
  - Returns explicit transport if provided
  - Derives from platform if transport is empty (legacy)
  - Defaults to `"fcm"` for invalid/unknown transports
- Updated `handleNotificationNativeRegister` to:
  - Validate that UnifiedPush endpoints are `https://` URLs
  - Store both `Transport` and `Platform` on the device record

**`backend/internal/processor/native_sender.go`**
- Added `UnifiedPushSender` type with `Send()` method:
  - POSTs JSON payload (title, body, data) directly to the endpoint URL
  - Treats 404/410 as "stale" (endpoint no longer valid), triggering cleanup
  - 15-second timeout
- Updated `NativePushDispatcher`:
  - Added `unifiedPushSender` field
  - Replaced `senderFor(platform string)` with `selectSender(device) (interface{}, error)`:
    - Checks `device.Transport` first
    - Falls back to platform-derived routing if `Transport` is empty
    - Returns error if transport is unconfigured

**`backend/internal/api/push_mfa_handlers.go`**
- Updated `dispatchPushChallenge()` to filter out devices with `Transport == "unifiedpush"` before sending MFA challenges
- Temporary workaround noted in comments; encryption support will lift this restriction

### Tests
- `backend/internal/api/server_push_normalize_test.go`: Unit tests for `normalizeNativeTransport` (15 test cases covering explicit, derived, case-insensitive, and whitespace scenarios)
- `backend/internal/processor/native_sender_test.go`: Added three new tests:
  - `TestUnifiedPushSenderSendSuccess`: Verifies UP sender POSTs correctly formatted JSON
  - `TestUnifiedPushSenderReturnsStaleError`: Verifies 404/410 returns stale error
  - `TestDispatcherSelectSenderByTransport`: Verifies routing by Transport + Platform (7 sub-cases)
  - `TestDispatcherSendRoutesCorrectly`: Verifies Send() dispatches to the right sender type

## Android Client Changes

### Core Modules

**`app/build.gradle.kts`**
- Added `org.unifiedpush.android:connector:2.6.0` dependency (alongside firebase-messaging, no flavor split needed)

**`app/src/main/java/com/urlxl/mail/push/PushTokenProvider.kt`** (new file)
- Abstract `PushTokenProvider` interface for token acquisition (sync or async)
- `FcmTokenProvider`: wraps Firebase's `getInstance().token.await()`
- `UnifiedPushTokenProvider`: triggers distributor picker and receives endpoint asynchronously

**`app/src/main/java/com/urlxl/mail/push/LlamaUnifiedPushReceiver.kt`** (new file)
- Implements `MessagingReceiver` for UnifiedPush protocol events
- `onNewEndpoint()`: calls `syncProvidedToken(endpoint, transport="unifiedpush")`
- `onMessage()`: parses JSON payload and routes through existing `PushPayloadParser` → `PushNotificationDispatcher` pipeline (no MFA parsing, since MFA is excluded)
- `onRegistrationFailed()` / `onUnregistered()`: logs and lets FCM/pull-mode fallback handle delivery

**`app/src/main/java/com/urlxl/mail/push/NativeRegistration.kt`**
- `NativeRegistrationRequest`: added optional `transport` field
- `NativeRegistrationRequestMapper.map()`: added `transport` parameter (defaults to null, sent to backend)
- `NativeRegistrationClient.register()`: added `transport` parameter, threaded to mapper and sent to backend

**`app/src/main/java/com/urlxl/mail/push/PushSyncCoordinator.kt`**
- `syncProvidedToken()`: added optional `transport` parameter
- `syncAndPersist()`: added optional `transport` parameter, threaded to registration client

**`app/src/main/AndroidManifest.xml`**
- Registered `LlamaUnifiedPushReceiver` with `<receiver>` tag and intent-filters:
  - `org.unifiedpush.android.connector.MESSAGE`
  - `org.unifiedpush.android.connector.NEW_ENDPOINT`
  - `org.unifiedpush.android.connector.REGISTRATION_FAILED`
  - `org.unifiedpush.android.connector.UNREGISTERED`

## Testing Strategy

### Backend

1. **Unit Tests** (passing):
   - `TestNormalizeNativeTransport`: 15 cases (explicit, derived, case-insensitive, whitespace, fallback)
   - `TestUnifiedPushSenderSendSuccess`: Verifies HTTP POST with JSON payload
   - `TestUnifiedPushSenderReturnsStaleError`: Verifies 404/410 handling
   - `TestDispatcherSelectSenderByTransport`: 7 routing scenarios
   - `TestDispatcherSendRoutesCorrectly`: Send method dispatches correctly

2. **Manual Integration Tests** (to be performed):
   - Register a device with `transport=unifiedpush` and a real ntfy topic URL (e.g., `https://ntfy.sh/<random-test-topic>`)
   - Trigger a mail notification and verify it appears in the ntfy web UI / Android app
   - Verify an MFA push-2FA challenge to the same device is NOT sent (check server logs / database record)
   - Confirm device cleanup works: if the ntfy endpoint 404s, verify the device is marked stale and removed

### Android

1. **Automated Checks**:
   - Gradle build succeeds (all new .kt files compile)
   - Manifest registers receiver correctly

2. **Manual Functional Tests** (to be performed):
   - Install llama-mobile on a device with ntfy app installed
   - Trigger pairing and select ntfy as the distributor
   - Verify `onNewEndpoint` callback fires
   - Verify `NativeRegistrationClient.register()` succeeds with `transport=unifiedpush`
   - Trigger a server-side mail notification
   - Verify `onMessage` callback receives the payload and notification appears in Android UI
   - Verify FCM path remains functional on a stock device with Play Services (regression check)

## Deployment Notes

- **No database migrations**: Transport is an optional field; existing rows decode as null (legacy behavior)
- **No Cloudflare changes**: FCM and APNs relays unchanged
- **Rate limiting**: UnifiedPush transport gets no Cloudflare-side rate limiting; relies on the receiving ntfy/distributor server's own limits
- **Analytics**: UnifiedPush sends are not recorded in Cloudflare Analytics Engine (trade-off for simpler deployment)

## Future Work

1. **End-to-end Encryption**: Implement RFC 8291 / Web-Push-compatible encrypted payloads, allowing MFA challenges over UnifiedPush
2. **KDE Mobile / Ubuntu Touch Clients**: Once built, they call the same `/api/notifications/native/register` endpoint with `transport=unifiedpush` and their own `platform` value
3. **Cloudflare Rate Limiting**: If abuse occurs, add Durable Objects-based per-distributor rate limits
4. **Analytics**: Record UnifiedPush send metrics in a similar way once usage patterns stabilize
