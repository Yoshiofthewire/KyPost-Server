package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"llama-lab/backend/internal/contacts"
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
