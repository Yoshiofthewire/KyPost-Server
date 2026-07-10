# APN Relay Deployment Design

**Date:** 2026-07-10  
**Author:** Claude Code  
**Scope:** Deploy the APNs push relay to Cloudflare Workers  
**Environment:** Sandbox (testing only)  
**Success Criteria:** Relay deploys successfully and `/health` endpoint responds with `configured: true`

---

## Overview

The APNs relay is a Cloudflare Worker that delivers push notifications to iOS devices via Apple Push Notification service (APNs). It holds the APNs auth key and issues provider tokens on behalf of self-hosted Llama Labels servers, each authenticated with its own API key.

This deployment guide covers:
1. Creating Apple Developer credentials (APNs key)
2. Setting up the Cloudflare Worker with secrets and KV bindings
3. Deploying to Cloudflare
4. Validating the deployment

---

## Architecture

### Components

**Apple Developer Portal**
- Source of truth for APNs credentials (key, key ID, team ID, topic)
- Provides the `.p8` private key for ES256 token signing

**Cloudflare Workers**
- Hosts the relay at a public HTTPS endpoint
- Receives requests from self-hosted servers via Bearer token auth
- Generates APNs provider tokens (ES256 JWTs)
- Forwards push notifications to Apple's APNs servers

**Cloudflare KV Namespaces**
- `API_KEYS`: Stores API key records (hashed keys, metadata, expiry)
- `APNS_TOKEN_CACHE`: Caches provider tokens (expires every ~29 minutes)

**Rate Limiting**
- Per-key per-minute rate limiter (native Cloudflare binding, no KV writes)
- Configured limit: 10 requests/minute per API key (adjustable)

### Data Flow

```
Self-hosted server
    ↓ (POST /send with Bearer token)
Cloudflare Worker (APNs relay)
    ├→ Verify API key from KV
    ├→ Check rate limit
    ├→ Get/refresh provider token from KV cache
    ├→ Sign ES256 JWT with Apple private key
    └→ Send push via APNs HTTP/2
         ↓
    Apple APNs service
         ↓
    iOS device
```

---

## Setup Steps

### Phase 1: Apple Developer Credentials

#### Step 1.1: Create APNs Key in Developer Portal
1. Log in to [Apple Developer portal](https://developer.apple.com)
2. Navigate to **Certificates, Identifiers & Profiles** → **Keys**
3. Click **+** to create a new key
4. Name it (e.g., "Llama Mail APNs")
5. Check **Apple Push Notifications service (APNs)**
6. Click **Continue**, then **Register**
7. Download the `.p8` file immediately (can only be downloaded once)

#### Step 1.2: Capture Key Details
From the Developer portal, note down:
- **Key ID**: Shown next to the key name (e.g., `ABC123DEF4`)
- **Team ID**: Shown at top-right of portal (e.g., `AB12CD34EF`)
- **Bundle ID / Topic**: `com.urlxl.mail` (the app's bundle identifier)

Save the `.p8` file securely; you'll paste its contents into Cloudflare secrets.

---

### Phase 2: Cloudflare Worker Deployment

#### Step 2.1: Verify Cloudflare Account
- Ensure you have a Cloudflare account with Workers enabled
- Have `wrangler` CLI installed locally (`npm install -g wrangler` or use project's version)

#### Step 2.2: Create KV Namespaces
If not already created, create two KV namespaces:
```bash
wrangler kv:namespace create "API_KEYS"
wrangler kv:namespace create "APNS_TOKEN_CACHE"
```
Note the returned IDs and update `worker-apns/wrangler.toml`.

#### Step 2.3: Update wrangler.toml
In `worker-apns/wrangler.toml`:
- Verify `name`, `main`, `compatibility_date` are set
- Update KV namespace `id` fields with your real Cloudflare KV IDs
- Verify `APNS_ENVIRONMENT = "sandbox"` is set (for testing)
- Verify rate limit: `RATE_LIMIT_PER_MINUTE = "10"`

#### Step 2.4: Set Cloudflare Secrets
Deploy secrets via Wrangler (interactive):
```bash
cd worker-apns/
wrangler secret put APNS_AUTH_KEY        # Paste .p8 file contents
wrangler secret put APNS_KEY_ID          # Paste Key ID from portal
wrangler secret put APNS_TEAM_ID         # Paste Team ID from portal
wrangler secret put APNS_TOPIC           # Type: com.urlxl.mail
wrangler secret put ADMIN_SECRET         # Create a random secret for admin endpoints
```

#### Step 2.5: Deploy the Worker
```bash
cd worker-apns/
wrangler deploy
```

On success, Wrangler will output the deployed worker URL, e.g.:
```
✓ Uploaded apns relay (1.23 sec)
Deployed to: https://llama-labels-push-relay-apns.YOUR_SUBDOMAIN.workers.dev
```

---

### Phase 3: Validation

#### Step 3.1: Test /health Endpoint
```bash
curl https://llama-labels-push-relay-apns.YOUR_SUBDOMAIN.workers.dev/health
```

Expected response (pretty-printed):
```json
{
  "ok": true,
  "configured": true,
  "rateLimits": {
    "perMinute": 10
  },
  "registrationEnabled": true
}
```

If `configured` is `false`, one or more secrets are missing or invalid.

#### Step 3.2: Create a Test API Key (Optional)
To fully validate, create an API key via the admin endpoint:
```bash
curl -X POST https://llama-labels-push-relay-apns.YOUR_SUBDOMAIN.workers.dev/admin/keys \
  -H "Authorization: Bearer YOUR_ADMIN_SECRET" \
  -H "Content-Type: application/json" \
  -d '{"label":"test-key"}'
```

Expected response:
```json
{
  "id": "uuid",
  "label": "test-key",
  "key": "raw-api-key-hex",
  "expiresAt": null
}
```

Save the `key` value for later testing.

---

## Deployment Checklist

- [ ] Apple Developer account set up with APNs key created
- [ ] `.p8` file downloaded and securely stored
- [ ] Key ID, Team ID, Bundle ID captured from Developer portal
- [ ] Cloudflare account with Workers enabled
- [ ] KV namespaces created (`API_KEYS`, `APNS_TOKEN_CACHE`)
- [ ] `worker-apns/wrangler.toml` updated with real KV IDs
- [ ] `APNS_ENVIRONMENT` set to `sandbox` in wrangler.toml
- [ ] All secrets pushed to Cloudflare (APNS_AUTH_KEY, APNS_KEY_ID, APNS_TEAM_ID, APNS_TOPIC, ADMIN_SECRET)
- [ ] `wrangler deploy` completes successfully
- [ ] `/health` endpoint responds with `configured: true`
- [ ] (Optional) Test admin endpoint creates API key successfully

---

## Error Handling

### `configured: false` on /health
**Cause:** One or more APNs secrets missing or invalid  
**Solution:** Verify all secrets are set correctly in Cloudflare dashboard → Worker → Settings → Variables

### Deployment fails with "unauthorized"
**Cause:** Cloudflare authentication issue  
**Solution:** Run `wrangler login` and re-authenticate, or check Cloudflare account permissions

### KV namespace errors
**Cause:** KV IDs in wrangler.toml don't exist  
**Solution:** Recreate KV namespaces and update IDs in wrangler.toml

---

## Next Steps (Post-Deployment)

Once the relay is deployed and validated:
1. Document the relay URL and admin secret securely
2. Share the relay URL with self-hosted servers (they'll use it for push registration)
3. Begin iOS/macOS client development (out of scope for this phase)
4. Test push delivery with a real iOS device when client is ready

---

## References

- [Apple Developer: APNs Overview](https://developer.apple.com/documentation/usernotifications/setting_up_a_remote_notification_server)
- [Cloudflare Workers: Environment Variables & Secrets](https://developers.cloudflare.com/workers/wrangler/configuration/)
- [APNs HTTP/2 API](https://developer.apple.com/documentation/usernotifications/setting_up_a_remote_notification_server/sending_notification_requests_to_apns)

