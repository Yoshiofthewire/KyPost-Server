package imap

import (
	"strings"
	"testing"

	goimap "github.com/BrianLeishman/go-imap"

	"kypost-server/backend/internal/mailmsg"
)

func TestPgpDetectSignature(t *testing.T) {
	t.Run("no attachments", func(t *testing.T) {
		if sig := pgpDetectSignature(nil); sig != "" {
			t.Fatalf("expected empty signature, got %q", sig)
		}
	})

	t.Run("no signature attachment", func(t *testing.T) {
		attachments := []goimap.Attachment{
			{Name: "photo.png", MimeType: "image/png", Content: []byte{0x89, 0x50, 0x4e, 0x47}},
		}
		if sig := pgpDetectSignature(attachments); sig != "" {
			t.Fatalf("expected empty signature, got %q", sig)
		}
	})

	t.Run("armored signature attachment", func(t *testing.T) {
		armored := "-----BEGIN PGP SIGNATURE-----\n\nfakebase64data\n-----END PGP SIGNATURE-----\n"
		attachments := []goimap.Attachment{
			{Name: "unrelated.txt", MimeType: "text/plain", Content: []byte("hello")},
			{Name: "signature.asc", MimeType: "application/pgp-signature", Content: []byte(armored)},
		}
		got := pgpDetectSignature(attachments)
		if got != armored {
			t.Fatalf("expected %q, got %q", armored, got)
		}
	})

	t.Run("leading whitespace still detected", func(t *testing.T) {
		armored := "  \n-----BEGIN PGP SIGNATURE-----\n\nfakebase64data\n-----END PGP SIGNATURE-----\n"
		attachments := []goimap.Attachment{
			{Name: "signature.asc", MimeType: "application/pgp-signature", Content: []byte(armored)},
		}
		if got := pgpDetectSignature(attachments); got != armored {
			t.Fatalf("expected %q, got %q", armored, got)
		}
	})
}

// TestEmailContentSize exercises the size-accounting GetMessageBodies and
// fetchAttachments both gate on before ever handing content back to a
// caller. GetMessageBodies/fetchAttachments/GetAttachment themselves can't
// be driven directly in this package's tests without a live (or fake)
// *goimap.Dialer connection — none exists in this test suite today, and
// standing one up is out of scope for this change — so this pins down the
// exact arithmetic those call sites depend on: the total of HTML, text, and
// every attachment's content, which is what a real oversized message would
// trip.
func TestEmailContentSize(t *testing.T) {
	t.Run("sums HTML, text, and every attachment", func(t *testing.T) {
		e := &goimap.Email{
			HTML: strings.Repeat("h", 10),
			Text: strings.Repeat("t", 5),
			Attachments: []goimap.Attachment{
				{Content: make([]byte, 3)},
				{Content: make([]byte, 7)},
			},
		}
		got := emailContentSize(e)
		want := int64(10 + 5 + 3 + 7)
		if got != want {
			t.Fatalf("got %d, want %d", got, want)
		}
	})

	t.Run("empty email sizes to zero", func(t *testing.T) {
		if got := emailContentSize(&goimap.Email{}); got != 0 {
			t.Fatalf("got %d, want 0", got)
		}
	})

	t.Run("many small attachments add up like one big one", func(t *testing.T) {
		withLoweredMaxInboundMessageBytes(t, 20)
		e := &goimap.Email{}
		for i := 0; i < 5; i++ {
			e.Attachments = append(e.Attachments, goimap.Attachment{Content: make([]byte, 5)})
		}
		// 5 attachments * 5 bytes = 25 bytes total, over the 20-byte cap,
		// even though no single attachment is anywhere near it.
		if size := emailContentSize(e); size <= mailmsg.MaxInboundMessageBytes {
			t.Fatalf("expected aggregate size %d to exceed cap %d", size, mailmsg.MaxInboundMessageBytes)
		}
	})

	t.Run("exactly at the cap does not overflow it", func(t *testing.T) {
		withLoweredMaxInboundMessageBytes(t, 16)
		e := &goimap.Email{HTML: strings.Repeat("a", 16)}
		if size := emailContentSize(e); size > mailmsg.MaxInboundMessageBytes {
			t.Fatalf("expected size %d not to exceed cap %d", size, mailmsg.MaxInboundMessageBytes)
		}
	})
}
