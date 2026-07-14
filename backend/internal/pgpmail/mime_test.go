package pgpmail

import (
	"strings"
	"testing"

	"github.com/ProtonMail/gopenpgp/v3/crypto"

	"llama-lab/backend/internal/mailmsg"
)

// extractOctetStreamPart is a test-only MIME walker that finds the armored
// PGP data part EncryptMIME produces, mirroring the content-sniffing
// technique (crypto.IsPGPMessage) the receive-path integration (Task 7) uses
// against real IMAP-fetched attachments.
func extractOctetStreamPart(t *testing.T, raw []byte) (string, bool) {
	t.Helper()
	_, content, err := splitMessage(raw)
	if err != nil {
		t.Fatalf("splitMessage: %v", err)
	}
	_, attachments, err := ParseContent(content)
	if err != nil {
		t.Fatalf("ParseContent: %v", err)
	}
	for _, a := range attachments {
		if crypto.IsPGPMessage(string(a.Content)) {
			return string(a.Content), true
		}
	}
	return "", false
}

func TestEncryptDecryptMIMERoundTrip(t *testing.T) {
	alice, err := GenerateIdentity("Alice", "alice@example.com")
	if err != nil {
		t.Fatalf("GenerateIdentity alice: %v", err)
	}
	bob, err := GenerateIdentity("Bob", "bob@example.com")
	if err != nil {
		t.Fatalf("GenerateIdentity bob: %v", err)
	}

	plaintext := mailmsg.Message{
		From:    "alice@example.com",
		To:      []string{"bob@example.com"},
		Subject: "Secret",
		Body:    "meet at dawn",
		Mode:    "plain",
	}.Build()

	encrypted, err := EncryptMIME(plaintext, []string{bob.ArmoredPublicKey}, alice)
	if err != nil {
		t.Fatalf("EncryptMIME: %v", err)
	}
	if !strings.Contains(string(encrypted), "multipart/encrypted") {
		t.Fatal("expected multipart/encrypted content type in output")
	}
	if !strings.Contains(string(encrypted), "Subject: Secret") {
		t.Fatal("expected Subject header preserved on the outer envelope")
	}

	armoredData, ok := extractOctetStreamPart(t, encrypted)
	if !ok {
		t.Fatal("expected an application/octet-stream data part")
	}

	result, err := DecryptMIME(armoredData, bob, []string{alice.ArmoredPublicKey})
	if err != nil {
		t.Fatalf("DecryptMIME: %v", err)
	}
	if !result.Verified {
		t.Fatal("expected signature to verify")
	}
	if result.SignerFingerprint != alice.Fingerprint {
		t.Fatalf("signer fingerprint mismatch: got %s want %s", result.SignerFingerprint, alice.Fingerprint)
	}
	body, attachments, err := ParseContent(result.Content)
	if err != nil {
		t.Fatalf("ParseContent: %v", err)
	}
	if body != "meet at dawn" {
		t.Fatalf("body mismatch: got %q", body)
	}
	if len(attachments) != 0 {
		t.Fatalf("expected no attachments, got %d", len(attachments))
	}
}

func TestEncryptDecryptMIMEWithAttachment(t *testing.T) {
	alice, err := GenerateIdentity("Alice", "alice@example.com")
	if err != nil {
		t.Fatalf("GenerateIdentity alice: %v", err)
	}

	plaintext := mailmsg.Message{
		From:    "alice@example.com",
		To:      []string{"alice@example.com"},
		Subject: "With attachment",
		Body:    "see attached",
		Mode:    "plain",
		Attachments: []mailmsg.Attachment{
			{Name: "note.txt", MimeType: "text/plain", Content: []byte("hello file")},
		},
	}.Build()

	encrypted, err := EncryptMIME(plaintext, []string{alice.ArmoredPublicKey}, nil)
	if err != nil {
		t.Fatalf("EncryptMIME: %v", err)
	}
	armoredData, ok := extractOctetStreamPart(t, encrypted)
	if !ok {
		t.Fatal("expected an application/octet-stream data part")
	}

	result, err := DecryptMIME(armoredData, alice, nil)
	if err != nil {
		t.Fatalf("DecryptMIME: %v", err)
	}
	body, attachments, err := ParseContent(result.Content)
	if err != nil {
		t.Fatalf("ParseContent: %v", err)
	}
	if body != "see attached" {
		t.Fatalf("body mismatch: got %q", body)
	}
	if len(attachments) != 1 || attachments[0].Name != "note.txt" || string(attachments[0].Content) != "hello file" {
		t.Fatalf("unexpected attachments: %+v", attachments)
	}
}

func TestSignMIMEAndVerifyDetached(t *testing.T) {
	alice, err := GenerateIdentity("Alice", "alice@example.com")
	if err != nil {
		t.Fatalf("GenerateIdentity alice: %v", err)
	}

	plaintext := mailmsg.Message{
		From:    "alice@example.com",
		To:      []string{"bob@example.com"},
		Subject: "Signed only",
		Body:    "trust me",
		Mode:    "plain",
	}.Build()

	signed, err := SignMIME(plaintext, alice)
	if err != nil {
		t.Fatalf("SignMIME: %v", err)
	}
	if !strings.Contains(string(signed), "multipart/signed") {
		t.Fatal("expected multipart/signed content type in output")
	}

	_, content, err := splitMessage(plaintext)
	if err != nil {
		t.Fatalf("splitMessage: %v", err)
	}
	sigStart := strings.Index(string(signed), "-----BEGIN PGP SIGNATURE-----")
	if sigStart == -1 {
		t.Fatal("expected an armored signature block in the output")
	}
	sigEnd := strings.Index(string(signed)[sigStart:], "-----END PGP SIGNATURE-----") + len("-----END PGP SIGNATURE-----")
	armoredSig := string(signed)[sigStart : sigStart+sigEnd]

	result, err := VerifyDetached(content, armoredSig, []string{alice.ArmoredPublicKey})
	if err != nil {
		t.Fatalf("VerifyDetached: %v", err)
	}
	if !result.Verified {
		t.Fatal("expected signature to verify")
	}
	if result.SignerFingerprint != alice.Fingerprint {
		t.Fatalf("signer fingerprint mismatch: got %s want %s", result.SignerFingerprint, alice.Fingerprint)
	}
}
