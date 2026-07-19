package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// These tests cover handleContactsSync's own device-auth check (via
// deviceAuthFromRequest), not previously covered by a dedicated auth test.
func TestContactsSyncAcceptsDeviceCredentialsViaHeader(t *testing.T) {
	srv := newTestServer(t)
	all, err := srv.users.List()
	if err != nil || len(all) == 0 {
		t.Fatalf("no test user available: %v", err)
	}
	deviceID, deviceSecret := pairNativeDevice(t, srv, all[0].ID, "contacts-device")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/contacts/sync", nil)
	setDeviceHeaders(req, deviceID, deviceSecret)
	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (device auth via headers should reach the handler); body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestContactsSyncRejectsQueryParams(t *testing.T) {
	srv := newTestServer(t)
	all, err := srv.users.List()
	if err != nil || len(all) == 0 {
		t.Fatalf("no test user available: %v", err)
	}
	deviceID, deviceSecret := pairNativeDevice(t, srv, all[0].ID, "contacts-device-2")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/contacts/sync?device="+deviceID+"&secret="+deviceSecret, nil)
	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d (query params must not authenticate — headers only)", rec.Code, http.StatusUnauthorized)
	}
}

func TestContactsSyncRejectsInvalidSecret(t *testing.T) {
	srv := newTestServer(t)
	all, err := srv.users.List()
	if err != nil || len(all) == 0 {
		t.Fatalf("no test user available: %v", err)
	}
	deviceID, _ := pairNativeDevice(t, srv, all[0].ID, "contacts-device-3")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/contacts/sync", nil)
	setDeviceHeaders(req, deviceID, "deadbeef")
	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
