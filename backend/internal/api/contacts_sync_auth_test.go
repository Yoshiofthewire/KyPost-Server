package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// These tests cover handleContactsSync's own pairing-auth check — a
// separate inline copy from resolveMailAuthContext, not previously covered
// by a dedicated auth test.
func TestContactsSyncAcceptsSubscriberHashViaHeader(t *testing.T) {
	srv := newTestServer(t)
	store := testUserStore(t, srv)
	subscriberID, err := store.GetOrCreateSubscriberID()
	if err != nil {
		t.Fatalf("GetOrCreateSubscriberID: %v", err)
	}
	hash := srv.pairingSubscriberHash(subscriberID)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/contacts/sync", nil)
	req.Header.Set(headerSubscriberID, subscriberID)
	req.Header.Set(headerSubscriberHash, hash)
	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (pairing auth via headers should reach the handler); body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestContactsSyncAcceptsSubscriberHashViaQueryParamFallback(t *testing.T) {
	srv := newTestServer(t)
	store := testUserStore(t, srv)
	subscriberID, err := store.GetOrCreateSubscriberID()
	if err != nil {
		t.Fatalf("GetOrCreateSubscriberID: %v", err)
	}
	hash := srv.pairingSubscriberHash(subscriberID)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/contacts/sync?sub="+subscriberID+"&hash="+hash, nil)
	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (legacy query-param auth must keep working during rollout); body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestContactsSyncRejectsInvalidHash(t *testing.T) {
	srv := newTestServer(t)
	store := testUserStore(t, srv)
	subscriberID, err := store.GetOrCreateSubscriberID()
	if err != nil {
		t.Fatalf("GetOrCreateSubscriberID: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/contacts/sync", nil)
	req.Header.Set(headerSubscriberID, subscriberID)
	req.Header.Set(headerSubscriberHash, "deadbeef")
	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
