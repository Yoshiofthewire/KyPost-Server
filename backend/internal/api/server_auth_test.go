package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"llama-lab/backend/internal/users"
)

func authRequestAs(s *Server, req *http.Request, userID string) {
	token := "session-token-" + userID
	s.mu.Lock()
	s.sessions[token] = Session{UserID: userID, ExpiresAt: time.Now().Add(24 * time.Hour)}
	s.mu.Unlock()
	req.AddCookie(&http.Cookie{Name: "llama_session", Value: token})
}

func doJSON(srv *Server, handler http.HandlerFunc, method, path string, payload any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	var body *bytes.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		body = bytes.NewReader(b)
	} else {
		body = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, body)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func TestLoginMeLogoutFlow(t *testing.T) {
	srv := newTestServer(t)
	all, err := srv.users.List()
	if err != nil || len(all) != 1 {
		t.Fatalf("expected exactly one bootstrap user, got %+v err=%v", all, err)
	}
	admin := all[0]

	// Wrong password is rejected.
	rec := doJSON(srv, srv.handleLogin, http.MethodPost, "/api/auth/login", map[string]string{
		"username": admin.Username,
		"password": "wrong-password",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("login with wrong password: status = %d, want 401", rec.Code)
	}

	// handleMe with no session says unauthenticated.
	rec = doJSON(srv, srv.handleMe, http.MethodGet, "/api/auth/me", nil)
	var meResp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &meResp); err != nil {
		t.Fatalf("unmarshal handleMe: %v", err)
	}
	if meResp["authenticated"] != false {
		t.Fatalf("expected unauthenticated, got %+v", meResp)
	}

	// The bootstrap admin's password is unknown to this test (it's randomly
	// generated), so create a fresh known-password user to exercise login.
	u, err := srv.users.Create("alice", "correct-horse-battery", users.RoleUser)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	rec = doJSON(srv, srv.handleLogin, http.MethodPost, "/api/auth/login", map[string]string{
		"username": u.Username,
		"password": "correct-horse-battery",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status = %d, body=%s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "llama_session" {
		t.Fatalf("expected a single llama_session cookie, got %+v", cookies)
	}
	sessionCookie := cookies[0]

	rec = doJSON(srv, srv.handleMe, http.MethodGet, "/api/auth/me", nil, sessionCookie)
	if err := json.Unmarshal(rec.Body.Bytes(), &meResp); err != nil {
		t.Fatalf("unmarshal handleMe: %v", err)
	}
	if meResp["authenticated"] != true || meResp["username"] != "alice" || meResp["role"] != string(users.RoleUser) {
		t.Fatalf("unexpected /api/auth/me payload: %+v", meResp)
	}

	// Deactivating the user must immediately invalidate their live session.
	if _, err := srv.users.Deactivate(u.ID); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}
	rec = doJSON(srv, srv.handleMe, http.MethodGet, "/api/auth/me", nil, sessionCookie)
	if err := json.Unmarshal(rec.Body.Bytes(), &meResp); err != nil {
		t.Fatalf("unmarshal handleMe: %v", err)
	}
	if meResp["authenticated"] != false {
		t.Fatalf("expected deactivated user's session to be rejected, got %+v", meResp)
	}
}

func TestChangePasswordRequiresCurrentPassword(t *testing.T) {
	srv := newTestServer(t)
	u, err := srv.users.Create("bob", "old-password", users.RoleUser)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	protected := srv.withAuth(srv.handleChangePassword)

	// Wrong old password is rejected.
	body, _ := json.Marshal(map[string]string{"oldPassword": "not-it", "newPassword": "new-password"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/password", bytes.NewReader(body))
	authRequestAs(srv, req, u.ID)
	rec := httptest.NewRecorder()
	protected(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", rec.Code, rec.Body.String())
	}

	// Correct old password succeeds and the new password takes effect.
	body, _ = json.Marshal(map[string]string{"oldPassword": "old-password", "newPassword": "new-password"})
	req = httptest.NewRequest(http.MethodPost, "/api/auth/password", bytes.NewReader(body))
	authRequestAs(srv, req, u.ID)
	rec = httptest.NewRecorder()
	protected(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	got, err := srv.users.Get(u.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !users.VerifyPassword(got, "new-password") {
		t.Fatalf("expected new password to verify")
	}
}
