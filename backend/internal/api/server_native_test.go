package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	imapadapter "llama-lab/backend/internal/adapters/imap"
	"llama-lab/backend/internal/config"
	"llama-lab/backend/internal/health"
	"llama-lab/backend/internal/logging"
	"llama-lab/backend/internal/state"
)

type stubMailClient struct{}

func (s *stubMailClient) ListUnreadInbox(_ context.Context, _ string) ([]imapadapter.Message, string, error) {
	return nil, "", nil
}

func (s *stubMailClient) ListUnreadMessages(_ context.Context, _ string, _ int) ([]imapadapter.UnreadMessage, error) {
	return nil, nil
}

func (s *stubMailClient) ListLabels(_ context.Context) ([]string, error) {
	return nil, nil
}

func (s *stubMailClient) ListSubfolders(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (s *stubMailClient) CreateFolder(_ context.Context, _ string, _ string) (string, error) {
	return "", nil
}

func (s *stubMailClient) RenameFolder(_ context.Context, _ string, _ string) (string, error) {
	return "", nil
}

func (s *stubMailClient) DeleteFolder(_ context.Context, _ string) error {
	return nil
}

func (s *stubMailClient) EnsureLabel(_ context.Context, _ string) error {
	return nil
}

func (s *stubMailClient) ApplyLabel(_ context.Context, _ string, _ string) error {
	return nil
}

func (s *stubMailClient) ApplyInboxAction(_ context.Context, _ string, _ string, _ string, _ string) error {
	return nil
}

func (s *stubMailClient) SaveDraft(_ context.Context, _ imapadapter.DraftMessage) error {
	return nil
}

func (s *stubMailClient) SaveSent(_ context.Context, _ imapadapter.DraftMessage) error {
	return nil
}

func newTestServer(t *testing.T) *Server {
	t.Helper()

	logDir := t.TempDir()
	stateDir := t.TempDir()

	logger, err := logging.New(logDir, "info")
	if err != nil {
		t.Fatalf("logging.New: %v", err)
	}
	t.Cleanup(func() {
		_ = logger.Close()
	})

	store, err := state.New(stateDir)
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}

	srv := NewServer(config.Default(), logger, store, health.NewService(), &stubMailClient{}, nil)
	srv.pairingSecret = "test-pairing-secret"
	return srv
}

func authRequest(s *Server, req *http.Request) {
	token := "session-token"
	s.mu.Lock()
	s.sessions[token] = time.Now().Add(24 * time.Hour)
	s.mu.Unlock()
	req.AddCookie(&http.Cookie{Name: "llama_session", Value: token})
}

func TestNativeRegisterStoresDevice(t *testing.T) {
	srv := newTestServer(t)
	subscriberID := "subscriber-1"
	token, _, err := srv.createPairingToken(subscriberID, time.Minute)
	if err != nil {
		t.Fatalf("createPairingToken: %v", err)
	}

	payload := map[string]any{
		"subscriberId":   subscriberID,
		"subscriberHash": srv.pairingSubscriberHash(subscriberID),
		"pairingToken":   token,
		"deviceToken":    "native-device-token",
		"deviceId":       "device-a",
		"platform":       "ios",
		"deviceName":     "Alice phone",
		"appVersion":     "1.2.3",
	}
	body, _ := json.Marshal(payload)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/notifications/native/register", bytes.NewReader(body))
	srv.handleNotificationNativeRegister(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	devices := srv.store.ListNativeDevices()
	if len(devices) != 1 {
		t.Fatalf("len(devices) = %d, want 1", len(devices))
	}
	if devices[0].DeviceID != "device-a" {
		t.Fatalf("device id = %q, want %q", devices[0].DeviceID, "device-a")
	}
	if devices[0].Platform != "ios" {
		t.Fatalf("platform = %q, want %q", devices[0].Platform, "ios")
	}
	if devices[0].DeviceName != "Alice phone" {
		t.Fatalf("deviceName = %q, want %q", devices[0].DeviceName, "Alice phone")
	}
}

func TestNativeRegisterRejectsInvalidPairingToken(t *testing.T) {
	srv := newTestServer(t)

	payload := map[string]any{
		"subscriberId": "subscriber-1",
		"pairingToken": "bad-token",
		"deviceToken":  "native-device-token",
	}
	body, _ := json.Marshal(payload)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/notifications/native/register", bytes.NewReader(body))
	srv.handleNotificationNativeRegister(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestNativeDevicesListAndDelete(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.store.UpsertNativeDevice(state.NativeDevice{
		DeviceID:  "device-b",
		Platform:  "android",
		PushToken: "token-b",
	}); err != nil {
		t.Fatalf("UpsertNativeDevice: %v", err)
	}

	listRec := httptest.NewRecorder()
	listReq := httptest.NewRequest(http.MethodGet, "/api/notifications/native/devices", nil)
	authRequest(srv, listReq)
	srv.withAuth(srv.handleNotificationNativeDevices).ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d", listRec.Code, http.StatusOK)
	}
	var listResp struct {
		Devices []state.NativeDevice `json:"devices"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal list response: %v", err)
	}
	if len(listResp.Devices) != 1 {
		t.Fatalf("GET devices len = %d, want 1", len(listResp.Devices))
	}

	delBody := []byte(`{"deviceId":"device-b"}`)
	delRec := httptest.NewRecorder()
	delReq := httptest.NewRequest(http.MethodDelete, "/api/notifications/native/devices", bytes.NewReader(delBody))
	authRequest(srv, delReq)
	srv.withAuth(srv.handleNotificationNativeDevices).ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want %d", delRec.Code, http.StatusOK)
	}

	devices := srv.store.ListNativeDevices()
	if len(devices) != 0 {
		t.Fatalf("len(devices) = %d, want 0", len(devices))
	}
}
