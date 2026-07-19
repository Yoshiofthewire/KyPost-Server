package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// These tests cover withMailAuth / resolveMailAuthContext: the dual auth
// path (session cookie or a paired device's own credentials) added to the
// mail read/act-on endpoints (inbox, folders, actions, draft, send), plus
// the scope boundary that account setup (/api/imap/config, /api/imap/test)
// stays cookie-only. See Mobile_Mail_Relay.md.

func TestMailAuthCookieStillWorks(t *testing.T) {
	srv := newTestServer(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/inbox", nil)
	authRequest(srv, req)
	srv.withMailAuth(srv.handleInbox).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestMailAuthAcceptsDeviceCredentials(t *testing.T) {
	srv := newTestServer(t)
	all, err := srv.users.List()
	if err != nil || len(all) == 0 {
		t.Fatalf("no test user available: %v", err)
	}
	deviceID, deviceSecret := pairNativeDevice(t, srv, all[0].ID, "mail-device")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/inbox/folders", nil)
	setDeviceHeaders(req, deviceID, deviceSecret)
	srv.withMailAuth(srv.handleInboxFolders).ServeHTTP(rec, req)

	// No cookie was set — auth must have succeeded via device credentials
	// alone for the handler to be reached at all. No IMAP account is
	// configured for this test user, so the handler's own
	// errIMAPNotConfigured path (400) is the expected "auth passed, nothing
	// configured yet" signal.
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (imap not configured); body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestMailAuthDeviceCredentialsWorkRegardlessOfPairingSecret(t *testing.T) {
	srv := newTestServer(t)
	all, err := srv.users.List()
	if err != nil || len(all) == 0 {
		t.Fatalf("no test user available: %v", err)
	}
	deviceID, deviceSecret := pairNativeDevice(t, srv, all[0].ID, "mail-device-2")
	// Ongoing device auth no longer depends on PAIRING_SECRET at all — only
	// registration and the pairing-QR mint still check it.
	srv.pairingSecret = ""

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/inbox/folders", nil)
	setDeviceHeaders(req, deviceID, deviceSecret)
	srv.withMailAuth(srv.handleInboxFolders).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (imap not configured); body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestMailAuthRejectsInvalidSecret(t *testing.T) {
	srv := newTestServer(t)
	all, err := srv.users.List()
	if err != nil || len(all) == 0 {
		t.Fatalf("no test user available: %v", err)
	}
	deviceID, _ := pairNativeDevice(t, srv, all[0].ID, "mail-device-3")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/inbox", nil)
	setDeviceHeaders(req, deviceID, "wrong-secret")
	srv.withMailAuth(srv.handleInbox).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestMailAuthRejectsUnknownDevice(t *testing.T) {
	srv := newTestServer(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/inbox", nil)
	setDeviceHeaders(req, "never-registered", "anything")
	srv.withMailAuth(srv.handleInbox).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestMailAuthNoCredentialsIsPlainUnauthorized(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/inbox", nil)
	srv.withMailAuth(srv.handleInbox).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

// TestMailAuthScopeBoundaryExcludesAccountSetup confirms /api/imap/config
// and /api/imap/test — wired with withAuth, not withMailAuth — never accept
// device pairing credentials. Mobile must never see or set raw mail
// credentials; account setup stays a web-only, cookie-only flow.
func TestMailAuthScopeBoundaryExcludesAccountSetup(t *testing.T) {
	srv := newTestServer(t)
	all, err := srv.users.List()
	if err != nil || len(all) == 0 {
		t.Fatalf("no test user available: %v", err)
	}
	deviceID, deviceSecret := pairNativeDevice(t, srv, all[0].ID, "mail-device-4")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/imap/config", nil)
	setDeviceHeaders(req, deviceID, deviceSecret)
	srv.withAuth(srv.handleIMAPConfig).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d (withAuth must ignore device credentials); body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}
