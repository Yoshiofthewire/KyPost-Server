package processor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	imapadapter "kypost-server/backend/internal/adapters/imap"
	"kypost-server/backend/internal/logging"
	"kypost-server/backend/internal/sendas"
)

// stubSendAsMailClient implements imapadapter.Client by embedding the
// (nil) interface and overriding only the two methods
// checkPendingSendAsAliases calls — any other method call would panic on
// the nil embedded interface, which is fine: it means the code under test
// reached further than this test intended and should be caught, not
// silently no-op'd.
type stubSendAsMailClient struct {
	imapadapter.Client
	searchResults map[string][]imapadapter.Overview // keyed by the searched verification code
	searchErr     error
	headerResults map[int][]string
	headerErr     error
	searchCalls   []string
	headerCalls   [][]int
}

func (c *stubSendAsMailClient) SearchMessages(_ context.Context, _, _, query string, _ int) ([]imapadapter.Overview, error) {
	c.searchCalls = append(c.searchCalls, query)
	if c.searchErr != nil {
		return nil, c.searchErr
	}
	return c.searchResults[query], nil
}

func (c *stubSendAsMailClient) FetchHeaderFields(_ context.Context, uids []int, _ ...string) (map[int][]string, error) {
	c.headerCalls = append(c.headerCalls, uids)
	if c.headerErr != nil {
		return nil, c.headerErr
	}
	out := map[int][]string{}
	for _, uid := range uids {
		if lines, ok := c.headerResults[uid]; ok {
			out[uid] = lines
		}
	}
	return out, nil
}

// newTestPollerForSendAs builds a minimal *Poller sufficient to exercise
// checkPendingSendAsAliases: a logger and a stateDir so userStateDir/
// userSendAsStore work, with sendAsStores initialized as New() would do.
func newTestPollerForSendAs(t *testing.T) *Poller {
	t.Helper()
	logger, err := logging.New(t.TempDir())
	if err != nil {
		t.Fatalf("logging.New: %v", err)
	}
	t.Cleanup(func() { _ = logger.Close() })

	return &Poller{
		log:          logger,
		stateDir:     t.TempDir(),
		sendAsStores: map[string]*sendas.Store{},
	}
}

// sendAsAliasesFileForTest mirrors the unexported aliasesFile the sendas
// package persists to disk (see sendas/store.go) — used here only to
// backdate ExpiresAt/FailedAt directly on disk for boundary tests, the same
// technique sendas/store_test.go uses from inside its own package, adapted
// for use from outside the package.
type sendAsAliasesFileForTest struct {
	Aliases []sendas.Alias `json:"aliases"`
}

// backdateSendAsField rewrites a single field (by JSON round-trip through a
// map, so it doesn't matter which of Alias's string fields is targeted) of
// the alias with the given ID, directly on the on-disk send_as_aliases.json
// file, then persists it back.
func backdateSendAsField(t *testing.T, stateDir, userID, aliasID string, mutate func(a *sendas.Alias)) {
	t.Helper()
	path := filepath.Join(stateDir, "users", userID, "send_as_aliases.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read send_as_aliases.json: %v", err)
	}
	var f sendAsAliasesFileForTest
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("unmarshal send_as_aliases.json: %v", err)
	}
	found := false
	for i := range f.Aliases {
		if f.Aliases[i].ID == aliasID {
			mutate(&f.Aliases[i])
			found = true
		}
	}
	if !found {
		t.Fatalf("alias %q not found in send_as_aliases.json", aliasID)
	}
	out, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal send_as_aliases.json: %v", err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatalf("write send_as_aliases.json: %v", err)
	}
}

func TestCheckPendingSendAsAliasesMarksVerifiedOnPassingAuthResult(t *testing.T) {
	p := newTestPollerForSendAs(t)
	userID := "user-1"

	store, err := p.userSendAsStore(userID)
	if err != nil {
		t.Fatalf("userSendAsStore: %v", err)
	}
	alias, err := store.Create(userID, "candidate@example.com", "Candidate")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	mail := &stubSendAsMailClient{
		searchResults: map[string][]imapadapter.Overview{
			alias.VerificationCode: {{UID: 1}},
		},
		headerResults: map[int][]string{
			1: {"Authentication-Results: mx.example.com; dkim=pass header.d=example.com"},
		},
	}

	p.checkPendingSendAsAliases(context.Background(), userID, mail)

	got, ok := store.Get(alias.ID)
	if !ok {
		t.Fatalf("Get: alias not found")
	}
	if got.Status != "verified" {
		t.Fatalf("Status = %q, want verified", got.Status)
	}
	if got.VerifiedAt == "" {
		t.Fatalf("VerifiedAt not set")
	}
}

func TestCheckPendingSendAsAliasesStaysPendingOnFailingAuthResult(t *testing.T) {
	p := newTestPollerForSendAs(t)
	userID := "user-1"

	store, err := p.userSendAsStore(userID)
	if err != nil {
		t.Fatalf("userSendAsStore: %v", err)
	}
	alias, err := store.Create(userID, "candidate@example.com", "Candidate")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	mail := &stubSendAsMailClient{
		searchResults: map[string][]imapadapter.Overview{
			alias.VerificationCode: {{UID: 1}},
		},
		headerResults: map[int][]string{
			1: {"Authentication-Results: mx.example.com; dkim=fail header.d=wrong-domain.com"},
		},
	}

	p.checkPendingSendAsAliases(context.Background(), userID, mail)

	got, ok := store.Get(alias.ID)
	if !ok {
		t.Fatalf("Get: alias not found")
	}
	if got.Status != "pending" {
		t.Fatalf("Status = %q, want pending", got.Status)
	}
}

func TestCheckPendingSendAsAliasesStaysPendingOnNoSearchMatch(t *testing.T) {
	p := newTestPollerForSendAs(t)
	userID := "user-1"

	store, err := p.userSendAsStore(userID)
	if err != nil {
		t.Fatalf("userSendAsStore: %v", err)
	}
	alias, err := store.Create(userID, "candidate@example.com", "Candidate")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	mail := &stubSendAsMailClient{
		searchResults: map[string][]imapadapter.Overview{},
	}

	p.checkPendingSendAsAliases(context.Background(), userID, mail)

	got, ok := store.Get(alias.ID)
	if !ok {
		t.Fatalf("Get: alias not found")
	}
	if got.Status != "pending" {
		t.Fatalf("Status = %q, want pending", got.Status)
	}
	if len(mail.headerCalls) != 0 {
		t.Fatalf("headerCalls = %d, want 0 (no search match should skip header fetch)", len(mail.headerCalls))
	}
}

func TestCheckPendingSendAsAliasesMarksExpiredAsFailed(t *testing.T) {
	p := newTestPollerForSendAs(t)
	userID := "user-1"

	store, err := p.userSendAsStore(userID)
	if err != nil {
		t.Fatalf("userSendAsStore: %v", err)
	}
	alias, err := store.Create(userID, "candidate@example.com", "Candidate")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	backdateSendAsField(t, p.stateDir, userID, alias.ID, func(a *sendas.Alias) {
		a.ExpiresAt = time.Now().Add(-1 * time.Minute).UTC().Format(time.RFC3339)
	})

	mail := &stubSendAsMailClient{}

	p.checkPendingSendAsAliases(context.Background(), userID, mail)

	got, ok := store.Get(alias.ID)
	if !ok {
		t.Fatalf("Get: alias not found")
	}
	if got.Status != "failed" {
		t.Fatalf("Status = %q, want failed", got.Status)
	}
	if got.FailedAt == "" {
		t.Fatalf("FailedAt not set")
	}
	if len(mail.searchCalls) != 0 {
		t.Fatalf("searchCalls = %d, want 0 (expired record should never be searched)", len(mail.searchCalls))
	}
}

func TestCheckPendingSendAsAliasesIgnoresNonPendingRecords(t *testing.T) {
	p := newTestPollerForSendAs(t)
	userID := "user-1"

	store, err := p.userSendAsStore(userID)
	if err != nil {
		t.Fatalf("userSendAsStore: %v", err)
	}
	verified, err := store.Create(userID, "verified@example.com", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.MarkVerified(verified.ID); err != nil {
		t.Fatalf("MarkVerified: %v", err)
	}
	failed, err := store.Create(userID, "failed@example.com", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.MarkFailed(failed.ID); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	mail := &stubSendAsMailClient{}

	p.checkPendingSendAsAliases(context.Background(), userID, mail)

	if len(mail.searchCalls) != 0 {
		t.Fatalf("searchCalls = %d, want 0 (non-pending records must not be re-examined)", len(mail.searchCalls))
	}
}

func TestCheckPendingSendAsAliasesSweepsOldFailedRecords(t *testing.T) {
	p := newTestPollerForSendAs(t)
	userID := "user-1"

	store, err := p.userSendAsStore(userID)
	if err != nil {
		t.Fatalf("userSendAsStore: %v", err)
	}
	failed, err := store.Create(userID, "failed@example.com", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.MarkFailed(failed.ID); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	backdateSendAsField(t, p.stateDir, userID, failed.ID, func(a *sendas.Alias) {
		a.FailedAt = time.Now().Add(-25 * time.Hour).UTC().Format(time.RFC3339)
	})

	mail := &stubSendAsMailClient{}

	p.checkPendingSendAsAliases(context.Background(), userID, mail)

	if _, ok := store.Get(failed.ID); ok {
		t.Fatalf("Get: expected record to be swept, still present")
	}
}
