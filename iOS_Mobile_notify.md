# Llama Mail iOS Notifications — Backend & Worker Integration

This document describes the backend and Cloudflare Worker changes required to support iOS native push notifications via **direct APNs** (not Firebase) alongside the existing Android FCM implementation.

## Architecture Overview

The notification delivery path splits by platform at the backend's poller:

```
Backend Poller (Go)
    ├─ if device.Platform == "android"
    │   └─ POST /send → FCM Worker (existing: worker/)
    │       └─ FCM HTTP v1 → Android Device
    │
    └─ if device.Platform == "ios"
        └─ POST /send → APNs Worker (new: worker-apns/)
            └─ APNs HTTP/2 → iOS Device
```

Both workers use **identical** `/send` request/response contracts (preserving the Go backend's `RelaySender` abstraction entirely unchanged). Only the push service they call differs.

---

## Part 1: Backend changes (small, contained)

### 1.1 Go `native_sender.go` — dual-relay instantiation

Currently:
```go
type RelaySender struct {
    relayURL string
    apiKey   string
    client   *http.Client
}

func NewRelaySenderFromEnv(log *logging.Logger) *RelaySender {
    // reads PUSH_RELAY_URL, PUSH_RELAY_KEY, PUSH_RELAY_KEY_FILE
}
```

Change to support two senders:

```go
type NativePushDispatcher struct {
    fcmSender   *RelaySender     // Android → existing worker/ (FCM)
    apnsSender  *RelaySender     // iOS → new worker-apns/ (direct APNs)
}

func NewNativePushDispatcher(log *logging.Logger) *NativePushDispatcher {
    return &NativePushDispatcher{
        fcmSender:  newRelaySenderFromEnvWithPrefix(log, "PUSH_RELAY"),      // existing vars
        apnsSender: newRelaySenderFromEnvWithPrefix(log, "APNS_RELAY"),      // new vars
    }
}

func (d *NativePushDispatcher) Send(ctx context.Context, device state.NativeDevice, message NativePushMessage) error {
    var sender *RelaySender
    if strings.EqualFold(strings.TrimSpace(device.Platform), "ios") {
        sender = d.apnsSender
    } else {
        sender = d.fcmSender    // default to FCM for "android" and any other value
    }
    
    if sender == nil {
        return errors.New("push relay not configured for " + device.Platform)
    }
    return sender.Send(ctx, device, message)
}

// Extract the key-resolution logic from NewRelaySenderFromEnv into a parameterizable helper:
func newRelaySenderFromEnvWithPrefix(log *logging.Logger, prefix string) *RelaySender {
    relayURL := strings.TrimRight(strings.TrimSpace(os.Getenv(prefix + "_URL")), "/")
    if relayURL == "" {
        return nil
    }
    
    apiKey, err := resolveRelayKeyWithPrefix(log, prefix, relayURL, &http.Client{Timeout: 15 * time.Second})
    if err != nil || apiKey == "" {
        return nil
    }
    
    return &RelaySender{
        relayURL: relayURL,
        apiKey:   apiKey,
        client:   &http.Client{Timeout: 15 * time.Second},
    }
}

// Similar parameterization of resolveRelayKey, registerWithRelay
func resolveRelayKeyWithPrefix(log *logging.Logger, prefix string, relayURL string, client *http.Client) (string, error) {
    // reads {prefix}_KEY, {prefix}_KEY_FILE, then auto-registers
    // (copy the exact logic from the current resolveRelayKey, just swap env-var names)
}
```

### 1.2 Go `poller.go` — dispatcher wiring

Currently:
```go
type Poller struct {
    ...
    nativeSender *RelaySender
}

func NewPoller(...) *Poller {
    return &Poller{
        ...
        nativeSender: NewRelaySenderFromEnv(log),
    }
}
```

Change to:
```go
type Poller struct {
    ...
    nativePushDispatcher *NativePushDispatcher
}

func NewPoller(...) *Poller {
    return &Poller{
        ...
        nativePushDispatcher: NewNativePushDispatcher(log),
    }
}

// In handleMessage (around line 713):
// OLD: err := p.nativeSender.Send(sendCtx, device, notification)
// NEW:
err := p.nativePushDispatcher.Send(sendCtx, device, notification)
```

**Impact on other code:** `isRelayStaleResponse` handling (line ~723) stays exactly the same — both relays return `410 {stale:true}` and 502 errors identically.

### 1.3 Environment variables

No new backend env vars needed in the Go app — all APNs relay config is scoped to the worker deployment (see Part 2). The Go backend reads:

- Existing: `PUSH_RELAY_URL`, `PUSH_RELAY_KEY` / `PUSH_RELAY_KEY_FILE`
- New: `APNS_RELAY_URL`, `APNS_RELAY_KEY` / `APNS_RELAY_KEY_FILE` (same pattern, different endpoints)

Auto-registration follows the existing precedence: explicit env key → persisted key file → auto-register at startup.

---

## Part 2: New Cloudflare Worker — APNs relay (`worker-apns/`)

Directory structure:
```
worker-apns/
├── src/
│   ├── index.ts         # API-key management, routing (reuse pattern from worker/)
│   ├── apns.ts          # APNs provider-token signing + send
│   └── types.ts         # shared types (PushMessage, etc)
├── wrangler.toml        # configuration
├── wrangler.toml.example
├── package.json
├── package-lock.json
├── tsconfig.json
└── README.md
```

### 2.1 `src/index.ts` — API contract (reuse from `worker/src/index.ts`)

The APNs worker exposes the **identical** `/health`, `/register`, `/send`, `/admin/keys` routes as the FCM worker. Only the underlying delivery mechanism differs.

Copy `worker/src/index.ts` wholesale and change:

1. **`handleSend` call site** (line ~297 in worker/):
```typescript
// OLD:
result = await sendFcmMessage(config, env.OAUTH_CACHE, message);

// NEW:
result = await sendApnsMessage(env.APNS_TOKEN_CACHE, message);
```

2. **`fcmConfig`** → drop it (not needed for APNs):
```typescript
// Delete the fcmConfig() helper and its isConfigured() check (~lines 150–160)
// Add instead:

function apnsConfigured(env: Env): boolean {
    return Boolean(
        (env.APNS_AUTH_KEY ?? "").trim() &&
        (env.APNS_KEY_ID ?? "").trim() &&
        (env.APNS_TEAM_ID ?? "").trim() &&
        (env.APNS_TOPIC ?? "").trim()
    );
}
```

3. **`/health` response** — swap FCM config for APNs config:
```typescript
// OLD:
return json({
    ok: true,
    configured: isConfigured(fcmConfig(env)),
    ...
});

// NEW:
return json({
    ok: true,
    configured: apnsConfigured(env),
    ...
});
```

Everything else (rate limiting, key management, analytics, request validation) is identical.

### 2.2 `src/apns.ts` — APNs HTTP/2 sender

```typescript
/**
 * APNs HTTP/2 push delivery for the Cloudflare Worker relay.
 *
 * Generates ES256 provider tokens and sends to Apple's push service.
 */

const APNS_PRODUCTION_HOST = "api.push.apple.com";
const APNS_SANDBOX_HOST = "api.sandbox.push.apple.com";

export interface ApnsConfig {
    authKey: string;        // .p8 PEM contents
    keyId: string;          // from Apple Developer portal
    teamId: string;         // Team ID from Apple Developer portal
    topic: string;          // bundle ID, e.g. "com.urlxl.mail"
    environment: "production" | "sandbox";
}

export interface PushMessage {
    token: string;
    title: string;
    body: string;
    data?: Record<string, string>;
}

export type ApnsResult =
    | { ok: true }
    | { ok: false; stale: true; status: number; detail: string }     // device token is dead
    | { ok: false; stale: false; status: number; detail: string };   // transient/server error

/**
 * Import a PKCS#8 PEM ES256 private key for APNs provider-token signing.
 */
async function importApnsPrivateKey(pem: string): Promise<CryptoKey> {
    const normalized = pem.replace(/\\n/g, "\n").trim();
    const body = normalized
        .replace(/-----BEGIN PRIVATE KEY-----/, "")
        .replace(/-----END PRIVATE KEY-----/, "")
        .replace(/\s+/g, "");
    const der = Uint8Array.from(atob(body), (c) => c.charCodeAt(0));
    
    return crypto.subtle.importKey(
        "pkcs8",
        der,
        { name: "ECDSA", namedCurve: "P-256" },
        false,
        ["sign"]
    );
}

function base64UrlEncode(bytes: ArrayBuffer | Uint8Array): string {
    const arr = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes);
    let binary = "";
    for (let i = 0; i < arr.length; i++) {
        binary += String.fromCharCode(arr[i]);
    }
    return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function base64UrlEncodeString(input: string): string {
    return base64UrlEncode(new TextEncoder().encode(input));
}

/**
 * Generate an ES256 provider token for APNs. Valid for up to ~60 minutes.
 * Note: Apple asks that you not regenerate more than roughly once per 20 minutes.
 */
async function generateProviderToken(config: ApnsConfig, nowSeconds: number): Promise<string> {
    const header = base64UrlEncodeString(JSON.stringify({
        alg: "ES256",
        kid: config.keyId,
        typ: "JWT",
    }));

    const claims = base64UrlEncodeString(JSON.stringify({
        iss: config.teamId,
        iat: nowSeconds,
        // Note: no 'exp' claim — Apple honors up to ~60 min
    }));

    const signingInput = `${header}.${claims}`;
    const key = await importApnsPrivateKey(config.authKey);
    
    // WebCrypto ECDSA produces raw r‖s (IEEE P1363), which is exactly what JWS ES256 needs.
    const signature = await crypto.subtle.sign(
        { name: "ECDSA", hash: "SHA-256" },
        key,
        new TextEncoder().encode(signingInput)
    );

    return `${signingInput}.${base64UrlEncode(signature)}`;
}

/**
 * Retrieve a cached APNs provider token, or generate a new one.
 * Token is cached for ~29 minutes (refresh before 30-min expiry).
 */
async function getProviderToken(config: ApnsConfig, cache: KVNamespace): Promise<string> {
    const cacheKey = "apns_provider_token";
    const cached = await cache.get(cacheKey);
    if (cached) {
        return cached;
    }

    const nowSeconds = Math.floor(Date.now() / 1000);
    const token = await generateProviderToken(config, nowSeconds);
    
    // Cache for 29 minutes (refresh before 30-min expiry)
    await cache.put(cacheKey, token, { expirationTtl: 29 * 60 });
    return token;
}

/**
 * Detect APNs device-token errors (token is dead, not provider-token errors).
 */
function isStaleResponse(status: number, response: string): boolean {
    const lower = response.toLowerCase();
    
    // APNs returns HTTP 400 with "Unregistered", "BadDeviceToken", or "DeviceTokenNotForTopic"
    if (status === 400 && (lower.includes("unregistered") || lower.includes("baddevicetoken") || lower.includes("devicetokennotfortopic"))) {
        return true;
    }
    
    // APNs returns HTTP 410 Gone for expired/revoked tokens
    if (status === 410) {
        return true;
    }

    return false;
}

/**
 * Send a single push via APNs HTTP/2. Body shape mirrors FCM's `fcm.ts` for consistency.
 */
export async function sendApnsMessage(
    config: ApnsConfig,
    cache: KVNamespace,
    message: PushMessage
): Promise<ApnsResult> {
    const token = (message.token ?? "").trim();
    if (!token) {
        return { ok: false, stale: false, status: 400, detail: "missing token" };
    }

    let providerToken: string;
    try {
        providerToken = await getProviderToken(config, cache);
    } catch (err) {
        return { 
            ok: false, 
            stale: false, 
            status: 500, 
            detail: `provider token generation failed: ${(err as Error).message}` 
        };
    }

    // APNs HTTP/2 request to /3/device/{token}
    const host = config.environment === "production" ? APNS_PRODUCTION_HOST : APNS_SANDBOX_HOST;
    const url = `https://${host}/3/device/${token}`;

    // Build the APS payload (matching FCM's structure for consistency)
    const payload = {
        aps: {
            alert: {
                title: message.title,
                body: message.body,
            },
            sound: "default",
            "mutable-content": 1,
        },
        // Spread the data fields into top-level custom keys (matching fcm.ts pattern)
        ...(message.data ?? {}),
    };

    try {
        const resp = await fetch(url, {
            method: "POST",
            headers: {
                authorization: `bearer ${providerToken}`,
                "apns-topic": config.topic,
                "apns-push-type": "alert",
                "apns-priority": "10",
                "content-type": "application/json",
            },
            body: JSON.stringify(payload),
        });

        if (resp.ok) {
            return { ok: true };
        }

        const detail = (await resp.text()).trim();
        
        // Distinguish device-token errors (stale) from provider/server errors (retriable)
        if (isStaleResponse(resp.status, detail)) {
            return { ok: false, stale: true, status: resp.status, detail };
        }
        
        // Provider-token errors: refresh the token cache and retry once
        if (resp.status === 403 && (detail.toLowerCase().includes("expiredtoken") || detail.toLowerCase().includes("invalidtoken"))) {
            await cache.delete("apns_provider_token");
            // In a real implementation, retry once with a fresh token. For simplicity, return 502 so backend retries.
            return { ok: false, stale: false, status: 502, detail: "provider token expired; backend should retry" };
        }
        
        return { ok: false, stale: false, status: resp.status, detail };
    } catch (err) {
        const msg = (err as Error).message ?? String(err);
        return { ok: false, stale: false, status: 502, detail: `apns fetch error: ${msg}` };
    }
}
```

### 2.3 `src/types.ts` (optional, for code organization)

If you want to avoid duplicating type definitions across the two workers, create a shared types file or use the same shape as FCM (no schema change needed).

### 2.4 `wrangler.toml`

```toml
name = "llama-labels-push-relay-apns"
main = "src/index.ts"
compatibility_date = "2024-11-01"

[observability]
enabled = true

[vars]
RATE_LIMIT_PER_MINUTE = "10"
REGISTRATION_ENABLED = "true"

[[kv_namespaces]]
binding = "API_KEYS"
id = "REPLACE_WITH_API_KEYS_NAMESPACE_ID"

[[kv_namespaces]]
binding = "APNS_TOKEN_CACHE"
id = "REPLACE_WITH_APNS_TOKEN_CACHE_NAMESPACE_ID"

[[unsafe.bindings]]
name = "PUSH_RATE_LIMITER"
type = "ratelimit"
namespace_id = "1002"  # different namespace_id from worker/ (which uses 1001)
simple = { limit = 10, period = 60 }

[[analytics_engine_datasets]]
binding = "USAGE_ANALYTICS"
dataset = "llama_push_usage_apns"

# Secrets (set with `npx wrangler secret put <NAME>`):
#   APNS_AUTH_KEY   - .p8 key file contents (PEM format, preserve newlines)
#   APNS_KEY_ID     - Key ID from Apple Developer portal
#   APNS_TEAM_ID    - Team ID from Apple Developer portal
#   APNS_TOPIC      - Bundle ID, e.g. "com.urlxl.mail"
#   APNS_ENVIRONMENT - "production" or "sandbox"
#   ADMIN_SECRET    - random secret for /admin/keys endpoints
```

### 2.5 `wrangler.toml.example`

Same as above, but with placeholder namespace IDs:

```toml
# ... [keep everything except the two `[[kv_namespaces]]` blocks and the one `[[unsafe.bindings]]` block]

[[kv_namespaces]]
binding = "API_KEYS"
id = "REPLACE_WITH_API_KEYS_NAMESPACE_ID"

[[kv_namespaces]]
binding = "APNS_TOKEN_CACHE"
id = "REPLACE_WITH_APNS_TOKEN_CACHE_NAMESPACE_ID"

[[unsafe.bindings]]
name = "PUSH_RATE_LIMITER"
type = "ratelimit"
namespace_id = 1002  # different from worker/'s 1001
simple = { limit = 10, period = 60 }
```

### 2.6 `README.md`

```markdown
# Llama Labels Push Relay (APNs) — Cloudflare Worker

This Worker delivers native push notifications to iOS devices via Apple Push Notification service (APNs).

The published iOS app is compiled with a single bundle ID (`com.urlxl.mail`), so only the holder of the corresponding Apple Developer Team ID can deliver push to it. Instead of shipping the APNs auth key (`.p8`) to every self-hosted server, the **maintainer** runs this Worker. Self-hosted Llama Labels servers forward push requests to it, each authenticated with its own API key. Self-hosters need **no Apple Developer account and never recompile the app**.

```
self-hosted Go server  --(Bearer per-server key)-->  this Worker  --(APNs provider token)-->  APNs  -->  iOS Device
```

## One-time setup (maintainer)

1. Install deps and log in:
   ```sh
   cd worker-apns
   npm install
   npx wrangler login
   ```

2. Create KV namespaces and get their IDs:
   ```sh
   npx wrangler kv namespace create API_KEYS
   npx wrangler kv namespace create APNS_TOKEN_CACHE
   ```

3. Create local `wrangler.toml` from the example:
   ```sh
   cp wrangler.toml.example wrangler.toml
   ```
   Edit and paste the namespace IDs from step 2 into the two `[[kv_namespaces]]` blocks.

4. Obtain an APNs Auth Key from the Apple Developer portal:
   - Log in to https://developer.apple.com/account
   - Certificates, Identifiers & Profiles → Keys
   - Click "+" to create a new key
   - Check "Apple Push Notifications service (APNs)"
   - Click "Continue", then "Register"
   - Download the `.p8` file **once** (it can't be re-downloaded — losing it means revoking and minting a new Key ID)
   - Note the Key ID and your Team ID (visible in the header of the portal)

5. Set the secrets:
   ```sh
   npx wrangler secret put APNS_AUTH_KEY    # contents of the .p8 file
   npx wrangler secret put APNS_KEY_ID      # Key ID from step 4
   npx wrangler secret put APNS_TEAM_ID     # Team ID from step 4
   npx wrangler secret put APNS_TOPIC       # com.urlxl.mail
   npx wrangler secret put APNS_ENVIRONMENT # "production" (or "sandbox" for debug builds)
   npx wrangler secret put ADMIN_SECRET     # a long random string you choose
   ```

6. Deploy:
   ```sh
   npx wrangler deploy
   ```

## Self-registration (no maintainer involvement)

Same as `worker/`: self-hosted servers call `POST /register` to get a per-server API key, which they persist and reuse on every restart.

```sh
curl -X POST https://<your-worker>.workers.dev/register \
  -H "Content-Type: application/json" \
  -d '{"label":"alice-server"}'
# -> {"id":"...","label":"alice-server","key":"<RAW KEY>","expiresAt":null}
```

## Environment-specific deployment

If you want separate dev/sandbox and production workers:

1. Duplicate `wrangler.toml` → `wrangler.prod.toml`
2. Update `APNS_ENVIRONMENT` secret and namespace IDs to point to sandbox/production Cloudflare resources respectively
3. Deploy:
   ```sh
   npx wrangler deploy --env dev
   npx wrangler deploy --env prod
   ```
4. Give the Go backend two URLs:
   - `APNS_RELAY_URL=https://<your-worker-dev>.workers.dev` (uses sandbox)
   - Or override per-environment via the standard Cloudflare environment promotion pipeline

## Troubleshooting

| Error | Cause | Fix |
|-------|-------|-----|
| `InvalidToken` (HTTP 403) | Auth key is incorrect or expired | Regenerate the `.p8` key in Apple Developer portal, rotate the secret |
| `BadDeviceToken` (HTTP 400) | Device token is malformed or stale | Device was uninstalled or re-registered; backend automatically re-tries registration on next notification |
| `DeviceTokenNotForTopic` (HTTP 400) | Token was registered for a different bundle ID | Provisioning profile mismatch; rebuild the app |
| `Unregistered` (HTTP 400) | Device revoked APNs permission or uninstalled app | Same as BadDeviceToken |
| HTTP 429 Too Many Requests | Rate limit exceeded | Increase per-worker limit in `wrangler.toml` or implement per-user throttling in the Go backend |

## HTTP/2 Support

APNs requires HTTP/2. Cloudflare Workers' `fetch()` automatically negotiates HTTP/2 via ALPN. If you see "HTTP/1.1 only" errors in logs, check whether Apple's endpoint is reachable and whether your Cloudflare plan supports HTTP/2 (all current plans do).

## Payload Compatibility

Both the FCM and APNs workers receive identical request payloads from the Go backend:

```json
{
  "token": "device-token-here",
  "title": "Alice Smith",
  "body": "Project Update",
  "data": {
    "messageId": "msg-123",
    "senderName": "Alice Smith",
    "emailSubject": "Project Update",
    "Keywords": "work,important"
  }
}
```

The APNs worker translates this to:

```json
{
  "aps": {
    "alert": { "title": "Alice Smith", "body": "Project Update" },
    "sound": "default",
    "mutable-content": 1
  },
  "messageId": "msg-123",
  "senderName": "Alice Smith",
  "emailSubject": "Project Update",
  "Keywords": "work,important"
}
```

Both platforms handle the full payload identically client-side (mirroring both Android and iOS with the same notification contract).
```

---

## Part 3: Device Registration & Dispatch (no API changes)

The existing `/api/notifications/native/register` and `/api/notifications/native/pull` endpoints **require zero changes**. The `platform` field is already stored and returned; it simply flows through to whichever relay the dispatcher picks.

### Register Request (unchanged)

```json
{
  "subscriberId": "user@example.com",
  "subscriberHash": "hmac-sha256(...)",
  "pairingToken": "token-from-qr",
  "deviceToken": "raw-apns-token-for-ios or fcm-token-for-android",
  "deviceId": null,
  "platform": "ios",
  "deviceName": "iPhone 15 Pro",
  "appVersion": "1.0.0"
}
```

### Register Response (unchanged)

```json
{
  "ok": true,
  "synced": true,
  "deviceId": "device-id-assigned-by-server",
  "deliveryMode": "push",
  "pullEndpoint": "https://backend.example.com/api/notifications/native/pull"
}
```

---

## Part 4: Deployment Checklist

### Backend (Go)

- [ ] Merge the `NativePushDispatcher` changes into `backend/internal/processor/native_sender.go`
- [ ] Merge the dispatcher wiring into `backend/internal/processor/poller.go`
- [ ] Document the new env vars: `APNS_RELAY_URL`, `APNS_RELAY_KEY`, `APNS_RELAY_KEY_FILE` (same precedence as FCM)
- [ ] Test with a mock APNs worker responding `{ok:true}` on every request (sanity-check dispatch logic)

### Cloudflare (new APNs Worker)

- [ ] Create the `worker-apns/` directory structure
- [ ] Implement `src/index.ts` (copy from `worker/`, swap FCM config for APNs config check)
- [ ] Implement `src/apns.ts` (ES256 token signing + send)
- [ ] Create `wrangler.toml` and `wrangler.toml.example` (see above)
- [ ] Create `README.md` (see above)
- [ ] Deploy to a staging domain first (`wrangler deploy --env dev`)
- [ ] Test the `/health` endpoint — it should report `configured: true` once secrets are set
- [ ] Do an early HTTP/2 spike: verify a test POST to `/send` successfully negotiates HTTP/2 to `api.push.apple.com` (or sandbox), even if the request itself fails (e.g., 400 BadDeviceToken is fine as proof the protocol worked)
- [ ] Promote to production

### iOS App

- [ ] When registering, send `platform: "ios"` and the **raw hex APNs device token** (no Firebase)
- [ ] Exact wire-contract matches: `RegisterRequest` and `PullResponse` fields as documented in the iOS build plan

---

## Operational Notes

### Token Lifecycle

- **APNs provider token** (`.p8` key) — annual rotation required. Apple sends renewal notices; losing the `.p8` key means revoking the Key ID in the portal and creating a new one.
- **Device tokens** — ephemeral; iOS can issue a new one at any time. Both workers (FCM and APNs) handle stale tokens identically: return HTTP 410 `{stale:true}`, and the backend drops the device.

### Monitoring

- **Metrics**: per-key usage counts in Analytics Engine, queryable by key ID (same as FCM worker)
- **Errors**: 
  - 4xx responses logged as `level: "warn"` (backend should re-try registration on stale, ignore malformed)
  - 5xx responses logged as `level: "error"` (transient; backend retries with backoff)
- **Rate limits**: native Cloudflare rate-limiting binding; adjust `RATE_LIMIT_PER_MINUTE` if needed

### Sandbox vs. Production

A **device token is only valid against one environment**:
- If provisioning profile has `aps-environment = development` → token works only against `api.sandbox.push.apple.com`
- If provisioning profile has `aps-environment = production` → token works only against `api.push.apple.com`

Recommendation: maintain two workers (dev/sandbox and prod/production) and point the Go backend's `APNS_RELAY_URL` accordingly based on build target. The code is the same; only the secrets/environment differ.

---

## Implementation Risk Mitigation

### Critical Path
1. **HTTP/2 feasibility** (do this early on Cloudflare)
   - Test: `curl -v --http2 https://api.sandbox.push.apple.com/3/device/test`
   - Workers' `fetch()` should handle it automatically, but confirm with a test request
2. **Provider token signing** (do this in isolation before integrating into `index.ts`)
   - Unit test: generate a token, decode it as JWT (no verification needed, just structure check)
   - Verify the `kid` and `iss` headers/claims match the test input
3. **Device token stale detection** (test with intentionally dead token)
   - Should get 400 `"Unregistered"` or 410 Gone, correctly mapped to `{stale:true}`
4. **Go dispatcher logic** (test with stubbed relays)
   - Mock two `RelaySender` instances, verify the dispatcher picks the right one based on platform

### Deferred to Production
- Full e2e test of a real iOS device sending a push (needs provisioning + dev certificate setup)
- APNs auth key rotation procedures (annual, well-documented by Apple)

---

## Summary

iOS push is now delivered via a **dedicated APNs Worker**, keeping Google out of the iOS app entirely. The backend's abstraction (`RelaySender` for relays, `NativePushDispatcher` to route by platform) is minimal and reuses all existing error-handling and rate-limiting logic. Both FCM and APNs workers implement the same `/send` contract, making the backend change a simple dispatcher swap that touches no other subsystems.
