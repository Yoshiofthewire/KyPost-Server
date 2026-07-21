package api

// HTTP-level tests for the From-validation branch added to handleMailSend
// (Task 6 of the send-as alias feature). No real SMTP server is reachable
// in this test environment, so tests that need the handler to get past
// From-resolution and reach smtpDeliver point IMAP config at an unreachable
// SMTP target (writeUnreachableSMTPIMAPConfig, from sendas_handlers_test.go)
// and assert the resulting 502 — the same "reached the network layer"
// signal used throughout this package (see server_mail_pgp_test.go and
// sendas_handlers_test.go). See mail_from_resolve_test.go for pure unit
// tests of the underlying resolveMailFrom helper, which is what actually
// verifies the resolved From *value* in each branch without needing a
// reachable SMTP server.

import (
	"net/http"
	"testing"
)

// TestHandleMailSendWithNoFromUsesAccountAddress is the critical regression
// guard: every existing caller never sends a `from` field at all, and that
// must keep behaving exactly as it did before this task — reaching
// smtpDeliver with the account's own address, not being rejected by the new
// validation branch.
func TestHandleMailSendWithNoFromUsesAccountAddress(t *testing.T) {
	srv := newTestServer(t)
	userID := srv.mustBootstrapUserID(t)
	writeUnreachableSMTPIMAPConfig(t, srv, userID, "alice@example.com")

	rec := doJSONAuth(srv, srv.withAuth(srv.handleMailSend), http.MethodPost, "/api/mail/send",
		map[string]any{"to": "bob@example.com", "subject": "hi", "body": "hello"}, userID)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d (reached smtpDeliver); body=%s", rec.Code, http.StatusBadGateway, rec.Body.String())
	}
}

// TestHandleMailSendWithOwnAddressAsFromSucceeds proves that explicitly
// resubmitting the account's own address (in any casing) behaves the same
// as omitting `from` altogether, without ever needing to consult the
// send-as alias store.
func TestHandleMailSendWithOwnAddressAsFromSucceeds(t *testing.T) {
	srv := newTestServer(t)
	userID := srv.mustBootstrapUserID(t)
	writeUnreachableSMTPIMAPConfig(t, srv, userID, "alice@example.com")

	rec := doJSONAuth(srv, srv.withAuth(srv.handleMailSend), http.MethodPost, "/api/mail/send",
		map[string]any{"to": "bob@example.com", "subject": "hi", "body": "hello", "from": "ALICE@Example.com"}, userID)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d (reached smtpDeliver); body=%s", rec.Code, http.StatusBadGateway, rec.Body.String())
	}
}

// TestHandleMailSendWithUnverifiedAliasRejected proves an unrecognized (no
// alias record at all) From address is rejected with 403 before any SMTP
// attempt — the IMAP config points at 127.0.0.1:1 (refuses connections
// near-instantly), so a 403 (rather than 502) proves the rejection happens
// before the network layer is ever touched.
func TestHandleMailSendWithUnverifiedAliasRejected(t *testing.T) {
	srv := newTestServer(t)
	userID := srv.mustBootstrapUserID(t)
	writeUnreachableSMTPIMAPConfig(t, srv, userID, "alice@example.com")

	rec := doJSONAuth(srv, srv.withAuth(srv.handleMailSend), http.MethodPost, "/api/mail/send",
		map[string]any{"to": "bob@example.com", "subject": "hi", "body": "hello", "from": "nobody@example.com"}, userID)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	const want = "the from address is not a verified send-as alias for this account"
	if got := rec.Body.String(); got != want+"\n" {
		t.Fatalf("body = %q, want %q", got, want+"\n")
	}
}

// TestHandleMailSendWithInvalidFromRejected proves a malformed `from` value
// is rejected 400 before any SMTP attempt.
func TestHandleMailSendWithInvalidFromRejected(t *testing.T) {
	srv := newTestServer(t)
	userID := srv.mustBootstrapUserID(t)
	writeUnreachableSMTPIMAPConfig(t, srv, userID, "alice@example.com")

	rec := doJSONAuth(srv, srv.withAuth(srv.handleMailSend), http.MethodPost, "/api/mail/send",
		map[string]any{"to": "bob@example.com", "subject": "hi", "body": "hello", "from": "not-an-address"}, userID)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	const want = "invalid from address"
	if got := rec.Body.String(); got != want+"\n" {
		t.Fatalf("body = %q, want %q", got, want+"\n")
	}
}

// TestHandleMailSendWithVerifiedAliasUsesAliasFrom proves a from address
// backed by a verified alias record is accepted: the request gets past
// From-validation and reaches smtpDeliver (502 from the unreachable SMTP
// target), rather than being rejected 403. The alias is created and marked
// verified directly via the store (bypassing HTTP/the not-yet-built
// verification poller), matching the pattern this feature's other tasks
// already established.
func TestHandleMailSendWithVerifiedAliasUsesAliasFrom(t *testing.T) {
	srv := newTestServer(t)
	userID := srv.mustBootstrapUserID(t)
	writeUnreachableSMTPIMAPConfig(t, srv, userID, "alice@example.com")

	store, err := srv.userSendAsStore(userID)
	if err != nil {
		t.Fatalf("userSendAsStore: %v", err)
	}
	alias, err := store.Create(userID, "bob@example.com", "Bob Example")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.MarkVerified(alias.ID); err != nil {
		t.Fatalf("MarkVerified: %v", err)
	}

	rec := doJSONAuth(srv, srv.withAuth(srv.handleMailSend), http.MethodPost, "/api/mail/send",
		map[string]any{"to": "carol@example.com", "subject": "hi", "body": "hello", "from": "Bob@Example.com"}, userID)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d (verified alias should reach smtpDeliver, not be rejected 403); body=%s", rec.Code, http.StatusBadGateway, rec.Body.String())
	}
}
