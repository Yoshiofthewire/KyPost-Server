package captcha

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewVerifierDisabledWhenNoProvider(t *testing.T) {
	v, err := NewVerifier(Config{Provider: ProviderNone})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if v != nil {
		t.Fatalf("expected nil Verifier for ProviderNone, got %#v", v)
	}
}

func TestNewVerifierRequiresSecretKey(t *testing.T) {
	for _, p := range []Provider{ProviderTurnstile, ProviderFriendly} {
		if _, err := NewVerifier(Config{Provider: p}); err == nil {
			t.Errorf("NewVerifier(%q with no secret key) should have errored", p)
		}
	}
}

func TestNewVerifierRejectsUnknownProvider(t *testing.T) {
	if _, err := NewVerifier(Config{Provider: "bogus", SecretKey: "x"}); err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func fakeSiteverify(t *testing.T, wantSecret string, success bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"success": success})
	}))
}

func TestTurnstileVerifierHonorsSiteverifyResult(t *testing.T) {
	for _, success := range []bool{true, false} {
		srv := fakeSiteverify(t, "secret", success)
		defer srv.Close()

		v := &turnstileVerifier{secretKey: "secret", verifyURL: srv.URL, client: srv.Client()}
		ok, err := v.Verify(context.Background(), "some-token", "1.2.3.4")
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if ok != success {
			t.Errorf("Verify() = %v, want %v", ok, success)
		}
	}
}

func TestTurnstileVerifierRejectsEmptyToken(t *testing.T) {
	// No server needed: an empty token must short-circuit to false without
	// making a network call at all.
	v := &turnstileVerifier{secretKey: "secret", verifyURL: "http://unused.invalid", client: http.DefaultClient}
	ok, err := v.Verify(context.Background(), "", "1.2.3.4")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if ok {
		t.Fatal("Verify(\"\") should be false")
	}
}

func TestFriendlyVerifierHonorsSiteverifyResult(t *testing.T) {
	for _, success := range []bool{true, false} {
		srv := fakeSiteverify(t, "secret", success)
		defer srv.Close()

		v := &friendlyVerifier{secretKey: "secret", verifyURL: srv.URL, client: srv.Client()}
		ok, err := v.Verify(context.Background(), "some-solution", "")
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if ok != success {
			t.Errorf("Verify() = %v, want %v", ok, success)
		}
	}
}

func TestVerifierPropagatesNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	v := &turnstileVerifier{secretKey: "secret", verifyURL: srv.URL, client: srv.Client()}
	if _, err := v.Verify(context.Background(), "token", ""); err == nil {
		t.Fatal("expected error on non-200 siteverify response")
	}
}
