package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// malformedLine is a line that fails to decode as a vCard (the BEGIN value
// isn't "VCARD") but is short enough that go-vcard's decoder consumes it and
// returns a non-EOF error in a single Decode() call, so each occurrence
// drives exactly one iteration of the import loop without ever incrementing
// cardCount. A stream of these is the pathological "malformed non-vCard
// input" the attempts cap exists to bound.
const malformedLine = "BEGIN:NOTAVCARD\r\n"

// runContactsImport POSTs body to the (authenticated) import handler and
// returns the decoded JSON result, failing the test if the handler doesn't
// respond within timeout. This guards against a regression back to a loop
// that isn't bounded independent of successful parses.
func runContactsImport(t *testing.T, srv *Server, body string, timeout time.Duration) importResultForTest {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/api/contacts/import", strings.NewReader(body))
	authRequest(srv, req)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.withAuth(srv.handleContactsImport)(rec, req)
	}()

	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatalf("handleContactsImport did not return within %s; the malformed-input loop is not properly bounded", timeout)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var result importResultForTest
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal import response: %v; body=%s", err, rec.Body.String())
	}
	return result
}

// importResultForTest mirrors the handler's unexported importResult JSON
// shape so tests can decode the response.
type importResultForTest struct {
	Imported   int      `json:"imported"`
	Skipped    int      `json:"skipped"`
	Errors     []string `json:"errors"`
	ErrorCount int      `json:"errorCount"`
}

// TestHandleContactsImport_MalformedInputTerminatesWithinAttemptsCap proves
// that a stream of entirely malformed (non-vCard) input terminates the
// import loop instead of looping for as long as the request body lasts. The
// input here (50,000 malformed lines) is far larger than any cap on
// successful parses (cardCount never increments for this input at all), so
// before the attempts cap existed nothing would break the loop early.
func TestHandleContactsImport_MalformedInputTerminatesWithinAttemptsCap(t *testing.T) {
	srv := newTestServer(t)
	srv.mustBootstrapUserID(t)

	body := strings.Repeat(malformedLine, 50000)
	result := runContactsImport(t, srv, body, 10*time.Second)

	if result.Imported != 0 {
		t.Fatalf("Imported = %d, want 0 (input never contains a valid card)", result.Imported)
	}
	// The attempts cap (maxCards*2 = 10000) must have kicked in well before
	// all 50,000 malformed lines were consumed.
	if result.ErrorCount >= 50000 {
		t.Fatalf("ErrorCount = %d, want well under 50000 (attempts cap should have stopped the loop early)", result.ErrorCount)
	}
	if result.ErrorCount < 1000 {
		t.Fatalf("ErrorCount = %d, want at least in the low thousands (attempts cap is 10000)", result.ErrorCount)
	}

	foundStopMessage := false
	for _, e := range result.Errors {
		if strings.Contains(e, "too many errors") {
			foundStopMessage = true
		}
	}
	if !foundStopMessage {
		t.Fatalf("expected an error entry noting the attempts cap was hit; got %v", result.Errors)
	}
}

// TestHandleContactsImport_ErrorsCappedButCountPreserved proves that
// result.Errors is capped near 100 entries even when many more errors occur,
// while result.ErrorCount still reports the true total so the truncation is
// communicated rather than silently dropped.
func TestHandleContactsImport_ErrorsCappedButCountPreserved(t *testing.T) {
	srv := newTestServer(t)
	srv.mustBootstrapUserID(t)

	const trueErrorCount = 300 // well above the 100 cap, well below the 10000 attempts cap
	body := strings.Repeat(malformedLine, trueErrorCount)
	result := runContactsImport(t, srv, body, 10*time.Second)

	if len(result.Errors) > 100 {
		t.Fatalf("len(result.Errors) = %d, want <= 100 (capped)", len(result.Errors))
	}
	if len(result.Errors) == trueErrorCount {
		t.Fatalf("len(result.Errors) = %d, expected the slice to be truncated below the true count", len(result.Errors))
	}
	if result.ErrorCount != trueErrorCount {
		t.Fatalf("ErrorCount = %d, want %d (true total must be preserved despite the capped Errors slice)", result.ErrorCount, trueErrorCount)
	}
}
