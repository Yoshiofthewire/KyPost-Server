package api

// Pure unit tests for resolveMailFrom, the From-resolution helper extracted
// out of handleMailSend so every branch of the new send-as validation logic
// (added on top of the pre-existing "from := sanitizeHeaderValue(payload.
// Username)" behavior) can be exercised directly, without needing a
// reachable SMTP server or HTTP plumbing. See mail_send_from_test.go for the
// complementary HTTP-level tests that exercise handleMailSend as a whole.

import (
	"net/http"
	"testing"

	"kypost-server/backend/internal/sendas"
)

// failIfCalled returns an aliasStoreFn that fails the test if it's ever
// invoked — used to prove the trivial branches (no requested From, or a
// requested From that names the account's own address) never reach into
// the alias store at all, matching today's zero-lookup behavior exactly.
func failIfCalled(t *testing.T) func() (*sendas.Store, error) {
	t.Helper()
	return func() (*sendas.Store, error) {
		t.Fatal("aliasStoreFn should not have been called")
		return nil, nil
	}
}

func TestResolveMailFromEmptyUsesAccountAddress(t *testing.T) {
	from, status, msg := resolveMailFrom("alice@example.com", "", failIfCalled(t))
	if status != 0 || msg != "" {
		t.Fatalf("status=%d msg=%q, want 0/\"\"", status, msg)
	}
	if from != "alice@example.com" {
		t.Fatalf("from = %q, want alice@example.com", from)
	}
}

func TestResolveMailFromOwnAddressDifferentCasingUsesAccountAddress(t *testing.T) {
	from, status, msg := resolveMailFrom("alice@example.com", "ALICE@Example.com", failIfCalled(t))
	if status != 0 || msg != "" {
		t.Fatalf("status=%d msg=%q, want 0/\"\"", status, msg)
	}
	if from != "alice@example.com" {
		t.Fatalf("from = %q, want alice@example.com", from)
	}
}

func TestResolveMailFromInvalidAddressRejected(t *testing.T) {
	from, status, msg := resolveMailFrom("alice@example.com", "not-an-address", failIfCalled(t))
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
	}
	if msg != "invalid from address" {
		t.Fatalf("msg = %q, want %q", msg, "invalid from address")
	}
	if from != "" {
		t.Fatalf("from = %q, want empty on error", from)
	}
}

func TestResolveMailFromUnverifiedAliasRejected(t *testing.T) {
	store, err := sendas.New(t.TempDir())
	if err != nil {
		t.Fatalf("sendas.New: %v", err)
	}
	from, status, msg := resolveMailFrom("alice@example.com", "bob@example.com", func() (*sendas.Store, error) {
		return store, nil
	})
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", status, http.StatusForbidden)
	}
	if msg != "the from address is not a verified send-as alias for this account" {
		t.Fatalf("msg = %q", msg)
	}
	if from != "" {
		t.Fatalf("from = %q, want empty on error", from)
	}
}

func TestResolveMailFromVerifiedAliasUsesAliasFrom(t *testing.T) {
	store, err := sendas.New(t.TempDir())
	if err != nil {
		t.Fatalf("sendas.New: %v", err)
	}
	alias, err := store.Create("user-1", "bob@example.com", "Bob Example")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.MarkVerified(alias.ID); err != nil {
		t.Fatalf("MarkVerified: %v", err)
	}

	from, status, msg := resolveMailFrom("alice@example.com", "Bob@Example.com", func() (*sendas.Store, error) {
		return store, nil
	})
	if status != 0 || msg != "" {
		t.Fatalf("status=%d msg=%q, want 0/\"\"", status, msg)
	}
	want := `"Bob Example" <bob@example.com>`
	if from != want {
		t.Fatalf("from = %q, want %q", from, want)
	}
}

func TestResolveMailFromAliasStoreErrorSurfacesInternalError(t *testing.T) {
	from, status, msg := resolveMailFrom("alice@example.com", "bob@example.com", func() (*sendas.Store, error) {
		return nil, http.ErrBodyNotAllowed // any non-nil error stand-in
	})
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", status, http.StatusInternalServerError)
	}
	if msg != "failed to check send-as aliases" {
		t.Fatalf("msg = %q", msg)
	}
	if from != "" {
		t.Fatalf("from = %q, want empty on error", from)
	}
}
