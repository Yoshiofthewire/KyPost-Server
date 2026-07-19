package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGroupsGetAcceptsDeviceCredentials drives the endpoint through the
// server's real route table (not a hand-wired middleware call) so it fails
// if GET /api/groups is ever wired back to withAuth instead of
// withMailAuth. Mobile clients only have their own device pairing
// credentials, never a session cookie — see Client_Contact_Update.md Part 0.
func TestGroupsGetAcceptsDeviceCredentials(t *testing.T) {
	srv := newTestServer(t)
	userID := srv.mustBootstrapUserID(t)
	deviceID, deviceSecret := pairNativeDevice(t, srv, userID, "groups-device")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/groups", nil)
	setDeviceHeaders(req, deviceID, deviceSecret)
	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (device auth should reach the handler); body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}
