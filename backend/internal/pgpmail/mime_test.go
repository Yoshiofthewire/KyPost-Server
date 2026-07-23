package pgpmail

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/textproto"
	"strings"
	"testing"

	"github.com/ProtonMail/gopenpgp/v3/constants"
	"github.com/ProtonMail/gopenpgp/v3/crypto"

	"kypost-server/backend/internal/mailmsg"
)

// withLoweredMaxInboundMessageBytes temporarily lowers the shared inbound
// size cap so tests can exercise overflow/boundary behavior without
// allocating megabytes of test data, restoring the original value via
// t.Cleanup.
func withLoweredMaxInboundMessageBytes(t *testing.T, limit int64) {
	t.Helper()
	original := mailmsg.MaxInboundMessageBytes
	mailmsg.MaxInboundMessageBytes = limit
	t.Cleanup(func() { mailmsg.MaxInboundMessageBytes = original })
}

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
	// EncryptMIME was called with a nil signer above: the resulting
	// ciphertext must not carry an embedded signature. This guards against
	// the encrypt-implicitly-signs regression where a caller's eagerly
	// loaded identity leaked into EncryptMIME's signer argument even when
	// signing wasn't requested.
	if result.Signed {
		t.Fatal("expected unsigned result when EncryptMIME was called with a nil signer")
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

// TestSignMIMEWithAttachmentPreservesTrailingCRLF is a regression test for a
// bug in buildSignedEnvelope: when content (the signed part) already ends in
// its own "\r\n" — which mailmsg.Message.Build() always produces for
// multipart/mixed messages, since mime/multipart.Writer.Close() terminates
// with "\r\n--boundary--\r\n" — the buggy code skipped writing the boundary
// delimiter's own CRLF, silently truncating 2 bytes off the signed content
// as understood by any real MIME parser (which always strips exactly one
// CRLF as the delimiter separator, not two). That corruption doesn't show up
// by inspecting the produced bytes directly; it only appears once the
// envelope is parsed back through a real mime/multipart.Reader, so this test
// does exactly that instead of just calling VerifyDetached in-process.
func TestSignMIMEWithAttachmentPreservesTrailingCRLF(t *testing.T) {
	alice, err := GenerateIdentity("Alice", "alice@example.com")
	if err != nil {
		t.Fatalf("GenerateIdentity alice: %v", err)
	}

	plaintext := mailmsg.Message{
		From:    "alice@example.com",
		To:      []string{"bob@example.com"},
		Subject: "Signed with attachment",
		Body:    "see attached",
		Mode:    "plain",
		Attachments: []mailmsg.Attachment{
			{Name: "note.txt", MimeType: "text/plain", Content: []byte("hello file")},
		},
	}.Build()

	_, wantContent, err := splitMessage(plaintext)
	if err != nil {
		t.Fatalf("splitMessage: %v", err)
	}
	if !bytes.HasSuffix(wantContent, []byte("\r\n")) {
		t.Fatalf("test setup invalid: expected signed content to end in its own CRLF (multipart/mixed with attachment), got %q", wantContent[len(wantContent)-20:])
	}

	signed, err := SignMIME(plaintext, alice)
	if err != nil {
		t.Fatalf("SignMIME: %v", err)
	}

	// Parse the produced envelope's actual wire bytes back through a real
	// net/mail + mime/multipart reader, the same way a real interoperating
	// PGP/MIME client would.
	msg, err := mail.ReadMessage(bytes.NewReader(signed))
	if err != nil {
		t.Fatalf("mail.ReadMessage: %v", err)
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("mime.ParseMediaType: %v", err)
	}
	if !strings.HasPrefix(mediaType, "multipart/") {
		t.Fatalf("expected multipart Content-Type, got %q", mediaType)
	}

	mr := multipart.NewReader(msg.Body, params["boundary"])
	part, err := mr.NextPart()
	if err != nil {
		t.Fatalf("mr.NextPart (signed content part): %v", err)
	}

	// NextPart parses content's own embedded "Content-Type: ...\r\n\r\n"
	// prefix as this part's MIME headers, since MIME doesn't distinguish
	// "embedded" from "real" headers. Reconstruct the full part bytes from
	// the parsed header plus the remaining body, and compare that
	// reconstruction against content — not part's body alone.
	partBody, err := io.ReadAll(part)
	if err != nil {
		t.Fatalf("io.ReadAll(part): %v", err)
	}
	var gotContent bytes.Buffer
	gotContent.WriteString("Content-Type: " + part.Header.Get("Content-Type") + "\r\n\r\n")
	gotContent.Write(partBody)

	if !bytes.Equal(gotContent.Bytes(), wantContent) {
		t.Fatalf("signed content part corrupted by round-trip through a real MIME parser:\n got  %q\n want %q", gotContent.Bytes(), wantContent)
	}
}

// TestDecryptMIMERejectsOversizedArmoredInput exercises the cheap defense-in-
// depth check on the armored input itself, before any decryption is
// attempted — a huge non-PGP string is rejected immediately with
// ErrMessageTooLarge rather than being handed to the OpenPGP parser at all.
// The recipient identity's key material is never touched at this size, so a
// zero-value (non-nil) *Identity stands in fine.
func TestDecryptMIMERejectsOversizedArmoredInput(t *testing.T) {
	withLoweredMaxInboundMessageBytes(t, 10)
	_, err := DecryptMIME(strings.Repeat("a", 11), &Identity{}, nil)
	if !errors.Is(err, mailmsg.ErrMessageTooLarge) {
		t.Fatalf("got %v, want ErrMessageTooLarge", err)
	}
}

// TestDecryptMIMERejectsDecompressionBomb proves the real OOM guard: a
// small, legitimately-encrypted ciphertext whose plaintext decompresses past
// the cap makes decryption fail closed — the oversized plaintext is never
// returned — mirroring the PGP decompression-bomb scenario the plan calls
// out. gopenpgp's default profile compresses with zlib, so a large,
// highly-compressible plaintext produces a ciphertext far smaller than the
// plaintext itself — exactly the shape of a decompression bomb — while
// still being cheap to generate in a test.
//
// Note this does NOT assert errors.Is(err, mailmsg.ErrMessageTooLarge): the
// underlying go-crypto library deliberately genericizes every parsing error
// encountered while reading symmetrically-decrypted data (including the
// MaxDecompressedMessageSize overflow) into an opaque "parsing error" —
// openpgp/errors.HandleSensitiveParsingError does this on purpose, to avoid
// giving an attacker an oracle that distinguishes "ciphertext too large"
// from "ciphertext corrupted/wrong key" before the message is authenticated.
// So for real encrypted messages the specific sentinel is unreachable here;
// what's verified, and what actually matters for OOM safety, is that
// decryption aborts with *an* error instead of materializing the bomb.
func TestDecryptMIMERejectsDecompressionBomb(t *testing.T) {
	alice, err := GenerateIdentity("Alice", "alice@example.com")
	if err != nil {
		t.Fatalf("GenerateIdentity alice: %v", err)
	}

	const plaintextSize = 200_000
	plaintext := strings.Repeat("a", plaintextSize)

	recipients, err := crypto.NewKeyRing(nil)
	if err != nil {
		t.Fatalf("NewKeyRing: %v", err)
	}
	pubKey, err := crypto.NewKeyFromArmored(alice.ArmoredPublicKey)
	if err != nil {
		t.Fatalf("NewKeyFromArmored: %v", err)
	}
	if err := recipients.AddKey(pubKey); err != nil {
		t.Fatalf("AddKey: %v", err)
	}
	// EncryptMIME itself never requests compression (the default encryption
	// handle leaves Compression at NoCompression), but a third-party sender
	// is under no such obligation — DecryptMIME must handle whatever OpenPGP
	// message structure an attacker sends, compressed or not. Building the
	// ciphertext directly via the lower-level crypto package (bypassing
	// EncryptMIME) with compression explicitly requested simulates exactly
	// that: an attacker-crafted message carrying a compressed packet.
	encHandle, err := crypto.PGP().Encryption().Recipients(recipients).CompressWith(constants.ZLIBCompression).New()
	if err != nil {
		t.Fatalf("build encryption handle: %v", err)
	}
	pgpMessage, err := encHandle.Encrypt([]byte(plaintext))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	armored, err := pgpMessage.Armor()
	if err != nil {
		t.Fatalf("Armor: %v", err)
	}
	if len(armored) >= plaintextSize {
		t.Fatalf("test setup invalid: expected compression to shrink the ciphertext well below the %d-byte plaintext, got %d armored bytes", plaintextSize, len(armored))
	}

	withLoweredMaxInboundMessageBytes(t, 1024)
	result, err := DecryptMIME(armored, alice, nil)
	if err == nil {
		t.Fatalf("expected decryption to fail closed on an oversized decompressed payload, got a result with %d content bytes", len(result.Content))
	}
}

// TestDecryptMIMEAcceptsWithinCapDecompressionBomb is the boundary
// companion to the decompression-bomb test above: the same kind of
// ciphertext, but with the cap left high enough to admit the plaintext,
// decrypts normally.
func TestDecryptMIMEAcceptsWithinCapDecompressionBomb(t *testing.T) {
	alice, err := GenerateIdentity("Alice", "alice@example.com")
	if err != nil {
		t.Fatalf("GenerateIdentity alice: %v", err)
	}

	plaintext := "small and unremarkable"
	recipients, err := crypto.NewKeyRing(nil)
	if err != nil {
		t.Fatalf("NewKeyRing: %v", err)
	}
	pubKey, err := crypto.NewKeyFromArmored(alice.ArmoredPublicKey)
	if err != nil {
		t.Fatalf("NewKeyFromArmored: %v", err)
	}
	if err := recipients.AddKey(pubKey); err != nil {
		t.Fatalf("AddKey: %v", err)
	}
	encHandle, err := crypto.PGP().Encryption().Recipients(recipients).New()
	if err != nil {
		t.Fatalf("build encryption handle: %v", err)
	}
	pgpMessage, err := encHandle.Encrypt([]byte(plaintext))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	armored, err := pgpMessage.Armor()
	if err != nil {
		t.Fatalf("Armor: %v", err)
	}

	result, err := DecryptMIME(armored, alice, nil)
	if err != nil {
		t.Fatalf("DecryptMIME: %v", err)
	}
	if string(result.Content) != plaintext {
		t.Fatalf("got %q, want %q", result.Content, plaintext)
	}
}

// TestParseContentRejectsOversizedContent covers the entry-point guard: a
// content byte slice already larger than the cap is rejected immediately,
// without attempting to parse it as MIME at all.
func TestParseContentRejectsOversizedContent(t *testing.T) {
	withLoweredMaxInboundMessageBytes(t, 10)
	content := []byte("Content-Type: text/plain\r\n\r\n" + strings.Repeat("a", 20))
	_, _, err := ParseContent(content)
	if !errors.Is(err, mailmsg.ErrMessageTooLarge) {
		t.Fatalf("got %v, want ErrMessageTooLarge", err)
	}
}

// TestParseContentAcceptsAtCapBoundary proves the entry-point check is a
// strict "greater than", not "greater than or equal": content sized exactly
// to the (lowered) cap still parses normally.
func TestParseContentAcceptsAtCapBoundary(t *testing.T) {
	body := strings.Repeat("a", 5)
	content := []byte("Content-Type: text/plain\r\n\r\n" + body)
	withLoweredMaxInboundMessageBytes(t, int64(len(content)))

	got, attachments, err := ParseContent(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != body {
		t.Fatalf("got body %q, want %q", got, body)
	}
	if len(attachments) != 0 {
		t.Fatalf("expected no attachments, got %d", len(attachments))
	}
}

// TestParseContentMultipartRejectsWhenOversized proves the size guard
// applies to the multipart path too, not just the simple single-part path:
// a multipart/mixed content blob larger than the (lowered) cap is rejected
// with ErrMessageTooLarge before any part is parsed.
func TestParseContentMultipartRejectsWhenOversized(t *testing.T) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	textPart, err := w.CreatePart(textproto.MIMEHeader{"Content-Type": {"text/plain"}})
	if err != nil {
		t.Fatalf("CreatePart (text): %v", err)
	}
	if _, err := textPart.Write([]byte("hi")); err != nil {
		t.Fatalf("write text part: %v", err)
	}
	attachmentPart, err := w.CreatePart(textproto.MIMEHeader{"Content-Type": {"application/octet-stream"}})
	if err != nil {
		t.Fatalf("CreatePart (attachment): %v", err)
	}
	if _, err := attachmentPart.Write([]byte(strings.Repeat("a", 5000))); err != nil {
		t.Fatalf("write attachment part: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	var content bytes.Buffer
	content.WriteString(`Content-Type: multipart/mixed; boundary=` + w.Boundary() + "\r\n\r\n")
	content.Write(buf.Bytes())

	withLoweredMaxInboundMessageBytes(t, 4096)
	if int64(content.Len()) <= mailmsg.MaxInboundMessageBytes {
		t.Fatalf("test setup invalid: expected overall content (%d bytes) to exceed the lowered cap (%d)", content.Len(), mailmsg.MaxInboundMessageBytes)
	}

	_, _, err = ParseContent(content.Bytes())
	if !errors.Is(err, mailmsg.ErrMessageTooLarge) {
		t.Fatalf("got %v, want ErrMessageTooLarge", err)
	}
}

// TestParseContentMultipartAcceptsWithinCap is the boundary companion to
// TestParseContentMultipartRejectsWhenOversized: the identical multipart
// shape, but with the cap left high enough to admit it, parses out the text
// body and attachment normally — proving the cap doesn't false-positive on
// ordinary multipart content it was never meant to reject.
func TestParseContentMultipartAcceptsWithinCap(t *testing.T) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	textPart, err := w.CreatePart(textproto.MIMEHeader{"Content-Type": {"text/plain"}})
	if err != nil {
		t.Fatalf("CreatePart (text): %v", err)
	}
	if _, err := textPart.Write([]byte("hi")); err != nil {
		t.Fatalf("write text part: %v", err)
	}
	attachmentPart, err := w.CreatePart(textproto.MIMEHeader{"Content-Type": {"application/octet-stream"}, "Content-Disposition": {`attachment; filename="note.txt"`}})
	if err != nil {
		t.Fatalf("CreatePart (attachment): %v", err)
	}
	if _, err := attachmentPart.Write([]byte("attached data")); err != nil {
		t.Fatalf("write attachment part: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	var content bytes.Buffer
	content.WriteString(`Content-Type: multipart/mixed; boundary=` + w.Boundary() + "\r\n\r\n")
	content.Write(buf.Bytes())

	withLoweredMaxInboundMessageBytes(t, int64(content.Len())+1)

	body, attachments, err := ParseContent(content.Bytes())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body != "hi" {
		t.Fatalf("got body %q, want %q", body, "hi")
	}
	if len(attachments) != 1 || string(attachments[0].Content) != "attached data" {
		t.Fatalf("unexpected attachments: %+v", attachments)
	}
}
