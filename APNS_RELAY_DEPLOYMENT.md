# APN Relay Deployment Summary

**Deployment Date:** 2026-07-10  
**Status:** ✅ Live and Operational

---

## Deployment Details

### Relay URL
```
https://llama-labels-push-relay-apns.llama-mail.workers.dev
```

### Environment
- **Platform:** Cloudflare Workers
- **Region:** Global (edge locations worldwide)
- **Environment:** Sandbox (Apple APNs sandbox)
- **Bundle ID:** com.urlxl.mail

### Infrastructure
- **Worker Name:** llama-labels-push-relay-apns
- **Version ID:** 88e167a4-e266-4c04-a679-79ab4cb1d1a8
- **KV Namespaces:**
  - `API_KEYS` (4e040d1227014cf595d0c88e1d40c70e) — stores API key records
  - `APNS_TOKEN_CACHE` (0296a96f307d4941816b791bbc462ba0) — caches provider tokens
- **Rate Limit:** 10 requests/minute per API key

### Apple Credentials
- **Key ID:** 5VHY37SF45
- **Team ID:** TATYN4X57D
- **Topic (Bundle ID):** com.urlxl.mail
- **Auth Key (.p8):** Securely stored in Cloudflare secrets

---

## Testing the Relay

### Health Check
```bash
curl https://llama-labels-push-relay-apns.llama-mail.workers.dev/health
```

Expected response:
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

### Create a Test API Key
```bash
curl -X POST https://llama-labels-push-relay-apns.llama-mail.workers.dev/admin/keys \
  -H "Authorization: Bearer YOUR_ADMIN_SECRET" \
  -H "Content-Type: application/json" \
  -d '{"label":"test-ios-client"}'
```

Response will include:
- `id` — unique key identifier
- `key` — the API key to use for `/send` requests
- `label` — the label you provided
- `expiresAt` — expiration time (null = never expires)

### Send a Test Push
```bash
curl -X POST https://llama-labels-push-relay-apns.llama-mail.workers.dev/send \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "token": "device-push-token-from-ios",
    "title": "Test Notification",
    "body": "This is a test push from the APN relay",
    "data": {"action": "test"}
  }'
```

---

## Endpoints

| Method | Endpoint | Auth | Purpose |
|--------|----------|------|---------|
| GET | `/health` | None | Liveness & configuration check |
| POST | `/register` | None | Self-issue an API key (public registration) |
| POST | `/send` | Bearer (API key) | Send a push notification |
| POST | `/admin/keys` | Bearer (admin secret) | Create an API key (admin only) |
| GET | `/admin/keys` | Bearer (admin secret) | List all API keys (admin only) |
| DELETE | `/admin/keys/{id}` | Bearer (admin secret) | Revoke an API key (admin only) |

---

## Next Steps

1. **iOS/macOS App Integration** — Use this relay URL in your mobile app's push registration flow
2. **Production Deployment** — When ready, create a second relay for production APNs environment
3. **Monitoring** — Use Cloudflare Workers dashboard to monitor usage and errors
4. **Backup Keys** — Keep your `.p8` file and credentials in a secure location

---

## Security Notes

- The relay uses ES256 JWT tokens signed with your APNs private key
- Provider tokens are cached for ~29 minutes (refreshed automatically)
- Per-key rate limiting is enforced at 10 requests/minute (adjustable)
- All secrets are stored securely in Cloudflare and never logged
- KV storage is encrypted at rest

---

## Troubleshooting

**If /health shows `configured: false`**
- One or more secrets are missing or invalid
- Check Cloudflare Workers dashboard → Settings → Secrets
- Verify all 5 secrets are set: APNS_AUTH_KEY, APNS_KEY_ID, APNS_TEAM_ID, APNS_TOPIC, ADMIN_SECRET

**If push sends fail with 403 (InvalidToken)**
- Provider token cache may be stale
- Relay will auto-refresh the token on next attempt
- This is normal and recoverable

**Rate limit errors (429)**
- You've exceeded 10 pushes per minute per API key
- Implement exponential backoff in your client
- Contact if you need higher limits

---

## References

- [Apple APNs Documentation](https://developer.apple.com/documentation/usernotifications/setting_up_a_remote_notification_server)
- [Cloudflare Workers Docs](https://developers.cloudflare.com/workers/)
- [APN Relay Source Code](./worker-apns/)
