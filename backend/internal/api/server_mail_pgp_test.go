package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"github.com/ProtonMail/gopenpgp/v3/crypto"
	"llama-lab/backend/internal/contacts"
	"llama-lab/backend/internal/pgpmail"
)

func TestDecodeMailRequestParsesEncryptAndSign(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"to":      "bob@example.com",
		"subject": "hi",
		"body":    "hello",
		"encrypt": true,
		"sign":    true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/mail/send", bytes.NewReader(body))
	decoded, errMsg, err := decodeMailRequest(req)
	if err != nil {
		t.Fatalf("decodeMailRequest: %v (%s)", err, errMsg)
	}
	if !decoded.Encrypt || !decoded.Sign {
		t.Fatalf("expected Encrypt and Sign both true, got %+v", decoded)
	}
}

func TestFindContactPGPKey(t *testing.T) {
	store, err := contacts.New(t.TempDir())
	if err != nil {
		t.Fatalf("contacts.New: %v", err)
	}
	if _, err := store.Upsert(contacts.Contact{
		FormattedName: "Bob",
		Emails:        []contacts.ContactValue{{Value: "Bob@Example.com"}},
		PGPKey:        "-----BEGIN PGP PUBLIC KEY BLOCK-----\n...\n-----END PGP PUBLIC KEY BLOCK-----",
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	key, ok := findContactPGPKey(store, "bob@example.com")
	if !ok || key == "" {
		t.Fatalf("expected a key for bob@example.com, got ok=%v key=%q", ok, key)
	}

	if _, ok := findContactPGPKey(store, "nobody@example.com"); ok {
		t.Fatal("expected no key for an unknown address")
	}
}

// TestBuildEncryptedSendArgsKeepsFullRecipientsInSentFolder guards against a
// regression where the encrypted-send branch passed the with-key-only
// filtered lists to finishMailSend's Sent-folder parameters, silently
// dropping pickup-notified (no-key) recipients from the sender's own Sent
// record even though they received a plaintext notification. The Sent
// record must list every original recipient; only the SMTP envelope should
// be restricted to the with-key subset.
func TestBuildEncryptedSendArgsKeepsFullRecipientsInSentFolder(t *testing.T) {
	toList := []string{"alice@example.com", "bob@example.com"}
	ccList := []string{"carol@example.com"}
	bccList := []string{"dave@example.com"}
	withKeyEmails := []string{"alice@example.com", "carol@example.com"} // bob and dave have no key

	draftTo, draftCC, draftBCC, smtpRecipients := buildEncryptedSendArgs(toList, ccList, bccList, withKeyEmails)

	// Sent-folder record: must retain every original recipient, including
	// the no-key ones who only got a pickup notification.
	if len(draftTo) != 2 || draftTo[0] != "alice@example.com" || draftTo[1] != "bob@example.com" {
		t.Fatalf("draftTo should equal original toList unfiltered, got %v", draftTo)
	}
	if len(draftCC) != 1 || draftCC[0] != "carol@example.com" {
		t.Fatalf("draftCC should equal original ccList unfiltered, got %v", draftCC)
	}
	if len(draftBCC) != 1 || draftBCC[0] != "dave@example.com" {
		t.Fatalf("draftBCC should equal original bccList unfiltered, got %v", draftBCC)
	}

	// SMTP envelope: must be restricted to the with-key subset only — the
	// encrypted bytes must never be sent to a recipient without a key.
	wantSMTP := []string{"alice@example.com", "carol@example.com"}
	if len(smtpRecipients) != len(wantSMTP) {
		t.Fatalf("smtpRecipients length mismatch: got %v want %v", smtpRecipients, wantSMTP)
	}
	for i := range wantSMTP {
		if smtpRecipients[i] != wantSMTP[i] {
			t.Fatalf("smtpRecipients mismatch at %d: got %v want %v", i, smtpRecipients, wantSMTP)
		}
	}
	// bob and dave (no key) must not appear in the SMTP envelope.
	for _, r := range smtpRecipients {
		if r == "bob@example.com" || r == "dave@example.com" {
			t.Fatalf("smtpRecipients must not include no-key recipient %q, got %v", r, smtpRecipients)
		}
	}
}

// TestEncryptSignerOnlyPassesIdentityWhenSignRequested guards against the
// encrypt-implicitly-signs regression: handleMailSend eagerly loads a signer
// identity whenever req.Sign || req.Encrypt is true (so it can also cover
// the sign-only branch and the "signing requires an identity" 400 check),
// but that eagerly loaded identity must never leak into EncryptMIME's signer
// argument unless the caller explicitly asked to sign. Encrypt and Sign are
// independent per-email toggles.
func TestEncryptSignerOnlyPassesIdentityWhenSignRequested(t *testing.T) {
	identity, err := pgpmail.GenerateIdentity("Alice", "alice@example.com")
	if err != nil {
		t.Fatalf("GenerateIdentity: %v", err)
	}

	if got := encryptSigner(identity, false); got != nil {
		t.Fatalf("Encrypt=true,Sign=false: expected nil signer even though an identity exists, got %+v", got)
	}
	if got := encryptSigner(identity, true); got != identity {
		t.Fatalf("Encrypt=true,Sign=true: expected the loaded identity to be passed through")
	}
	if got := encryptSigner(nil, true); got != nil {
		t.Fatalf("expected nil to stay nil when no identity was loaded, got %+v", got)
	}
	if got := encryptSigner(nil, false); got != nil {
		t.Fatalf("expected nil to stay nil when no identity was loaded, got %+v", got)
	}
}

func TestIntersectPreservesOrderAndIsCaseInsensitive(t *testing.T) {
	got := intersect(
		[]string{"Alice@Example.com", "bob@example.com", "carol@example.com"},
		[]string{"bob@example.com", "ALICE@EXAMPLE.COM"},
	)
	want := []string{"Alice@Example.com", "bob@example.com"}
	if len(got) != len(want) {
		t.Fatalf("length mismatch: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mismatch at %d: got %v want %v", i, got, want)
		}
	}
}

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
