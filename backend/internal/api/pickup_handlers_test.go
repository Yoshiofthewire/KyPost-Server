package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// pickupMux builds a minimal ServeMux with the same route pattern server.go
// registers for the pickup page, so r.PathValue("id") resolves the way it
// would in the real server.
func pickupMux(srv *Server) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /pickup/{id}", srv.handlePickup)
	return mux
}

func TestHandlePickupHappyPath(t *testing.T) {
	srv := newTestServer(t)

	id, err := srv.pickupStore.Create("user-1", "recipient@example.com", "Hello <there>", "body & stuff", time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	token, _, err := srv.createPairingToken(id, time.Hour)
	if err != nil {
		t.Fatalf("createPairingToken: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/pickup/"+id+"?t="+token, nil)
	rec := httptest.NewRecorder()
	pickupMux(srv).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	bodyStr := rec.Body.String()
	if !strings.Contains(bodyStr, "Hello &lt;there&gt;") {
		t.Fatalf("expected HTML-escaped subject in body, got: %s", bodyStr)
	}
	if !strings.Contains(bodyStr, "body &amp; stuff") {
		t.Fatalf("expected HTML-escaped body in body, got: %s", bodyStr)
	}
}

func TestHandlePickupInvalidTokenNeverConsumesRecord(t *testing.T) {
	srv := newTestServer(t)

	id, err := srv.pickupStore.Create("user-1", "recipient@example.com", "Subject", "Body", time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/pickup/"+id+"?t=not-a-real-token", nil)
	rec := httptest.NewRecorder()
	pickupMux(srv).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}

	// The record must still be intact: an invalid token must never reach
	// pickupStore.View. Mint a real token now and confirm the record can
	// still be viewed once.
	token, _, err := srv.createPairingToken(id, time.Hour)
	if err != nil {
		t.Fatalf("createPairingToken: %v", err)
	}
	subject, body, err := srv.pickupStore.View(id)
	if err != nil {
		t.Fatalf("View after bad-token attempt should still succeed, got err: %v", err)
	}
	if subject != "Subject" || body != "Body" {
		t.Fatalf("unexpected record contents: subject=%q body=%q", subject, body)
	}
	_ = token // token minted only to demonstrate a valid one could still be built
}

func TestHandlePickupSecondViewIsGone(t *testing.T) {
	srv := newTestServer(t)

	id, err := srv.pickupStore.Create("user-1", "recipient@example.com", "Subject", "Body", time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	token, _, err := srv.createPairingToken(id, time.Hour)
	if err != nil {
		t.Fatalf("createPairingToken: %v", err)
	}

	mux := pickupMux(srv)

	firstReq := httptest.NewRequest(http.MethodGet, "/pickup/"+id+"?t="+token, nil)
	firstRec := httptest.NewRecorder()
	mux.ServeHTTP(firstRec, firstReq)
	if firstRec.Code != http.StatusOK {
		t.Fatalf("first view status = %d, want %d; body=%s", firstRec.Code, http.StatusOK, firstRec.Body.String())
	}

	secondReq := httptest.NewRequest(http.MethodGet, "/pickup/"+id+"?t="+token, nil)
	secondRec := httptest.NewRecorder()
	mux.ServeHTTP(secondRec, secondReq)
	if secondRec.Code != http.StatusGone {
		t.Fatalf("second view status = %d, want %d; body=%s", secondRec.Code, http.StatusGone, secondRec.Body.String())
	}
}

func TestHandlePickupUnknownIDIsGone(t *testing.T) {
	srv := newTestServer(t)

	// Never Create()d: a syntactically valid token for an ID that has no
	// backing record on disk.
	token, _, err := srv.createPairingToken("never-created-id", time.Hour)
	if err != nil {
		t.Fatalf("createPairingToken: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/pickup/never-created-id?t="+token, nil)
	rec := httptest.NewRecorder()
	pickupMux(srv).ServeHTTP(rec, req)

	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusGone, rec.Body.String())
	}
}
