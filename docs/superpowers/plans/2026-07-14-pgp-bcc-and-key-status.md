# PGP BCC Leak Fix + Key Revocation/Expiry Enforcement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix a PGP/MIME confidentiality leak where BCC recipients' key IDs are exposed in a shared ciphertext, and enforce key revocation/expiry on send.

**Architecture:** A new `pgpmail.KeyStatus`/`CheckKeyStatus`/`Identity.Status` trio (live-computed from gopenpgp's `Key.IsRevoked`/`IsExpired`, no caching) becomes the single source of truth for key usability. In `handleMailSend`, a new pure function `buildPGPRecipientPlan` resolves each recipient's contact key and usability, splitting To/CC (share one ciphertext) from BCC (each gets its own ciphertext via `buildPGPDeliveries`), with revoked/expired/no-key recipients falling back to the existing pickup-link path. A new `smtpDeliver` helper (extracted from `finishMailSend`) lets the main send and each per-BCC send share the same transport code.

**Tech Stack:** Go, `github.com/ProtonMail/gopenpgp/v3` (`v3.4.1`), `github.com/ProtonMail/go-crypto` (`v1.4.1`, currently an indirect dependency — becomes direct via test-only imports).

## Global Constraints

- No schema or storage changes — key status is always recomputed live from already-stored armored key text via `Key.IsRevoked(now)`/`Key.IsExpired(now)`, never cached on `Contact` or `User`.
- Decryption is never gated on key revocation/expiry — a user must still be able to read old mail after their own key lapses. Do not add any check to the receive path (`pgp_receive.go`) or to `DecryptMIME`.
- Receive-path signature verification (`VerifyDetached`, `pgp_receive.go`) is unchanged — this plan only touches send-time signing and recipient-key selection.
- A revoked/expired recipient key is treated exactly like "no key on file" (routes to the plaintext pickup-link notification) — never a hard error.
- A revoked/expired own identity blocks `Sign=true` only; `Encrypt=true` alone is unaffected.
- A failed per-BCC-recipient send is logged and skipped (best-effort), never fails the overall request once the main To/CC send has succeeded.
- No frontend changes.

---

## Task 1: `pgpmail.KeyStatus` — revocation/expiry status helper

**Files:**
- Create: `backend/internal/pgpmail/keystatus.go`
- Create: `backend/internal/pgpmail/keystatus_test.go`

**Interfaces:**
- Produces: `type KeyStatus struct { Revoked, Expired bool }`, `func (s KeyStatus) Usable() bool`, `func CheckKeyStatus(armoredKey string) (KeyStatus, error)`, `func (id *Identity) Status() KeyStatus` — all consumed by Tasks 2, 4, 5, 6.

- [ ] **Step 1: Write the failing tests**

Create `backend/internal/pgpmail/keystatus_test.go`:

```go
package pgpmail

import (
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"github.com/ProtonMail/gopenpgp/v3/crypto"
)

func TestCheckKeyStatusUsableKey(t *testing.T) {
	id, err := GenerateIdentity("Alice", "alice@example.com")
	if err != nil {
		t.Fatalf("GenerateIdentity: %v", err)
	}
	status, err := CheckKeyStatus(id.ArmoredPublicKey)
	if err != nil {
		t.Fatalf("CheckKeyStatus: %v", err)
	}
	if status.Revoked || status.Expired || !status.Usable() {
		t.Fatalf("expected a fresh key to be usable, got %+v", status)
	}
}

func TestCheckKeyStatusExpiredKey(t *testing.T) {
	past := time.Now().Add(-48 * time.Hour)
	key, err := crypto.PGP().KeyGeneration().
		GenerationTime(past.Unix()).
		Lifetime(3600).
		AddUserId("Expired", "expired@example.com").
		New().GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	armored, err := key.GetArmoredPublicKey()
	if err != nil {
		t.Fatalf("GetArmoredPublicKey: %v", err)
	}

	status, err := CheckKeyStatus(armored)
	if err != nil {
		t.Fatalf("CheckKeyStatus: %v", err)
	}
	if !status.Expired || status.Usable() {
		t.Fatalf("expected a key generated 48h ago with a 1h lifetime to be expired and unusable, got %+v", status)
	}
}

func TestCheckKeyStatusRevokedKey(t *testing.T) {
	key, err := crypto.PGP().KeyGeneration().AddUserId("Revoked", "revoked@example.com").New().GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if err := key.GetEntity().Revoke(packet.NoReason, "test revocation", &packet.Config{}); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	armored, err := key.GetArmoredPublicKey()
	if err != nil {
		t.Fatalf("GetArmoredPublicKey: %v", err)
	}

	status, err := CheckKeyStatus(armored)
	if err != nil {
		t.Fatalf("CheckKeyStatus: %v", err)
	}
	if !status.Revoked || status.Usable() {
		t.Fatalf("expected a revoked key to be reported revoked and unusable, got %+v", status)
	}
}

func TestCheckKeyStatusInvalidArmor(t *testing.T) {
	if _, err := CheckKeyStatus("not a real armored key"); err == nil {
		t.Fatal("expected an error parsing invalid armored text")
	}
}

func TestIdentityStatusUsable(t *testing.T) {
	id, err := GenerateIdentity("Alice", "alice@example.com")
	if err != nil {
		t.Fatalf("GenerateIdentity: %v", err)
	}
	if status := id.Status(); !status.Usable() {
		t.Fatalf("expected a fresh identity to be usable, got %+v", status)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd "/home/yoshi/git/llama labels/backend" && go test ./internal/pgpmail/... -run 'TestCheckKeyStatus|TestIdentityStatus' -v`
Expected: FAIL — `CheckKeyStatus`, `KeyStatus`, and `Identity.Status` are undefined.

- [ ] **Step 3: Write the implementation**

Create `backend/internal/pgpmail/keystatus.go`:

```go
package pgpmail

import (
	"fmt"
	"time"

	"github.com/ProtonMail/gopenpgp/v3/crypto"
)

// KeyStatus reports whether an OpenPGP key is currently usable for
// encryption or signing, as of the moment it was computed. It is never
// cached — callers recompute it from the armored key text each time they
// need it, since a key's revocation/expiry status can change between calls.
type KeyStatus struct {
	Revoked bool
	Expired bool
}

// Usable reports whether a key in this status can be used for encryption
// or signing right now. It does not affect decryption — a user must still
// be able to read old mail after their own key is revoked or expires.
func (s KeyStatus) Usable() bool {
	return !s.Revoked && !s.Expired
}

// CheckKeyStatus parses an armored OpenPGP key (public or private) and
// reports its revocation/expiry status as of now.
func CheckKeyStatus(armoredKey string) (KeyStatus, error) {
	key, err := crypto.NewKeyFromArmored(armoredKey)
	if err != nil {
		return KeyStatus{}, fmt.Errorf("pgpmail: parse key: %w", err)
	}
	return keyStatusOf(key), nil
}

func keyStatusOf(key *crypto.Key) KeyStatus {
	now := time.Now().Unix()
	return KeyStatus{
		Revoked: key.IsRevoked(now),
		Expired: key.IsExpired(now),
	}
}

// Status reports id's current revocation/expiry status as of now.
func (id *Identity) Status() KeyStatus {
	return keyStatusOf(id.key)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd "/home/yoshi/git/llama labels/backend" && go test ./internal/pgpmail/... -run 'TestCheckKeyStatus|TestIdentityStatus' -v`
Expected: PASS (all 5 tests)

- [ ] **Step 5: Commit**

```bash
cd "/home/yoshi/git/llama labels/backend" && git add internal/pgpmail/keystatus.go internal/pgpmail/keystatus_test.go && git commit -m "$(cat <<'EOF'
pgpmail: add KeyStatus for live revocation/expiry checks

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: `buildPGPRecipientPlan` — resolve and split recipients by key usability

**Files:**
- Modify: `backend/internal/api/server.go:686-703` (insert after `findContactPGPKey`, before `intersect`)
- Modify: `backend/internal/api/server_mail_pgp_test.go`

**Interfaces:**
- Consumes: `pgpmail.CheckKeyStatus(armoredKey string) (pgpmail.KeyStatus, error)` (Task 1), `findContactPGPKey(store *contacts.Store, email string) (string, bool)` (existing, `server.go:670`).
- Produces: `type pgpRecipientPlan struct { toCCEmails, toCCKeys, bccEmails, bccKeys, withoutKeyEmails []string }`, `func buildPGPRecipientPlan(toList, ccList, bccList []string, contactsStore *contacts.Store) pgpRecipientPlan` — consumed by Task 3 (`buildPGPDeliveries`) and Task 4 (`handleMailSend`).

- [ ] **Step 1: Write the failing tests**

Add to `backend/internal/api/server_mail_pgp_test.go` (add `"time"`, `"github.com/ProtonMail/go-crypto/openpgp/packet"`, and `"github.com/ProtonMail/gopenpgp/v3/crypto"` to the existing import block):

```go
// generateArmoredKeyWithLifetime generates a fresh key with the given
// generation time and lifetime in seconds, returning its armored public
// key. A generation time in the past plus a short lifetime yields a key
// that is already expired as of "now" — used to build expired-key test
// fixtures without a static testdata file.
func generateArmoredKeyWithLifetime(t *testing.T, name, email string, generationTime time.Time, lifetimeSeconds int32) string {
	t.Helper()
	key, err := crypto.PGP().KeyGeneration().
		GenerationTime(generationTime.Unix()).
		Lifetime(lifetimeSeconds).
		AddUserId(name, email).
		New().GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	armored, err := key.GetArmoredPublicKey()
	if err != nil {
		t.Fatalf("GetArmoredPublicKey: %v", err)
	}
	return armored
}

// generateRevokedArmoredKey generates a fresh key and immediately revokes
// it, returning its armored public key with the revocation signature
// attached — as a real revoked key published to a keyserver would have.
func generateRevokedArmoredKey(t *testing.T, name, email string) string {
	t.Helper()
	key, err := crypto.PGP().KeyGeneration().AddUserId(name, email).New().GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if err := key.GetEntity().Revoke(packet.NoReason, "test revocation", &packet.Config{}); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	armored, err := key.GetArmoredPublicKey()
	if err != nil {
		t.Fatalf("GetArmoredPublicKey: %v", err)
	}
	return armored
}

func TestBuildPGPRecipientPlanSplitsToCCFromBCCAndFiltersUnusableKeys(t *testing.T) {
	store, err := contacts.New(t.TempDir())
	if err != nil {
		t.Fatalf("contacts.New: %v", err)
	}

	bobID, err := pgpmail.GenerateIdentity("Bob", "bob@example.com")
	if err != nil {
		t.Fatalf("GenerateIdentity bob: %v", err)
	}
	daveID, err := pgpmail.GenerateIdentity("Dave", "dave@example.com")
	if err != nil {
		t.Fatalf("GenerateIdentity dave: %v", err)
	}
	revokedKey := generateRevokedArmoredKey(t, "Revoked", "revoked@example.com")
	expiredKey := generateArmoredKeyWithLifetime(t, "Expired", "expired@example.com", time.Now().Add(-48*time.Hour), 3600)

	for _, c := range []contacts.Contact{
		{FormattedName: "Bob", Emails: []contacts.ContactValue{{Value: "bob@example.com"}}, PGPKey: bobID.ArmoredPublicKey},
		{FormattedName: "Dave", Emails: []contacts.ContactValue{{Value: "dave@example.com"}}, PGPKey: daveID.ArmoredPublicKey},
		{FormattedName: "Revoked", Emails: []contacts.ContactValue{{Value: "revoked@example.com"}}, PGPKey: revokedKey},
		{FormattedName: "Expired", Emails: []contacts.ContactValue{{Value: "expired@example.com"}}, PGPKey: expiredKey},
	} {
		if _, err := store.Upsert(c); err != nil {
			t.Fatalf("Upsert %s: %v", c.FormattedName, err)
		}
	}

	plan := buildPGPRecipientPlan(
		[]string{"bob@example.com"},
		[]string{"revoked@example.com"},
		[]string{"dave@example.com", "expired@example.com", "nokey@example.com"},
		store,
	)

	if len(plan.toCCEmails) != 1 || plan.toCCEmails[0] != "bob@example.com" || len(plan.toCCKeys) != 1 || plan.toCCKeys[0] != bobID.ArmoredPublicKey {
		t.Fatalf("expected bob alone in toCCEmails with his key, got emails=%v keys=%d", plan.toCCEmails, len(plan.toCCKeys))
	}
	if len(plan.bccEmails) != 1 || plan.bccEmails[0] != "dave@example.com" || len(plan.bccKeys) != 1 || plan.bccKeys[0] != daveID.ArmoredPublicKey {
		t.Fatalf("expected dave alone in bccEmails with his key, got emails=%v keys=%d", plan.bccEmails, len(plan.bccKeys))
	}
	wantWithoutKey := []string{"revoked@example.com", "expired@example.com", "nokey@example.com"}
	if len(plan.withoutKeyEmails) != len(wantWithoutKey) {
		t.Fatalf("withoutKeyEmails mismatch: got %v want %v", plan.withoutKeyEmails, wantWithoutKey)
	}
	for i, want := range wantWithoutKey {
		if plan.withoutKeyEmails[i] != want {
			t.Fatalf("withoutKeyEmails[%d]: got %q want %q (full: %v)", i, plan.withoutKeyEmails[i], want, plan.withoutKeyEmails)
		}
	}
}

func TestBuildPGPRecipientPlanDedupesAcrossToCcBccKeepingFirstOccurrence(t *testing.T) {
	store, err := contacts.New(t.TempDir())
	if err != nil {
		t.Fatalf("contacts.New: %v", err)
	}
	bobID, err := pgpmail.GenerateIdentity("Bob", "bob@example.com")
	if err != nil {
		t.Fatalf("GenerateIdentity: %v", err)
	}
	if _, err := store.Upsert(contacts.Contact{
		FormattedName: "Bob",
		Emails:        []contacts.ContactValue{{Value: "bob@example.com"}},
		PGPKey:        bobID.ArmoredPublicKey,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// bob@example.com appears in both To and BCC (different case) — must be
	// counted once as a To recipient, never duplicated into bccEmails too.
	plan := buildPGPRecipientPlan(
		[]string{"bob@example.com"},
		nil,
		[]string{"Bob@Example.com"},
		store,
	)

	if len(plan.toCCEmails) != 1 || len(plan.bccEmails) != 0 {
		t.Fatalf("expected bob counted once in toCCEmails and not duplicated into bccEmails, got toCC=%v bcc=%v", plan.toCCEmails, plan.bccEmails)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd "/home/yoshi/git/llama labels/backend" && go test ./internal/api/... -run TestBuildPGPRecipientPlan -v`
Expected: FAIL — `buildPGPRecipientPlan` and `pgpRecipientPlan` are undefined.

- [ ] **Step 3: Write the implementation**

In `backend/internal/api/server.go`, insert immediately after `findContactPGPKey` (after line 686, before the `intersect` function at line 688):

```go
// pgpRecipientPlan splits an encrypted send's To/CC/BCC recipients by PGP
// key availability and status. To/CC recipients with a usable key share one
// ciphertext, matching how a normal email is visible to every To/CC
// recipient. BCC recipients are kept separate so each can be encrypted
// individually in buildPGPDeliveries — sharing a ciphertext (and its
// embedded recipient key IDs) with anyone else would deanonymize them.
// Recipients with no key on file, or whose key is revoked or expired, land
// in withoutKeyEmails and fall back to the existing plaintext pickup-link
// notification.
type pgpRecipientPlan struct {
	toCCEmails       []string
	toCCKeys         []string
	bccEmails        []string
	bccKeys          []string
	withoutKeyEmails []string
}

// buildPGPRecipientPlan resolves each recipient's contact PGP key and
// builds a pgpRecipientPlan. Recipients are deduplicated case-insensitively
// across To+CC+BCC combined, keeping only the first occurrence — an address
// listed in both To and BCC is treated as a To recipient.
func buildPGPRecipientPlan(toList, ccList, bccList []string, contactsStore *contacts.Store) pgpRecipientPlan {
	var plan pgpRecipientPlan
	seen := map[string]bool{}

	resolve := func(recipient string) (armoredKey string, usable bool) {
		key, ok := findContactPGPKey(contactsStore, recipient)
		if !ok {
			return "", false
		}
		status, err := pgpmail.CheckKeyStatus(key)
		if err != nil || !status.Usable() {
			return "", false
		}
		return key, true
	}

	toCC := append(append([]string{}, toList...), ccList...)
	for _, recipient := range toCC {
		lower := strings.ToLower(strings.TrimSpace(recipient))
		if lower == "" || seen[lower] {
			continue
		}
		seen[lower] = true
		if key, ok := resolve(recipient); ok {
			plan.toCCEmails = append(plan.toCCEmails, recipient)
			plan.toCCKeys = append(plan.toCCKeys, key)
		} else {
			plan.withoutKeyEmails = append(plan.withoutKeyEmails, recipient)
		}
	}
	for _, recipient := range bccList {
		lower := strings.ToLower(strings.TrimSpace(recipient))
		if lower == "" || seen[lower] {
			continue
		}
		seen[lower] = true
		if key, ok := resolve(recipient); ok {
			plan.bccEmails = append(plan.bccEmails, recipient)
			plan.bccKeys = append(plan.bccKeys, key)
		} else {
			plan.withoutKeyEmails = append(plan.withoutKeyEmails, recipient)
		}
	}
	return plan
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd "/home/yoshi/git/llama labels/backend" && go test ./internal/api/... -run TestBuildPGPRecipientPlan -v`
Expected: PASS (both tests)

- [ ] **Step 5: Run the full package test suite to confirm no regressions**

Run: `cd "/home/yoshi/git/llama labels/backend" && go build ./... && go test ./internal/api/... ./internal/pgpmail/...`
Expected: all packages `ok` (nothing else was touched yet, so `buildEncryptedSendArgs`/`intersect` and their tests still exist and still pass unchanged)

- [ ] **Step 6: Commit**

```bash
cd "/home/yoshi/git/llama labels/backend" && git add internal/api/server.go internal/api/server_mail_pgp_test.go && git commit -m "$(cat <<'EOF'
api: add buildPGPRecipientPlan to split To/CC from BCC by key status

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: `buildPGPDeliveries` + `smtpDeliver` — per-recipient-group encryption and shared transport

**Files:**
- Modify: `backend/internal/api/server.go` (insert after `buildPGPRecipientPlan`; extract SMTP-send block from `finishMailSend`)
- Modify: `backend/internal/api/server_mail_pgp_test.go`

**Interfaces:**
- Consumes: `pgpRecipientPlan` (Task 2), `pgpmail.EncryptMIME(plaintext []byte, recipientArmoredPubKeys []string, signer *pgpmail.Identity) ([]byte, error)` (existing, `pgpmail/mime.go:70`).
- Produces: `type pgpDelivery struct { Recipients []string; Ciphertext []byte }`, `func buildPGPDeliveries(msg []byte, plan pgpRecipientPlan, signer *pgpmail.Identity) ([]pgpDelivery, error)`, `func smtpDeliver(smtpHost string, smtpPort int, addr, smtpUsername, smtpPassword, from string, recipients []string, msg []byte) error` — both consumed by Task 4.

- [ ] **Step 1: Write the failing tests**

Add to `backend/internal/api/server_mail_pgp_test.go` (add `"io"`, `"mime"`, `"mime/multipart"`, `"net/mail"`, and `"llama-lab/backend/internal/mailmsg"` to the existing import block; `bytes` and `strings` are already imported by the package):

```go
// extractArmoredPGPData is a test-only MIME walker that extracts the
// armored PGP data part from an EncryptMIME envelope. Mirrors pgpmail's own
// (unexported) extractOctetStreamPart test helper — duplicated here since
// api and pgpmail are separate test packages.
func extractArmoredPGPData(t *testing.T, raw []byte) string {
	t.Helper()
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("mail.ReadMessage: %v", err)
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		t.Fatalf("expected multipart Content-Type, got %q (err=%v)", mediaType, err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			t.Fatal("no application/octet-stream part found")
		}
		if err != nil {
			t.Fatalf("NextPart: %v", err)
		}
		if strings.HasPrefix(part.Header.Get("Content-Type"), "application/octet-stream") {
			data, err := io.ReadAll(part)
			if err != nil {
				t.Fatalf("ReadAll part: %v", err)
			}
			return string(data)
		}
	}
}

// TestBuildPGPDeliveriesIsolatesBCCRecipients is the core regression test
// for the BCC key-ID leak: before this change, To/CC/BCC keys were all
// merged into one shared ciphertext, so any recipient could inspect the
// message's OpenPGP packets and see which BCC'd keys it was also encrypted
// to. This asserts the stronger guarantee buildPGPDeliveries actually
// implements — each BCC recipient gets a wholly separate ciphertext that
// nobody else (not the To/CC recipients, not other BCC recipients) can
// decrypt at all.
func TestBuildPGPDeliveriesIsolatesBCCRecipients(t *testing.T) {
	bob, err := pgpmail.GenerateIdentity("Bob", "bob@example.com")
	if err != nil {
		t.Fatalf("GenerateIdentity bob: %v", err)
	}
	carol, err := pgpmail.GenerateIdentity("Carol", "carol@example.com")
	if err != nil {
		t.Fatalf("GenerateIdentity carol: %v", err)
	}
	dave, err := pgpmail.GenerateIdentity("Dave", "dave@example.com")
	if err != nil {
		t.Fatalf("GenerateIdentity dave: %v", err)
	}
	eve, err := pgpmail.GenerateIdentity("Eve", "eve@example.com")
	if err != nil {
		t.Fatalf("GenerateIdentity eve: %v", err)
	}

	plan := pgpRecipientPlan{
		toCCEmails: []string{"bob@example.com", "carol@example.com"},
		toCCKeys:   []string{bob.ArmoredPublicKey, carol.ArmoredPublicKey},
		bccEmails:  []string{"dave@example.com", "eve@example.com"},
		bccKeys:    []string{dave.ArmoredPublicKey, eve.ArmoredPublicKey},
	}
	plaintext := mailmsg.Message{
		From:    "alice@example.com",
		To:      []string{"bob@example.com", "carol@example.com"},
		Subject: "Secret",
		Body:    "meet at dawn",
		Mode:    "plain",
	}.Build()

	deliveries, err := buildPGPDeliveries(plaintext, plan, nil)
	if err != nil {
		t.Fatalf("buildPGPDeliveries: %v", err)
	}
	if len(deliveries) != 3 {
		t.Fatalf("expected 3 deliveries (1 shared to/cc + 2 individual bcc), got %d", len(deliveries))
	}

	shared := deliveries[0]
	if len(shared.Recipients) != 2 || shared.Recipients[0] != "bob@example.com" || shared.Recipients[1] != "carol@example.com" {
		t.Fatalf("expected shared delivery to bob+carol, got %v", shared.Recipients)
	}
	sharedArmored := extractArmoredPGPData(t, shared.Ciphertext)
	if _, err := pgpmail.DecryptMIME(sharedArmored, bob, nil); err != nil {
		t.Fatalf("bob should decrypt the shared to/cc ciphertext: %v", err)
	}
	if _, err := pgpmail.DecryptMIME(sharedArmored, carol, nil); err != nil {
		t.Fatalf("carol should decrypt the shared to/cc ciphertext: %v", err)
	}
	if _, err := pgpmail.DecryptMIME(sharedArmored, dave, nil); err == nil {
		t.Fatal("dave (bcc) must not be able to decrypt the shared to/cc ciphertext")
	}

	daveDelivery, eveDelivery := deliveries[1], deliveries[2]
	if len(daveDelivery.Recipients) != 1 || daveDelivery.Recipients[0] != "dave@example.com" {
		t.Fatalf("expected dave's own delivery, got %v", daveDelivery.Recipients)
	}
	if len(eveDelivery.Recipients) != 1 || eveDelivery.Recipients[0] != "eve@example.com" {
		t.Fatalf("expected eve's own delivery, got %v", eveDelivery.Recipients)
	}

	daveArmored := extractArmoredPGPData(t, daveDelivery.Ciphertext)
	if _, err := pgpmail.DecryptMIME(daveArmored, dave, nil); err != nil {
		t.Fatalf("dave should decrypt his own bcc ciphertext: %v", err)
	}
	if _, err := pgpmail.DecryptMIME(daveArmored, eve, nil); err == nil {
		t.Fatal("eve must not be able to decrypt dave's bcc ciphertext")
	}
	if _, err := pgpmail.DecryptMIME(daveArmored, bob, nil); err == nil {
		t.Fatal("bob (to/cc recipient) must not be able to decrypt dave's bcc ciphertext")
	}

	eveArmored := extractArmoredPGPData(t, eveDelivery.Ciphertext)
	if _, err := pgpmail.DecryptMIME(eveArmored, eve, nil); err != nil {
		t.Fatalf("eve should decrypt her own bcc ciphertext: %v", err)
	}
	if _, err := pgpmail.DecryptMIME(eveArmored, dave, nil); err == nil {
		t.Fatal("dave must not be able to decrypt eve's bcc ciphertext")
	}
}

func TestBuildPGPDeliveriesBCCOnlyWhenNoToCCHasUsableKey(t *testing.T) {
	dave, err := pgpmail.GenerateIdentity("Dave", "dave@example.com")
	if err != nil {
		t.Fatalf("GenerateIdentity dave: %v", err)
	}
	plan := pgpRecipientPlan{
		bccEmails: []string{"dave@example.com"},
		bccKeys:   []string{dave.ArmoredPublicKey},
	}
	plaintext := mailmsg.Message{
		From:    "alice@example.com",
		To:      []string{"nokey@example.com"},
		Subject: "Secret",
		Body:    "meet at dawn",
		Mode:    "plain",
	}.Build()

	deliveries, err := buildPGPDeliveries(plaintext, plan, nil)
	if err != nil {
		t.Fatalf("buildPGPDeliveries: %v", err)
	}
	if len(deliveries) != 1 || len(deliveries[0].Recipients) != 1 || deliveries[0].Recipients[0] != "dave@example.com" {
		t.Fatalf("expected exactly one bcc-only delivery to dave, got %+v", deliveries)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd "/home/yoshi/git/llama labels/backend" && go test ./internal/api/... -run TestBuildPGPDeliveries -v`
Expected: FAIL — `buildPGPDeliveries` and `pgpDelivery` are undefined.

- [ ] **Step 3: Write the implementation**

In `backend/internal/api/server.go`, insert immediately after `buildPGPRecipientPlan` (the function added in Task 2):

```go
// pgpDelivery is one PGP/MIME ciphertext and the SMTP recipient(s) it
// should be delivered to in a single transaction.
type pgpDelivery struct {
	Recipients []string
	Ciphertext []byte
}

// buildPGPDeliveries encrypts msg once for plan's shared To/CC recipients
// (if any) and once individually for each of plan's BCC recipients, so no
// BCC recipient's key ID ever appears in a ciphertext another recipient can
// inspect. signer is passed straight through to EncryptMIME for every
// delivery (nil if the caller didn't request signing).
func buildPGPDeliveries(msg []byte, plan pgpRecipientPlan, signer *pgpmail.Identity) ([]pgpDelivery, error) {
	var deliveries []pgpDelivery
	if len(plan.toCCEmails) > 0 {
		ciphertext, err := pgpmail.EncryptMIME(msg, plan.toCCKeys, signer)
		if err != nil {
			return nil, fmt.Errorf("encrypt to/cc recipients: %w", err)
		}
		deliveries = append(deliveries, pgpDelivery{Recipients: plan.toCCEmails, Ciphertext: ciphertext})
	}
	for i, recipient := range plan.bccEmails {
		ciphertext, err := pgpmail.EncryptMIME(msg, []string{plan.bccKeys[i]}, signer)
		if err != nil {
			return nil, fmt.Errorf("encrypt bcc recipient %s: %w", recipient, err)
		}
		deliveries = append(deliveries, pgpDelivery{Recipients: []string{recipient}, Ciphertext: ciphertext})
	}
	return deliveries, nil
}

// smtpDeliver sends msg over SMTP to recipients, choosing implicit TLS
// (port 465) or STARTTLS/plain auth otherwise. Extracted from
// finishMailSend so per-BCC-recipient encrypted sends (handleMailSend) can
// reuse the same transport logic for their own separate SMTP transactions.
func smtpDeliver(smtpHost string, smtpPort int, addr, smtpUsername, smtpPassword, from string, recipients []string, msg []byte) error {
	if smtpPort == 465 {
		return smtpSendWithImplicitTLS(smtpHost, smtpPort, smtpUsername, smtpPassword, from, recipients, msg, 45*time.Second)
	}
	auth := smtp.PlainAuth("", smtpUsername, smtpPassword, smtpHost)
	return smtpSendWithTimeout(addr, auth, from, recipients, msg, 45*time.Second)
}
```

Then, in `finishMailSend`, replace this block:

```go
	var sendErr error
	if smtpPort == 465 {
		sendErr = smtpSendWithImplicitTLS(smtpHost, smtpPort, smtpUsername, smtpPassword, from, recipients, msg, 45*time.Second)
	} else {
		auth := smtp.PlainAuth("", smtpUsername, smtpPassword, smtpHost)
		sendErr = smtpSendWithTimeout(addr, auth, from, recipients, msg, 45*time.Second)
	}
	if sendErr != nil {
		s.logger.Error("mail send failed", "smtpHost", smtpHost, "smtpPort", strconv.Itoa(smtpPort), "error", sendErr.Error())
		http.Error(w, fmt.Sprintf("failed to send email: %s", sendErr.Error()), http.StatusBadGateway)
		return false
	}
```

with:

```go
	if sendErr := smtpDeliver(smtpHost, smtpPort, addr, smtpUsername, smtpPassword, from, recipients, msg); sendErr != nil {
		s.logger.Error("mail send failed", "smtpHost", smtpHost, "smtpPort", strconv.Itoa(smtpPort), "error", sendErr.Error())
		http.Error(w, fmt.Sprintf("failed to send email: %s", sendErr.Error()), http.StatusBadGateway)
		return false
	}
```

This is a pure extraction — behavior is unchanged for every existing caller.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd "/home/yoshi/git/llama labels/backend" && go test ./internal/api/... -run TestBuildPGPDeliveries -v`
Expected: PASS (both tests)

- [ ] **Step 5: Run the full package test suite to confirm no regressions**

Run: `cd "/home/yoshi/git/llama labels/backend" && go build ./... && go test ./internal/api/... ./internal/pgpmail/...`
Expected: all packages `ok`

- [ ] **Step 6: Commit**

```bash
cd "/home/yoshi/git/llama labels/backend" && git add internal/api/server.go internal/api/server_mail_pgp_test.go && git commit -m "$(cat <<'EOF'
api: add buildPGPDeliveries and extract smtpDeliver transport helper

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Wire `handleMailSend` to the new plan/deliveries, enforce own-identity signing status

**Files:**
- Modify: `backend/internal/api/server.go` (handleMailSend, finishMailSend; delete `buildEncryptedSendArgs` and `intersect`)
- Modify: `backend/internal/api/server_mail_pgp_test.go` (delete tests for the removed functions)

**Interfaces:**
- Consumes: `buildPGPRecipientPlan` (Task 2), `buildPGPDeliveries`, `smtpDeliver` (Task 3), `signer.Status()` (Task 1), existing `encryptSigner(signer *pgpmail.Identity, sign bool) *pgpmail.Identity` (`server.go:845`), existing `s.finishMailSend(...) bool` (`server.go:872`), existing `s.sendPickupNotification(...)`.
- Produces: nothing new — this task only rewires existing entry points.

- [ ] **Step 1: Delete the tests for the functions this task removes**

In `backend/internal/api/server_mail_pgp_test.go`, delete `TestBuildEncryptedSendArgsKeepsFullRecipientsInSentFolder` and `TestIntersectPreservesOrderAndIsCaseInsensitive` in full (they test `buildEncryptedSendArgs`/`intersect`, which this step removes from `server.go`).

- [ ] **Step 2: Write the failing test for the own-identity signing block**

Add to `backend/internal/api/server_mail_pgp_test.go`:

```go
// TestMailSendBlocksSigningWithRevokedIdentity proves the own-identity
// enforcement added in this task: Sign=true must be rejected before any
// network I/O when the sender's own PGP identity is revoked. IMAP/SMTP
// config is written directly (bypassing the network) so the handler gets
// past its precondition checks and reaches the signer-status check; a 400
// response (rather than a 502 from a real send attempt) proves the request
// never reached the network.
func TestMailSendBlocksSigningWithRevokedIdentity(t *testing.T) {
	srv := newTestServer(t)
	all, _ := srv.users.List()
	userID := all[0].ID

	if err := writeIMAPConfigPayload(srv.userIMAPConfigPath(userID), srv.imapConfigKeyPath, imapConfigPayload{
		Host:     "imap.example.com",
		Port:     993,
		Username: "alice@example.com",
		Password: "pw",
		Mailbox:  "INBOX",
		SMTPHost: "smtp.example.com",
		SMTPPort: 587,
	}); err != nil {
		t.Fatalf("writeIMAPConfigPayload: %v", err)
	}

	key, err := crypto.PGP().KeyGeneration().AddUserId("Alice", "alice@example.com").New().GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if err := key.GetEntity().Revoke(packet.NoReason, "test revocation", &packet.Config{}); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	armored, err := key.Armor()
	if err != nil {
		t.Fatalf("Armor: %v", err)
	}
	importBody, _ := json.Marshal(map[string]string{"armoredPrivateKey": armored})
	importReq := httptest.NewRequest(http.MethodPost, "/api/pgp/identity/import", bytes.NewReader(importBody))
	authRequest(srv, importReq)
	importRec := httptest.NewRecorder()
	srv.withAuth(srv.handlePGPIdentityImport)(importRec, importReq)
	if importRec.Code != http.StatusOK {
		t.Fatalf("import: expected 200, got %d: %s", importRec.Code, importRec.Body.String())
	}

	sendBody, _ := json.Marshal(map[string]any{
		"to":      "bob@example.com",
		"subject": "hi",
		"body":    "hello",
		"sign":    true,
	})
	sendReq := httptest.NewRequest(http.MethodPost, "/api/mail/send", bytes.NewReader(sendBody))
	authRequest(srv, sendReq)
	sendRec := httptest.NewRecorder()
	srv.withAuth(srv.handleMailSend)(sendRec, sendReq)

	if sendRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for signing with a revoked identity, got %d: %s", sendRec.Code, sendRec.Body.String())
	}
}
```

Add `"encoding/json"` is already imported; add `"github.com/ProtonMail/go-crypto/openpgp/packet"` and `"github.com/ProtonMail/gopenpgp/v3/crypto"` if not already present from Task 2's helpers (they are — Task 2 already added both to this file's import block).

- [ ] **Step 3: Run the full test suite to confirm the deletions are clean and the new test fails for the right reason**

Run: `cd "/home/yoshi/git/llama labels/backend" && go build ./... && go test ./internal/api/... ./internal/pgpmail/...`
Expected: `TestMailSendBlocksSigningWithRevokedIdentity` FAILS (got a non-400 status — the signer-status check doesn't exist yet, so the handler proceeds to `SignMIME` and then attempts a real SMTP send to `smtp.example.com:587`, which will fail to connect and surface as a 502, or time out — either way, not the 400 this test requires). All other tests still `ok` (the two deleted tests are gone; `buildEncryptedSendArgs`/`intersect` in `server.go` are still defined but now unused by tests — the build still succeeds since Go only errors on unused *local* variables/imports, not unused package-level functions).

If the failing test hangs instead of failing quickly (a real connection attempt to `smtp.example.com` may take a while to time out), that itself confirms the check is missing — cancel it and proceed to Step 4; it will run quickly once Step 4's fix is in place, since the 400 now returns before any network call.

- [ ] **Step 4: Replace the signer-loading and encrypted-send logic in `handleMailSend`**

In `backend/internal/api/server.go`, replace this entire block (from the `var signer *pgpmail.Identity` line through the end of `handleMailSend`):

```go
	var signer *pgpmail.Identity
	if req.Sign || req.Encrypt {
		u, uerr := s.users.Get(ac.UserID)
		if uerr == nil && u.PGPPrivateKeyEnc != "" {
			signer, err = pgpmail.OpenPrivateKey(u.PGPPrivateKeyEnc, s.pgpPrivateKeyPath)
			if err != nil {
				http.Error(w, "failed to load pgp identity", http.StatusInternalServerError)
				return
			}
		} else if req.Sign {
			http.Error(w, "signing requires a pgp identity — generate or import one first", http.StatusBadRequest)
			return
		}
	}

	if !req.Encrypt {
		if req.Sign {
			signed, serr := pgpmail.SignMIME(msg, signer)
			if serr != nil {
				http.Error(w, "failed to sign message", http.StatusInternalServerError)
				return
			}
			msg = signed
		}
		recipients := append(append(append([]string{}, toList...), ccList...), bccList...)
		s.finishMailSend(w, r, ac.UserID, smtpHost, smtpPort, addr, payload.Username, payload.Password, from, toList, ccList, bccList, recipients, msg, req)
		return
	}

	contactsStore, cerr := s.userContactsStore(ac.UserID)
	if cerr != nil {
		http.Error(w, "failed to open contacts store", http.StatusInternalServerError)
		return
	}
	allRecipients := append(append(append([]string{}, toList...), ccList...), bccList...)
	seen := map[string]bool{}
	var withKeyEmails, withoutKeyEmails, recipientKeys []string
	for _, recipient := range allRecipients {
		lower := strings.ToLower(strings.TrimSpace(recipient))
		if lower == "" || seen[lower] {
			continue
		}
		seen[lower] = true
		if key, ok := findContactPGPKey(contactsStore, recipient); ok {
			withKeyEmails = append(withKeyEmails, recipient)
			recipientKeys = append(recipientKeys, key)
		} else {
			withoutKeyEmails = append(withoutKeyEmails, recipient)
		}
	}
	if len(withKeyEmails) == 0 {
		http.Error(w, "none of the recipients have a known pgp key — disable encryption or add keys to your contacts first", http.StatusBadRequest)
		return
	}

	encrypted, eerr := pgpmail.EncryptMIME(msg, recipientKeys, encryptSigner(signer, req.Sign))
	if eerr != nil {
		http.Error(w, "failed to encrypt message", http.StatusInternalServerError)
		return
	}
	draftTo, draftCC, draftBCC, encRecipients := buildEncryptedSendArgs(toList, ccList, bccList, withKeyEmails)

	if !s.finishMailSend(w, r, ac.UserID, smtpHost, smtpPort, addr, payload.Username, payload.Password, from, draftTo, draftCC, draftBCC, encRecipients, encrypted, req) {
		return
	}

	for _, recipient := range withoutKeyEmails {
		if err := s.sendPickupNotification(ac.UserID, from, recipient, req.Subject, req.Body, smtpHost, smtpPort, addr, payload.Username, payload.Password); err != nil {
			s.logger.Error("pickup notification send failed", "recipient", recipient, "error", err.Error())
		}
	}
}
```

with:

```go
	var signer *pgpmail.Identity
	if req.Sign || req.Encrypt {
		u, uerr := s.users.Get(ac.UserID)
		if uerr == nil && u.PGPPrivateKeyEnc != "" {
			signer, err = pgpmail.OpenPrivateKey(u.PGPPrivateKeyEnc, s.pgpPrivateKeyPath)
			if err != nil {
				http.Error(w, "failed to load pgp identity", http.StatusInternalServerError)
				return
			}
		} else if req.Sign {
			http.Error(w, "signing requires a pgp identity — generate or import one first", http.StatusBadRequest)
			return
		}
	}
	if req.Sign && signer != nil {
		if status := signer.Status(); !status.Usable() {
			http.Error(w, "cannot sign — your pgp identity is revoked or expired, generate or import a new one", http.StatusBadRequest)
			return
		}
	}

	if !req.Encrypt {
		if req.Sign {
			signed, serr := pgpmail.SignMIME(msg, signer)
			if serr != nil {
				http.Error(w, "failed to sign message", http.StatusInternalServerError)
				return
			}
			msg = signed
		}
		recipients := append(append(append([]string{}, toList...), ccList...), bccList...)
		s.finishMailSend(w, r, ac.UserID, smtpHost, smtpPort, addr, payload.Username, payload.Password, from, toList, ccList, bccList, recipients, msg, req)
		return
	}

	contactsStore, cerr := s.userContactsStore(ac.UserID)
	if cerr != nil {
		http.Error(w, "failed to open contacts store", http.StatusInternalServerError)
		return
	}
	plan := buildPGPRecipientPlan(toList, ccList, bccList, contactsStore)
	if len(plan.toCCEmails) == 0 && len(plan.bccEmails) == 0 {
		http.Error(w, "none of the recipients have a known pgp key — disable encryption or add keys to your contacts first", http.StatusBadRequest)
		return
	}

	deliveries, eerr := buildPGPDeliveries(msg, plan, encryptSigner(signer, req.Sign))
	if eerr != nil {
		http.Error(w, "failed to encrypt message", http.StatusInternalServerError)
		return
	}

	var mainRecipients []string
	var mainCiphertext []byte
	bccDeliveries := deliveries
	if len(plan.toCCEmails) > 0 {
		mainRecipients, mainCiphertext = deliveries[0].Recipients, deliveries[0].Ciphertext
		bccDeliveries = deliveries[1:]
	}

	if !s.finishMailSend(w, r, ac.UserID, smtpHost, smtpPort, addr, payload.Username, payload.Password, from, toList, ccList, bccList, mainRecipients, mainCiphertext, req) {
		return
	}

	for _, delivery := range bccDeliveries {
		if err := smtpDeliver(smtpHost, smtpPort, addr, payload.Username, payload.Password, from, delivery.Recipients, delivery.Ciphertext); err != nil {
			s.logger.Error("bcc pgp send failed", "recipient", delivery.Recipients[0], "error", err.Error())
		}
	}

	for _, recipient := range plan.withoutKeyEmails {
		if err := s.sendPickupNotification(ac.UserID, from, recipient, req.Subject, req.Body, smtpHost, smtpPort, addr, payload.Username, payload.Password); err != nil {
			s.logger.Error("pickup notification send failed", "recipient", recipient, "error", err.Error())
		}
	}
}
```

- [ ] **Step 5: Guard `finishMailSend` against an empty recipient list**

`mainRecipients`/`mainCiphertext` are empty when every usable key belongs to a BCC recipient (no To/CC recipient has one) — `finishMailSend` must skip the SMTP transaction in that case rather than attempting one with zero recipients, while still saving the Sent-folder copy and writing the response. In `backend/internal/api/server.go`, in `finishMailSend`, replace:

```go
	if sendErr := smtpDeliver(smtpHost, smtpPort, addr, smtpUsername, smtpPassword, from, recipients, msg); sendErr != nil {
		s.logger.Error("mail send failed", "smtpHost", smtpHost, "smtpPort", strconv.Itoa(smtpPort), "error", sendErr.Error())
		http.Error(w, fmt.Sprintf("failed to send email: %s", sendErr.Error()), http.StatusBadGateway)
		return false
	}
```

with:

```go
	if len(recipients) > 0 {
		if sendErr := smtpDeliver(smtpHost, smtpPort, addr, smtpUsername, smtpPassword, from, recipients, msg); sendErr != nil {
			s.logger.Error("mail send failed", "smtpHost", smtpHost, "smtpPort", strconv.Itoa(smtpPort), "error", sendErr.Error())
			http.Error(w, fmt.Sprintf("failed to send email: %s", sendErr.Error()), http.StatusBadGateway)
			return false
		}
	}
```

- [ ] **Step 6: Delete the now-unused `buildEncryptedSendArgs` and `intersect`**

In `backend/internal/api/server.go`, delete the `intersect` function (immediately after `findContactPGPKey`, before `pgpRecipientPlan`):

```go
// intersect returns the elements of addrs that case-insensitively appear in
// allowed, preserving addrs' order.
func intersect(addrs, allowed []string) []string {
	allowedSet := map[string]bool{}
	for _, a := range allowed {
		allowedSet[strings.ToLower(strings.TrimSpace(a))] = true
	}
	var out []string
	for _, a := range addrs {
		if allowedSet[strings.ToLower(strings.TrimSpace(a))] {
			out = append(out, a)
		}
	}
	return out
}
```

and delete `buildEncryptedSendArgs` in full, including its doc comment:

```go
// buildEncryptedSendArgs computes the two distinct recipient views needed by
// finishMailSend's encrypted-send call: the Sent-folder record (draftTo/CC/BCC)
// must list every original recipient, since without-key recipients still
// receive something (a plaintext pickup-link notification, sent separately
// by the caller) even though they're excluded from the encrypted SMTP
// envelope. smtpRecipients is restricted to withKeyEmails because the
// encrypted bytes must never be transmitted to a recipient without a key.
func buildEncryptedSendArgs(toList, ccList, bccList, withKeyEmails []string) (draftTo, draftCC, draftBCC, smtpRecipients []string) {
	encTo := intersect(toList, withKeyEmails)
	encCC := intersect(ccList, withKeyEmails)
	encBCC := intersect(bccList, withKeyEmails)
	smtpRecipients = append(append(append([]string{}, encTo...), encCC...), encBCC...)
	return toList, ccList, bccList, smtpRecipients
}
```

- [ ] **Step 7: Run the full test suite**

Run: `cd "/home/yoshi/git/llama labels/backend" && go build ./... && go vet ./... && go test ./...`
Expected: all packages `ok`, including `TestMailSendBlocksSigningWithRevokedIdentity` now passing, no vet warnings, no unused-function compile errors (both deleted functions have no remaining callers)

- [ ] **Step 8: Commit**

```bash
cd "/home/yoshi/git/llama labels/backend" && git add internal/api/server.go internal/api/server_mail_pgp_test.go && git commit -m "$(cat <<'EOF'
api: split BCC into per-recipient PGP ciphertexts, enforce own-key signing status

Fixes a key-ID leak where BCC recipients shared a ciphertext (and its
embedded recipient key IDs) with To/CC recipients. BCC recipients now
each get their own separately-encrypted, separately-delivered copy.
Also blocks Sign=true sends when the sender's own PGP identity is
revoked or expired.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: Surface revoked/expired status on `GET /api/pgp/identity`

**Files:**
- Modify: `backend/internal/api/pgp_handlers.go`
- Modify: `backend/internal/api/pgp_handlers_test.go`

**Interfaces:**
- Consumes: `pgpmail.CheckKeyStatus` (Task 1), `id.Status()` (Task 1).
- Produces: `pgpIdentityResponse` gains `Revoked`, `Expired bool` fields (`json:"revoked"`, `json:"expired"`).

- [ ] **Step 1: Write the failing test**

Add to `backend/internal/api/pgp_handlers_test.go` (add `"github.com/ProtonMail/go-crypto/openpgp/packet"` to the existing import block):

```go
func TestPGPIdentityImportRevokedKeyReportsRevoked(t *testing.T) {
	srv := newTestServer(t)

	key, err := crypto.PGP().KeyGeneration().AddUserId("Revoked", "revoked@example.com").New().GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if err := key.GetEntity().Revoke(packet.NoReason, "test revocation", &packet.Config{}); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	armored, err := key.Armor()
	if err != nil {
		t.Fatalf("Armor: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"armoredPrivateKey": armored})
	req := httptest.NewRequest(http.MethodPost, "/api/pgp/identity/import", bytes.NewReader(body))
	authRequest(srv, req)
	rec := httptest.NewRecorder()
	srv.withAuth(srv.handlePGPIdentityImport)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("import: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var importResp pgpIdentityResponse
	if err := json.NewDecoder(rec.Body).Decode(&importResp); err != nil {
		t.Fatalf("decode import response: %v", err)
	}
	if !importResp.Revoked {
		t.Fatalf("expected revoked=true on import response, got %+v", importResp)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/pgp/identity", nil)
	authRequest(srv, getReq)
	getRec := httptest.NewRecorder()
	srv.withAuth(srv.handlePGPIdentity)(getRec, getReq)
	var getResp pgpIdentityResponse
	if err := json.NewDecoder(getRec.Body).Decode(&getResp); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if !getResp.Revoked {
		t.Fatalf("expected revoked=true on GET response, got %+v", getResp)
	}
}
```

Also update the existing `TestPGPIdentityGenerateThenGetThenDelete` test: after the `if genResp.Fingerprint == "" ...` check, add:

```go
	if genResp.Revoked || genResp.Expired {
		t.Fatalf("expected a freshly generated identity to be neither revoked nor expired, got %+v", genResp)
	}
```

- [ ] **Step 2: Run tests to verify the new test fails**

Run: `cd "/home/yoshi/git/llama labels/backend" && go test ./internal/api/... -run TestPGPIdentity -v`
Expected: FAIL — `pgpIdentityResponse` has no field `Revoked`/`Expired` (compile error)

- [ ] **Step 3: Write the implementation**

In `backend/internal/api/pgp_handlers.go`, replace:

```go
type pgpIdentityResponse struct {
	Fingerprint string `json:"fingerprint"`
	KeyID       string `json:"keyId"`
	PublicKey   string `json:"publicKey"`
	Source      string `json:"source"`
	CreatedAt   string `json:"createdAt"`
}
```

with:

```go
type pgpIdentityResponse struct {
	Fingerprint string `json:"fingerprint"`
	KeyID       string `json:"keyId"`
	PublicKey   string `json:"publicKey"`
	Source      string `json:"source"`
	CreatedAt   string `json:"createdAt"`
	Revoked     bool   `json:"revoked"`
	Expired     bool   `json:"expired"`
}
```

Replace the body of `storePGPIdentity`:

```go
	createdAt := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.users.SetPGPIdentity(userID, id.Fingerprint, id.KeyID, id.ArmoredPublicKey, sealed, source, createdAt); err != nil {
		http.Error(w, "failed to store pgp identity", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, pgpIdentityResponse{
		Fingerprint: id.Fingerprint,
		KeyID:       id.KeyID,
		PublicKey:   id.ArmoredPublicKey,
		Source:      source,
		CreatedAt:   createdAt,
	})
```

with:

```go
	createdAt := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.users.SetPGPIdentity(userID, id.Fingerprint, id.KeyID, id.ArmoredPublicKey, sealed, source, createdAt); err != nil {
		http.Error(w, "failed to store pgp identity", http.StatusInternalServerError)
		return
	}
	status := id.Status()
	writeJSON(w, http.StatusOK, pgpIdentityResponse{
		Fingerprint: id.Fingerprint,
		KeyID:       id.KeyID,
		PublicKey:   id.ArmoredPublicKey,
		Source:      source,
		CreatedAt:   createdAt,
		Revoked:     status.Revoked,
		Expired:     status.Expired,
	})
```

Replace the `GET` case in `handlePGPIdentity`:

```go
	case http.MethodGet:
		u, err := s.users.Get(ac.UserID)
		if err != nil {
			http.Error(w, "failed to load user", http.StatusInternalServerError)
			return
		}
		if u.PGPFingerprint == "" {
			http.Error(w, "no pgp identity configured", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, pgpIdentityResponse{
			Fingerprint: u.PGPFingerprint,
			KeyID:       u.PGPKeyID,
			PublicKey:   u.PGPPublicKey,
			Source:      u.PGPKeySource,
			CreatedAt:   u.PGPKeyCreatedAt,
		})
```

with:

```go
	case http.MethodGet:
		u, err := s.users.Get(ac.UserID)
		if err != nil {
			http.Error(w, "failed to load user", http.StatusInternalServerError)
			return
		}
		if u.PGPFingerprint == "" {
			http.Error(w, "no pgp identity configured", http.StatusNotFound)
			return
		}
		status, _ := pgpmail.CheckKeyStatus(u.PGPPublicKey)
		writeJSON(w, http.StatusOK, pgpIdentityResponse{
			Fingerprint: u.PGPFingerprint,
			KeyID:       u.PGPKeyID,
			PublicKey:   u.PGPPublicKey,
			Source:      u.PGPKeySource,
			CreatedAt:   u.PGPKeyCreatedAt,
			Revoked:     status.Revoked,
			Expired:     status.Expired,
		})
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd "/home/yoshi/git/llama labels/backend" && go test ./internal/api/... -run TestPGPIdentity -v`
Expected: PASS (all `TestPGPIdentity*` tests)

- [ ] **Step 5: Commit**

```bash
cd "/home/yoshi/git/llama labels/backend" && git add internal/api/pgp_handlers.go internal/api/pgp_handlers_test.go && git commit -m "$(cat <<'EOF'
api: surface revoked/expired status on GET /api/pgp/identity

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Surface revoked/expired status on keyserver lookup and recipients-check

**Files:**
- Modify: `backend/internal/api/pgp_keyserver.go`
- Modify: `backend/internal/api/pgp_keyserver_test.go`

**Interfaces:**
- Consumes: `pgpmail.CheckKeyStatus` (Task 1), `generateRevokedArmoredKey` (Task 2, `server_mail_pgp_test.go`, same package).
- Produces: `handlePGPKeyserverLookup`'s JSON response gains `revoked`/`expired`; `addressStatus` (in `handlePGPRecipientsCheck`) gains `Revoked`, `Expired bool` fields, and `HasKey` now means "has a *usable* key" rather than merely "found on a contact."

- [ ] **Step 1: Write the failing tests**

Replace the existing `TestPGPRecipientsCheck` in `backend/internal/api/pgp_keyserver_test.go` (it currently uses a placeholder key string `"-----BEGIN PGP PUBLIC KEY BLOCK-----\n...\n-----END PGP PUBLIC KEY BLOCK-----"` that isn't real armored key text — `pgpmail.CheckKeyStatus` will fail to parse it, so it must be replaced with a real generated key) with:

```go
func TestPGPRecipientsCheck(t *testing.T) {
	srv := newTestServer(t)
	all, _ := srv.users.List()
	userID := all[0].ID
	contactsStore, err := srv.userContactsStore(userID)
	if err != nil {
		t.Fatalf("userContactsStore: %v", err)
	}
	hasKeyID, err := pgpmail.GenerateIdentity("Has Key", "haskey@example.com")
	if err != nil {
		t.Fatalf("GenerateIdentity: %v", err)
	}
	revokedKey := generateRevokedArmoredKey(t, "Revoked", "revoked@example.com")
	for _, c := range []contacts.Contact{
		{FormattedName: "Has Key", Emails: []contacts.ContactValue{{Value: "haskey@example.com"}}, PGPKey: hasKeyID.ArmoredPublicKey},
		{FormattedName: "Revoked", Emails: []contacts.ContactValue{{Value: "revoked@example.com"}}, PGPKey: revokedKey},
	} {
		if _, err := contactsStore.Upsert(c); err != nil {
			t.Fatalf("Upsert %s: %v", c.FormattedName, err)
		}
	}

	body, _ := json.Marshal(map[string]any{"addresses": []string{"haskey@example.com", "revoked@example.com", "nokey@example.com"}})
	req := httptest.NewRequest(http.MethodPost, "/api/pgp/recipients/check", bytes.NewReader(body))
	authRequest(srv, req)
	rec := httptest.NewRecorder()
	srv.withAuth(srv.handlePGPRecipientsCheck)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Results []struct {
			Address string `json:"address"`
			HasKey  bool   `json:"hasKey"`
			Revoked bool   `json:"revoked"`
			Expired bool   `json:"expired"`
		} `json:"results"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(resp.Results))
	}
	if !resp.Results[0].HasKey || resp.Results[0].Revoked || resp.Results[0].Expired {
		t.Fatalf("haskey@example.com: expected a usable key, got %+v", resp.Results[0])
	}
	if resp.Results[1].HasKey || !resp.Results[1].Revoked {
		t.Fatalf("revoked@example.com: expected hasKey=false, revoked=true, got %+v", resp.Results[1])
	}
	if resp.Results[2].HasKey || resp.Results[2].Revoked || resp.Results[2].Expired {
		t.Fatalf("nokey@example.com: expected no key at all, got %+v", resp.Results[2])
	}
}
```

Also update `TestPGPKeyserverLookupSuccess`: after the existing `resp.Fingerprint` check, change the response struct and add assertions:

```go
	var resp struct {
		Fingerprint string `json:"fingerprint"`
		Revoked     bool   `json:"revoked"`
		Expired     bool   `json:"expired"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Fingerprint != id.Fingerprint {
		t.Fatalf("fingerprint mismatch: got %s want %s", resp.Fingerprint, id.Fingerprint)
	}
	if resp.Revoked || resp.Expired {
		t.Fatalf("expected a freshly generated key to be neither revoked nor expired, got %+v", resp)
	}
```

(this replaces the existing `var resp struct { Fingerprint string ... }` block and the single fingerprint check that follow it)

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd "/home/yoshi/git/llama labels/backend" && go test ./internal/api/... -run 'TestPGPRecipientsCheck|TestPGPKeyserverLookupSuccess' -v`
Expected: FAIL — `resp.Revoked`/`resp.Expired` don't exist in the actual JSON response yet (zero-value false, but `TestPGPRecipientsCheck` fails because `resp.Results[0].HasKey` is currently computed differently and `resp.Results[1]` — the revoked key — currently has no revoked-detection at all)

- [ ] **Step 3: Write the implementation**

In `backend/internal/api/pgp_keyserver.go`, add the `pgpmail` import to the existing import block:

```go
	"llama-lab/backend/internal/pgpmail"
```

Replace the response in `handlePGPKeyserverLookup`:

```go
	writeJSON(w, http.StatusOK, map[string]any{
		"email":       email,
		"fingerprint": key.GetFingerprint(),
		"keyId":       key.GetHexKeyID(),
		"publicKey":   string(armored),
	})
```

with:

```go
	now := time.Now().Unix()
	writeJSON(w, http.StatusOK, map[string]any{
		"email":       email,
		"fingerprint": key.GetFingerprint(),
		"keyId":       key.GetHexKeyID(),
		"publicKey":   string(armored),
		"revoked":     key.IsRevoked(now),
		"expired":     key.IsExpired(now),
	})
```

Replace `handlePGPRecipientsCheck`'s body from the `type addressStatus struct` line through the end of the function:

```go
	type addressStatus struct {
		Address string `json:"address"`
		HasKey  bool   `json:"hasKey"`
	}
	statuses := make([]addressStatus, 0, len(req.Addresses))
	for _, addr := range req.Addresses {
		_, hasKey := findContactPGPKey(contactsStore, addr)
		statuses = append(statuses, addressStatus{Address: addr, HasKey: hasKey})
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": statuses})
}
```

with:

```go
	type addressStatus struct {
		Address string `json:"address"`
		HasKey  bool   `json:"hasKey"`
		Revoked bool   `json:"revoked"`
		Expired bool   `json:"expired"`
	}
	statuses := make([]addressStatus, 0, len(req.Addresses))
	for _, addr := range req.Addresses {
		status := addressStatus{Address: addr}
		if key, ok := findContactPGPKey(contactsStore, addr); ok {
			if ks, err := pgpmail.CheckKeyStatus(key); err == nil {
				status.Revoked = ks.Revoked
				status.Expired = ks.Expired
				status.HasKey = ks.Usable()
			}
		}
		statuses = append(statuses, status)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": statuses})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd "/home/yoshi/git/llama labels/backend" && go test ./internal/api/... -run 'TestPGPRecipientsCheck|TestPGPKeyserverLookupSuccess' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd "/home/yoshi/git/llama labels/backend" && git add internal/api/pgp_keyserver.go internal/api/pgp_keyserver_test.go && git commit -m "$(cat <<'EOF'
api: surface revoked/expired status on keyserver lookup and recipients-check

hasKey in POST /api/pgp/recipients/check now means "has a usable key"
(not revoked, not expired), matching what the send path actually
treats as usable.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: Final verification

**Files:** none (verification only)

- [ ] **Step 1: Run `go mod tidy` to reflect the new direct dependency on go-crypto**

Test files now import `github.com/ProtonMail/go-crypto/openpgp/packet` directly (previously only an indirect dependency via gopenpgp). Run:

```bash
cd "/home/yoshi/git/llama labels/backend" && go mod tidy
```

Expected: `go.mod`'s `github.com/ProtonMail/go-crypto v1.4.1 // indirect` line loses its `// indirect` marker; `go.sum` is unchanged (the module was already resolved). Review the diff with `git diff go.mod go.sum` before proceeding — it should be a one-line change to `go.mod` and no changes to `go.sum`.

- [ ] **Step 2: Full build, vet, and test run**

```bash
cd "/home/yoshi/git/llama labels/backend" && go build ./... && go vet ./... && go test ./...
```

Expected: build succeeds, no vet warnings, every package reports `ok`.

- [ ] **Step 3: Commit the go.mod/go.sum update (if any)**

```bash
cd "/home/yoshi/git/llama labels/backend" && git status --short go.mod go.sum
```

If either file changed:

```bash
cd "/home/yoshi/git/llama labels/backend" && git add go.mod go.sum && git commit -m "$(cat <<'EOF'
build: go mod tidy — go-crypto is now a direct test dependency

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

If neither changed, skip this step.
