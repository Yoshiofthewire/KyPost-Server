package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPairingCredentialsFromRequest_HeadersOnly(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/inbox", nil)
	req.Header.Set(headerSubscriberID, "sub-1")
	req.Header.Set(headerSubscriberHash, "ABCDEF")

	id, hash := pairingCredentialsFromRequest(req)
	if id != "sub-1" {
		t.Fatalf("subscriberID = %q, want %q", id, "sub-1")
	}
	if hash != "abcdef" {
		t.Fatalf("subscriberHash = %q, want %q (lowercased)", hash, "abcdef")
	}
}

func TestPairingCredentialsFromRequest_QueryParamFallback(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/inbox?sub=sub-2&hash=DEADBEEF", nil)

	id, hash := pairingCredentialsFromRequest(req)
	if id != "sub-2" {
		t.Fatalf("subscriberID = %q, want %q", id, "sub-2")
	}
	if hash != "deadbeef" {
		t.Fatalf("subscriberHash = %q, want %q (lowercased)", hash, "deadbeef")
	}
}

func TestPairingCredentialsFromRequest_HeadersTakePrecedenceOverQuery(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/inbox?sub=query-sub&hash=queryhash", nil)
	req.Header.Set(headerSubscriberID, "header-sub")
	req.Header.Set(headerSubscriberHash, "headerhash")

	id, hash := pairingCredentialsFromRequest(req)
	if id != "header-sub" {
		t.Fatalf("subscriberID = %q, want %q (header should win)", id, "header-sub")
	}
	if hash != "headerhash" {
		t.Fatalf("subscriberHash = %q, want %q (header should win)", hash, "headerhash")
	}
}

func TestPairingCredentialsFromRequest_NeitherPresent(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/inbox", nil)

	id, hash := pairingCredentialsFromRequest(req)
	if id != "" || hash != "" {
		t.Fatalf("subscriberID=%q subscriberHash=%q, want both empty", id, hash)
	}
}
