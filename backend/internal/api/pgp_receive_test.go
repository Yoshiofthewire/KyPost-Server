package api

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"

	"github.com/ProtonMail/gopenpgp/v3/crypto"

	imapadapter "llama-lab/backend/internal/adapters/imap"
	"llama-lab/backend/internal/contacts"
	"llama-lab/backend/internal/mailmsg"
	"llama-lab/backend/internal/pgpmail"
)

// extractArmoredPGPPayload is a test-only helper that pulls the armored
// OpenPGP data part out of a full multipart/encrypted envelope (as
// EncryptMIME produces), mirroring the content-sniffing technique
// pgpDetectPayload uses in production (internal/adapters/imap/client.go) —
// production reaches the same bytes via goimap's own attachment parsing
// rather than this direct MIME walk.
func extractArmoredPGPPayload(t *testing.T, raw []byte) string {
	t.Helper()
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("mail.ReadMessage: %v", err)
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		t.Fatalf("expected a multipart Content-Type, got %q (%v)", msg.Header.Get("Content-Type"), err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextPart: %v", err)
		}
		body, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("ReadAll part: %v", err)
		}
		if crypto.IsPGPMessage(string(body)) {
			return string(body)
		}
	}
	t.Fatal("no armored pgp payload found in encrypted envelope")
	return ""
}

func TestDecryptPGPMessageContentRoundTrip(t *testing.T) {
	srv := newTestServer(t)
	all, err := srv.users.List()
	if err != nil || len(all) == 0 {
		t.Fatalf("no test user available: %v", err)
	}
	userID := all[0].ID

	recipient, err := pgpmail.GenerateIdentity("Recipient", "recipient@example.com")
	if err != nil {
		t.Fatalf("GenerateIdentity: %v", err)
	}
	sealed, err := recipient.SealPrivateKey(srv.pgpPrivateKeyPath)
	if err != nil {
		t.Fatalf("SealPrivateKey: %v", err)
	}
	if _, err := srv.users.SetPGPIdentity(userID, recipient.Fingerprint, recipient.KeyID, recipient.ArmoredPublicKey, sealed, "generated", "2026-07-14T00:00:00Z"); err != nil {
		t.Fatalf("SetPGPIdentity: %v", err)
	}

	sender, err := pgpmail.GenerateIdentity("Sender", "sender@example.com")
	if err != nil {
		t.Fatalf("GenerateIdentity sender: %v", err)
	}
	contactsStore, err := srv.userContactsStore(userID)
	if err != nil {
		t.Fatalf("userContactsStore: %v", err)
	}
	if _, err := contactsStore.Upsert(contacts.Contact{
		FormattedName: "Sender",
		Emails:        []contacts.ContactValue{{Value: "sender@example.com"}},
		PGPKey:        sender.ArmoredPublicKey,
	}); err != nil {
		t.Fatalf("Upsert contact: %v", err)
	}

	plaintext := mailmsg.Message{
		From:    "sender@example.com",
		To:      []string{"recipient@example.com"},
		Subject: "Secret",
		Body:    "meet at dawn",
		Mode:    "plain",
	}.Build()
	encrypted, err := pgpmail.EncryptMIME(plaintext, []string{recipient.ArmoredPublicKey}, sender)
	if err != nil {
		t.Fatalf("EncryptMIME: %v", err)
	}

	payload := extractArmoredPGPPayload(t, encrypted)
	content := imapadapter.MessageContent{PGPEncryptedPayload: payload}
	result := srv.decryptPGPMessageContent(userID, content)

	if result.PGPDecryptError != "" {
		t.Fatalf("unexpected decrypt error: %s", result.PGPDecryptError)
	}
	if result.Body != "meet at dawn" {
		t.Fatalf("body mismatch: got %q", result.Body)
	}
	if !result.PGPVerified {
		t.Fatal("expected signature to verify against the known contact key")
	}
	if result.PGPSignerFingerprint != sender.Fingerprint {
		t.Fatalf("signer fingerprint mismatch: got %s want %s", result.PGPSignerFingerprint, sender.Fingerprint)
	}
}

func TestDecryptPGPMessageContentNoIdentityConfigured(t *testing.T) {
	srv := newTestServer(t)
	all, err := srv.users.List()
	if err != nil || len(all) == 0 {
		t.Fatalf("no test user available: %v", err)
	}
	userID := all[0].ID

	content := imapadapter.MessageContent{PGPEncryptedPayload: "-----BEGIN PGP MESSAGE-----\nbogus\n-----END PGP MESSAGE-----"}
	result := srv.decryptPGPMessageContent(userID, content)
	if result.PGPDecryptError == "" {
		t.Fatal("expected a decrypt error when no pgp identity is configured")
	}
}
