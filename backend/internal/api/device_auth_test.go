package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeviceCredentialsFromRequest_Headers(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/inbox", nil)
	req.Header.Set(headerDeviceID, "device-1")
	req.Header.Set(headerDeviceSecret, "secret-1")

	id, secret := deviceCredentialsFromRequest(req)
	if id != "device-1" {
		t.Fatalf("deviceID = %q, want %q", id, "device-1")
	}
	if secret != "secret-1" {
		t.Fatalf("deviceSecret = %q, want %q", secret, "secret-1")
	}
}

func TestDeviceCredentialsFromRequest_MissingHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/inbox", nil)

	id, secret := deviceCredentialsFromRequest(req)
	if id != "" || secret != "" {
		t.Fatalf("deviceID=%q deviceSecret=%q, want both empty", id, secret)
	}
}

func TestDeviceCredentialsFromRequest_NoQueryParamFallback(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/inbox?device=device-2&secret=secret-2", nil)

	id, secret := deviceCredentialsFromRequest(req)
	if id != "" || secret != "" {
		t.Fatalf("deviceID=%q deviceSecret=%q, want both empty (query params are not accepted)", id, secret)
	}
}

func TestDeviceAuthFromRequest_ValidCredentials(t *testing.T) {
	srv := newTestServer(t)
	all, err := srv.users.List()
	if err != nil || len(all) == 0 {
		t.Fatalf("no test user available: %v", err)
	}
	userID := all[0].ID
	deviceID, deviceSecret := pairNativeDevice(t, srv, userID, "device-x")

	req := httptest.NewRequest(http.MethodGet, "/api/notifications/native/pull", nil)
	setDeviceHeaders(req, deviceID, deviceSecret)

	gotUserID, device, ok := srv.deviceAuthFromRequest(req)
	if !ok {
		t.Fatalf("deviceAuthFromRequest ok = false, want true")
	}
	if gotUserID != userID {
		t.Fatalf("userID = %q, want %q", gotUserID, userID)
	}
	if device.DeviceID != deviceID {
		t.Fatalf("device.DeviceID = %q, want %q", device.DeviceID, deviceID)
	}
}

func TestDeviceAuthFromRequest_WrongSecret(t *testing.T) {
	srv := newTestServer(t)
	all, err := srv.users.List()
	if err != nil || len(all) == 0 {
		t.Fatalf("no test user available: %v", err)
	}
	deviceID, _ := pairNativeDevice(t, srv, all[0].ID, "device-y")

	req := httptest.NewRequest(http.MethodGet, "/api/notifications/native/pull", nil)
	setDeviceHeaders(req, deviceID, "not-the-real-secret")

	if _, _, ok := srv.deviceAuthFromRequest(req); ok {
		t.Fatalf("deviceAuthFromRequest ok = true, want false for a wrong secret")
	}
}

func TestDeviceAuthFromRequest_UnknownDevice(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/notifications/native/pull", nil)
	setDeviceHeaders(req, "never-registered", "whatever")

	if _, _, ok := srv.deviceAuthFromRequest(req); ok {
		t.Fatalf("deviceAuthFromRequest ok = true, want false for an unknown device")
	}
}

// TestDeviceAuthFromRequest_RemovedDeviceIsRejected is the core regression
// test for this fix: previously a device's shared account-wide credential
// kept working forever, so removing it from the paired-devices list (e.g.
// via the web UI) did not actually revoke its access. With per-device
// secrets, auth resolves through store.GetNativeDevice, so removing the row
// must reject the device's still-known credentials immediately.
func TestDeviceAuthFromRequest_RemovedDeviceIsRejected(t *testing.T) {
	srv := newTestServer(t)
	all, err := srv.users.List()
	if err != nil || len(all) == 0 {
		t.Fatalf("no test user available: %v", err)
	}
	userID := all[0].ID
	deviceID, deviceSecret := pairNativeDevice(t, srv, userID, "device-z")

	req := httptest.NewRequest(http.MethodGet, "/api/notifications/native/pull", nil)
	setDeviceHeaders(req, deviceID, deviceSecret)
	if _, _, ok := srv.deviceAuthFromRequest(req); !ok {
		t.Fatalf("deviceAuthFromRequest ok = false before removal, want true")
	}

	store, err := srv.userStore(userID)
	if err != nil {
		t.Fatalf("userStore: %v", err)
	}
	if _, err := store.RemoveNativeDevice(deviceID); err != nil {
		t.Fatalf("RemoveNativeDevice: %v", err)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/notifications/native/pull", nil)
	setDeviceHeaders(req2, deviceID, deviceSecret)
	if _, _, ok := srv.deviceAuthFromRequest(req2); ok {
		t.Fatalf("deviceAuthFromRequest ok = true after removal, want false (revocation must be immediate)")
	}
}

func TestDeviceAuthFromRequest_CrossUserIsolation(t *testing.T) {
	srv := newTestServer(t)
	all, err := srv.users.List()
	if err != nil || len(all) == 0 {
		t.Fatalf("no test user available: %v", err)
	}
	userID := all[0].ID
	otherUserID := userID + "-other"

	deviceID, deviceSecret := pairNativeDevice(t, srv, userID, "device-owned")
	otherDeviceID, _ := pairNativeDevice(t, srv, otherUserID, "device-elsewhere")

	// Device A's real credentials must resolve to its own owner, never the
	// other account.
	req := httptest.NewRequest(http.MethodGet, "/api/notifications/native/pull", nil)
	setDeviceHeaders(req, deviceID, deviceSecret)
	gotUserID, _, ok := srv.deviceAuthFromRequest(req)
	if !ok || gotUserID != userID {
		t.Fatalf("deviceAuthFromRequest = (%q, ok=%v), want (%q, true)", gotUserID, ok, userID)
	}

	// Device A's secret does not authenticate as device B's ID.
	crossReq := httptest.NewRequest(http.MethodGet, "/api/notifications/native/pull", nil)
	setDeviceHeaders(crossReq, otherDeviceID, deviceSecret)
	if _, _, ok := srv.deviceAuthFromRequest(crossReq); ok {
		t.Fatalf("deviceAuthFromRequest ok = true using device A's secret against device B's id, want false")
	}
}
