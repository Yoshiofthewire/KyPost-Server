package processor

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"llama-lab/backend/internal/state"
)

func TestFCMSenderSendSuccess(t *testing.T) {
	var seenAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"test-access-token","expires_in":3600}`))
		case "/send":
			seenAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"name":"projects/demo/messages/123"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	serviceAccountPath := writeTestServiceAccountFile(t, ts.URL+"/token")
	t.Setenv("FCM_SERVICE_ACCOUNT_FILE", serviceAccountPath)
	t.Setenv("FCM_PROJECT_ID", "demo-project")
	t.Setenv("FCM_SEND_URL", ts.URL+"/send")

	sender := newFCMSenderFromEnv(nil)
	if sender == nil {
		t.Fatal("newFCMSenderFromEnv(nil) returned nil")
	}
	sender.client = ts.Client()

	err := sender.Send(context.Background(), state.NativeDevice{PushToken: "device-token"}, NativePushMessage{Title: "Title", Body: "Body", Data: map[string]string{"messageId": "m1"}})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if seenAuth != "Bearer test-access-token" {
		t.Fatalf("authorization header = %q, want %q", seenAuth, "Bearer test-access-token")
	}
}

func TestFCMSenderSendReturnsStaleError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"test-access-token","expires_in":3600}`))
		case "/send":
			w.WriteHeader(http.StatusNotFound)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"error":{"status":"NOT_FOUND","message":"Requested entity was not found.","details":[{"errorCode":"UNREGISTERED"}]}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	serviceAccountPath := writeTestServiceAccountFile(t, ts.URL+"/token")
	t.Setenv("FCM_SERVICE_ACCOUNT_FILE", serviceAccountPath)
	t.Setenv("FCM_PROJECT_ID", "demo-project")
	t.Setenv("FCM_SEND_URL", ts.URL+"/send")

	sender := newFCMSenderFromEnv(nil)
	if sender == nil {
		t.Fatal("newFCMSenderFromEnv(nil) returned nil")
	}
	sender.client = ts.Client()

	err := sender.Send(context.Background(), state.NativeDevice{PushToken: "device-token"}, NativePushMessage{Title: "Title", Body: "Body"})
	if !errors.Is(err, ErrNativeDeviceStale) {
		t.Fatalf("Send() error = %v, want ErrNativeDeviceStale", err)
	}
}

func writeTestServiceAccountFile(t *testing.T, tokenURL string) string {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("x509.MarshalPKCS8PrivateKey: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})

	payload := map[string]any{
		"project_id":   "demo-project",
		"private_key":  string(pemBytes),
		"client_email": "demo@test.iam.gserviceaccount.com",
		"token_uri":    tokenURL,
	}
	content, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	path := filepath.Join(t.TempDir(), "service-account.json")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
	return path
}
