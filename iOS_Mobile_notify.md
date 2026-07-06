# iOS Mobile Notify — Push/Pull Sync Spec

This document covers everything needed to bring the iOS app (see
[iOS_Mobile_App.md](iOS_Mobile_App.md)) onto the same push-notification and pairing system
the Android app already uses, **and** the corresponding server-side changes.

**Two repos are involved:**
- `llama-mobile` (this repo) — the Android reference implementation lives here; the new iOS
  client's push module goes in the new iOS project.
- `~/git/llama labels` (sibling repo) — the backend (`backend/`, Go) and the Cloudflare Worker
  relay (`worker/`, TypeScript) that actually deliver push notifications. **The server-side
  changes in this document belong in that repo, not this one.**

Hand this file to Claude with both repos checked out; it should identify which changes go
where from the file paths cited below.

## How the existing system works (Android, today)

1. **Desktop pairing.** The web app shows a QR/deep-link:
   `llamalabels://native-pair?sub=<subscriberId>&hash=<subscriberHash>&srv=<serverUrl>&reg=<registrationUrl>&pt=<pairingToken>`.
   `reg` is optional; when absent the client derives `{srv}/api/notifications/native/register`.
2. **Registration.** The mobile app calls `POST {registrationUrl}` with the FCM token it just
   obtained, plus `subscriberId`, `subscriberHash`, `pairingToken`, `platform`, `deviceId`
   (empty on first call), `deviceName`, `appVersion`. The server validates the pairing token
   (HMAC, ~90s TTL) and the subscriber hash, resolves which user account owns that subscriber,
   and upserts a `NativeDevice` record keyed by `deviceId` (server mints one on first register
   and returns it — the client must persist and resend it on every later call so re-syncs
   update the same device row instead of creating duplicates).
3. **Delivery modes.** Per-user setting, `push` (default) or `pull`, chosen on the web
   Notifications page:
   - **push**: new-email events go to a Cloudflare Worker relay, which holds the shared
     Firebase service account and calls FCM's HTTP v1 API on the self-hosted server's behalf
     (so individual self-hosters never need their own Firebase project).
   - **pull**: the server queues notifications server-side (bounded ring buffer, 100 per
     user) with a monotonic `seq` cursor; the device polls
     `GET {pullEndpoint}?sub=&hash=&after=<cursor>` directly, no relay/FCM involved at all.
     Both the register response and every pull response carry the authoritative
     `deliveryMode`, so the client always re-derives which mode it's in rather than trusting
     local state.
4. **Background cadence in pull mode**: WorkManager periodic work at the OS floor (15 min),
   plus an immediate pull on app foreground and right after (re)pairing.
5. **Token refresh**: FCM hands out new tokens periodically; the client repeats the same
   registration call with the new token whenever that happens.

None of this is Android-specific by design — the whole point of the relay and the
subscriber-hash-based pull endpoint is that they're unauthenticated-by-session and
platform-agnostic. iOS should speak the exact same HTTP contract.

## Server-side: what already supports iOS

- `backend/internal/api/server.go`, `normalizeNativePlatform()` (around line 1311) already
  accepts `"ios"` as a valid platform value (anything else defaults to `"android"`). **The
  iOS client must send `"platform": "ios"` exactly** (lowercase) — sending anything else
  silently mislabels the device as Android in `ListNativeDevices`/`NativeDevice.Platform`.
- `backend/internal/processor/native_sender.go`'s `RelaySender` doc comment already states
  the relay "delivers to every platform (iOS and Android)" — the relay's `/send` endpoint
  already forwards a `platform` field through (`worker/src/index.ts`, the `FcmMessage`
  passed to `sendFcmMessage`).
- The register/pull/mode/devices HTTP endpoints (`/api/notifications/native/{register,pull,mode,devices,unpair}`)
  need **no shape changes** — they're already platform-agnostic JSON contracts.

## Server-side: real gaps to close

### 1. Worker: add an APNs-tuned payload block (`worker/src/fcm.ts`)

There's already a `TODO(ios)` at `worker/src/fcm.ts:153-156` flagging this. Today
`sendFcmMessage()` only sets `message.android.priority = "HIGH"`; FCM will still relay to
APNs for an iOS token, but without APNs-specific priority/sound/background-delivery tuning.
Add an `apns` block when `message.platform === "ios"`:

```ts
const payload = {
  message: {
    token: message.token,
    notification: { title: message.title, body: message.body },
    data: message.data ?? {},
    android: { priority: "HIGH" },
    apns: {
      headers: { "apns-priority": "10" },
      payload: {
        aps: {
          alert: { title: message.title, body: message.body },
          sound: "default",
          "mutable-content": 1,
        },
      },
    },
  },
};
```

Keep the `android` block present regardless of platform (FCM ignores blocks that don't match
the target token type), so this is additive, not conditional on removing anything.

### 2. Fix the data-payload field-name mismatch (do this for both platforms, while you're in this code)

This is a real, pre-existing bug worth fixing as part of iOS work rather than silently
copying into a second client. The Android app's FCM *data-message* parser
(`app/src/main/java/com/urlxl/mail/push/PushPayload.kt`, `PushPayloadParser.parse`) reads:

```
data["messageId"], data["senderName"], data["emailSubject"], data["Keywords"]
```

But the server's push-mode data payload
(`backend/internal/processor/poller.go`, `maybeSendNativePushNotification`, around line 668)
only ever sets:

```go
data := map[string]string{
    "messageId": strings.TrimSpace(msg.ID),
    "sender":    strings.TrimSpace(msg.Sender),
    "subject":   strings.TrimSpace(msg.Subject),
    "title":     title,
    "body":      body,
    "url":       "/read",
}
```

There is no `senderName`, `emailSubject`, or `Keywords` key anywhere in the backend or
worker (`grep -rn "senderName\|emailSubject" backend worker` returns nothing). In practice
this is currently masked because:
- When the app is **backgrounded**, Android's FCM SDK auto-displays the top-level
  `notification.title`/`body` (which *are* set correctly to sender/subject) without ever
  calling `onMessageReceived`/the parser.
- When the app is **foregrounded**, `onMessageReceived` *does* run the parser, which finds
  none of its expected keys and silently falls back to "New email" / "You received a new
  labeled email" — losing the actual sender/subject.
- **Pull mode is unaffected** because `PullNotification.toPushPayload()`
  (`app/src/main/java/com/urlxl/mail/push/PullNotification.kt`) reads the top-level
  `title`/`body` fields first and only falls back to `data["sender"]`/`data["subject"]`,
  which do match what the server sends there.

**Fix** (smallest diff): add `senderName`, `emailSubject`, and `Keywords` (comma-joined) to
the `data` map built in `maybeSendNativePushNotification` in `poller.go`, alongside the
existing keys (don't remove `sender`/`subject`/`title`/`body`/`url` — pull mode and any other
consumer still uses them):

```go
data := map[string]string{
    "messageId":    strings.TrimSpace(msg.ID),
    "sender":       strings.TrimSpace(msg.Sender),
    "subject":      strings.TrimSpace(msg.Subject),
    "senderName":   strings.TrimSpace(msg.Sender),
    "emailSubject": strings.TrimSpace(msg.Subject),
    "Keywords":     strings.Join(messageKeywords, ","),
    "title":        title,
    "body":         body,
    "url":          "/read",
}
```

Do this before writing the iOS client's data-message parser, and build the iOS parser to
read `senderName`/`emailSubject`/`Keywords`/`messageId` (matching Android's parser) so both
clients are correct against the same payload, instead of inventing a third field-name
convention.

### 3. No changes needed to device dedup / pairing / pull-queue logic

`UpsertNativeDevice` (`backend/internal/state/store.go:529`) already dedupes purely by
`deviceId`, mints one server-side when the client omits it, and the register response
returns it for the client to persist — this already works identically regardless of
platform. Same for the pull queue (`EnqueuePullNotification`/`PullNotificationsAfter`,
monotonic `seq`, 100-entry cap) and the pairing-token HMAC validation. No iOS-specific
handling is required here.

## iOS client: how it must integrate

### Registering for FCM (reuse the existing relay — do not build a raw APNs sender)

The Cloudflare Worker relay only speaks FCM's HTTP v1 API; it does not hold an APNs
Auth Key or talk to `api.push.apple.com` directly. The correct integration is therefore:
**Firebase iOS SDK on the client, terminating at the same Firebase project the relay's
service account belongs to** — Firebase forwards to APNs on your behalf. This requires
one-time console setup (not code): add an iOS app to that Firebase project, download
`GoogleService-Info.plist`, and upload the project's APNs Authentication Key (`.p8`) under
Firebase Console → Project Settings → Cloud Messaging → APNs Authentication Key. Once that's
done, no backend/relay change is needed to *receive* the token — only the `apns` payload
tuning in gap #1 above improves delivery quality.

Client-side flow (parity with `LlamaFirebaseMessagingService.kt` /
`NativeRegistration.kt`/`PushSyncCoordinator.kt`):

1. Request notification authorization (`UNUserNotificationCenter.requestAuthorization`) and
   call `UIApplication.shared.registerForRemoteNotifications()`.
2. In `application(_:didRegisterForRemoteNotificationsWithDeviceToken:)`, forward the raw
   APNs token to `Messaging.messaging().apnsToken`.
3. Read `Messaging.messaging().token` (async) — this is the FCM registration token, the
   `deviceToken` field in the register request.
4. On successful pairing-QR scan or app launch, `POST` to the resolved registration
   endpoint with the same JSON body shape as Android's `NativeRegistrationRequest`:
   ```json
   {
     "subscriberId": "...",
     "subscriberHash": "...",
     "pairingToken": "...",
     "deviceToken": "<FCM token>",
     "deviceId": "<persisted UUID, or omit on first call>",
     "platform": "ios",
     "deviceName": "<UIDevice model, e.g. \"iPhone15,3\">",
     "appVersion": "llama Mail for iOS v<N>"
   }
   ```
5. On `200` with `ok:true, synced:true`, persist `deviceId`, `deliveryMode`, and
   `pullEndpoint` from the response (mirror `PushRepository.savePairing`/`updateDelivery`).
   On `401`, treat as an expired/invalid pairing token and prompt to rescan; on `503`, the
   backend has no `PAIRING_SECRET` configured — not something the client can retry around.
6. Implement `MessagingDelegate.messaging(_:didReceiveRegistrationToken:)` to repeat step 4
   whenever the token rotates (parity with `onNewToken` in `LlamaFirebaseMessagingService`).
7. Implement `UNUserNotificationCenterDelegate` to parse the notification's `userInfo`
   (equivalent to `RemoteMessage.data`) with the same field names as Android's
   `PushPayloadParser` (`messageId`, `senderName`, `emailSubject`, `Keywords` — see gap #2)
   so foregrounded notifications and in-app history show correct sender/subject rather than
   a generic fallback, and to display a local notification/banner + append to in-app history
   when the app is foregrounded (iOS suppresses the system banner for a foregrounded app by
   default unless the delegate explicitly requests `.banner`/`.sound`).

### Pull mode (identical HTTP contract, no server change needed)

```
GET {pullEndpoint}?sub=<subscriberId>&hash=<subscriberHash>&after=<cursor>
```
Auth is the query params only — no session/bearer, same as Android's `PullNotificationClient`.
Response:
```json
{ "deliveryMode": "push"|"pull", "cursor": 123, "notifications": [
    { "seq": 124, "title": "...", "body": "...", "data": {"messageId": "...", "sender": "...", "subject": "..."}, "createdAt": "RFC3339" }
] }
```
De-duplicate by `seq > currentCursor`, advance a durable per-subscriber cursor to
`max(current, response.cursor)` **only after** the batch is persisted/displayed (so a crash
mid-batch re-fetches rather than drops — same ordering as `PullSyncCoordinator.handleSuccess`).
`deliveryMode` in the response is authoritative — flipping to `push` on the web must stop the
client's polling; flipping to `pull` must start it, re-read on every foreground.

Background cadence: register a `BGAppRefreshTask` (identifier e.g.
`com.urlxl.mail.ios.pull-refresh`) as the parity for WorkManager's 15-minute periodic worker
— iOS doesn't guarantee exact intervals for `BGAppRefreshTask`, which is an acceptable/known
degradation of the same "up to ~15 min latency while backgrounded" tradeoff Android already
documents (`PullWorker.kt` doc comment). Always also do an immediate pull on
`scenePhase == .active` (parity with `LlamaApp.onStart`).

### Secure storage

Store `subscriberId`, `subscriberHash`, `serverUrl`, `registrationUrl`, `pairingToken`, and
`deviceId` in the iOS Keychain (`kSecClassGenericPassword`), matching the isolation Android
gives this data via `EncryptedSharedPreferences`/`SecurePairingStore` — keep it out of the
same plaintext store used for notification history/sync-status UI state (that part can stay
in `UserDefaults`, matching Android's plaintext DataStore for the same data).

### Deep link / QR pairing

Register the custom URL scheme `llamalabels://native-pair` (the same one Android uses — no
new server-side scheme needed) and parse `sub`, `hash`, `srv`, `pt`, and optional `reg` query
params exactly as `NativePairingDeepLinkParser.kt` does: `sub`/`hash` required (else "Invalid
pairing parameters"), `pt` required (else "Missing pairing token"), `srv` required unless
`reg` is present (else "Missing server URL"). For in-app QR scanning (the "Scan QR Code"
button), use `VisionKit`'s `DataScannerViewController` (iOS 16+) or an `AVCaptureSession`
metadata scanner as the parity for Android's ML Kit `GmsBarcodeScanning`.

## Task checklist

**In `~/git/llama labels` (backend + worker repo):**
- [ ] `worker/src/fcm.ts`: add the `apns` payload block (gap #1).
- [ ] `backend/internal/processor/poller.go`: add `senderName`/`emailSubject`/`Keywords` to
      the native push data map in `maybeSendNativePushNotification` (gap #2).
- [ ] Add/extend a worker test asserting the `apns` block appears when `platform === "ios"`.
- [ ] Add/extend `backend/internal/processor/native_sender_test.go` or
      `poller_test.go` asserting the new data keys are present.
- [ ] No changes needed to `server.go` HTTP handlers, `store.go` state model, or the
      register/pull/mode API shapes.

**In the new iOS project:**
- [ ] Firebase iOS SDK integrated against the same Firebase project as the Worker relay;
      APNs Auth Key uploaded to that Firebase project (console step, not code).
- [ ] Registration/token-refresh flow calling the existing `/api/notifications/native/register`
      endpoint with `platform: "ios"`.
- [ ] Pull-mode client (`GET .../native/pull`) with cursor persistence.
- [ ] Local notification display + in-app history, parsing `senderName`/`emailSubject`/
      `Keywords`/`messageId` from `userInfo`.
- [ ] Keychain-backed pairing store; `UserDefaults`-backed history/sync-state.
- [ ] `BGAppRefreshTask` periodic pull + immediate pull on foreground.
- [ ] Deep-link handling for `llamalabels://native-pair` + in-app QR scan entry point.

## Manual QA before calling this done

- [ ] Pair a real iOS device via QR; confirm the server records `platform: "ios"` (check
      `GET /api/notifications/native/devices` while signed into the web app).
- [ ] Push mode: send a test notification while the iOS app is backgrounded — confirm sound,
      correct sender/subject text (not a generic fallback), and tapping it opens the app.
- [ ] Push mode: repeat while the iOS app is foregrounded — confirm the in-app banner/history
      also shows correct sender/subject (this is what gap #2 fixes).
- [ ] Switch the account to pull mode on the web app; confirm the iOS client stops receiving
      pushes and instead picks up new mail on next foreground/background poll, with no
      duplicate or dropped notifications across app restarts.
- [ ] Force-refresh the FCM token (e.g. reinstall the app) and confirm re-registration
      updates the same `deviceId` server-side rather than creating a duplicate device row.
- [ ] Rescan an expired pairing QR and confirm the client surfaces the "rescan" guidance on a
      401, matching Android's `expiredPairingToken` handling.
