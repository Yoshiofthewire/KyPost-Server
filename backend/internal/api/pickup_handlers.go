package api

import (
	"fmt"
	"html"
	"net/http"
	"net/smtp"
	"strings"
	"time"

	"llama-lab/backend/internal/mailmsg"
)

// pickupLinkTTL is how long a pickup link stays valid if never viewed —
// "expire after N days or first view, whichever comes first."
const pickupLinkTTL = 7 * 24 * time.Hour

// handlePickup serves the one-time, unauthenticated pickup page for a
// message sent to a recipient with no known PGP key. It is registered
// directly on the mux without withAuth: the recipient has no account, only
// the signed token in the link.
func (s *Server) handlePickup(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	token := strings.TrimSpace(r.URL.Query().Get("t"))
	if id == "" || token == "" {
		http.Error(w, "invalid pickup link", http.StatusBadRequest)
		return
	}
	if err := s.validatePairingToken(id, token, time.Now()); err != nil {
		http.Error(w, "this link is invalid or has expired", http.StatusForbidden)
		return
	}

	subject, body, err := s.pickupStore.View(id)
	if err != nil {
		http.Error(w, "this message has already been viewed or has expired", http.StatusGone)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><title>%s</title></head>`+
		`<body style="font-family:sans-serif;max-width:640px;margin:40px auto;padding:0 16px">`+
		`<h1>%s</h1><pre style="white-space:pre-wrap;font-family:inherit">%s</pre>`+
		`<p style="color:#666">This message has now been marked as viewed and cannot be retrieved again.</p>`+
		`</body></html>`,
		html.EscapeString(subject), html.EscapeString(subject), html.EscapeString(body))
}

// sendPickupNotification creates a pickup record for one recipient with no
// known PGP key and sends them a short, unencrypted email with a link to
// retrieve the real message once. Consumed by Task 6's send-path
// integration for every recipient in the "without key" set of an encrypted
// send.
func (s *Server) sendPickupNotification(userID, from, recipient, subject, plainBody, smtpHost string, smtpPort int, addr, smtpUsername, smtpPassword string) error {
	id, err := s.pickupStore.Create(userID, recipient, subject, plainBody, pickupLinkTTL)
	if err != nil {
		return fmt.Errorf("create pickup record: %w", err)
	}
	token, _, err := s.createPairingToken(id, pickupLinkTTL)
	if err != nil {
		return fmt.Errorf("create pickup token: %w", err)
	}

	link := fmt.Sprintf("%s/pickup/%s?t=%s", s.pickupBaseURL(), id, token)
	notice := mailmsg.Message{
		From:    from,
		To:      []string{recipient},
		Subject: "Encrypted message waiting for you: " + subject,
		Body: "You've received a message that was sent encrypted. " +
			"Since we don't have a PGP key on file for your address, " +
			"you can read it once, securely, at the link below:\n\n" + link +
			"\n\nThis link expires in 7 days or as soon as it's opened, whichever comes first.",
		Mode: "plain",
	}.Build()

	recipients := []string{recipient}
	if smtpPort == 465 {
		return smtpSendWithImplicitTLS(smtpHost, smtpPort, smtpUsername, smtpPassword, from, recipients, notice, 45*time.Second)
	}
	auth := smtp.PlainAuth("", smtpUsername, smtpPassword, smtpHost)
	return smtpSendWithTimeout(addr, auth, from, recipients, notice, 45*time.Second)
}

// pickupBaseURL is the externally-reachable base URL used to build pickup
// links, preferring the explicit SERVER_BASE_URL override — pickup
// notification emails are sent outside any HTTP request context, so
// externalBaseURL's X-Forwarded-* header trick isn't available here.
func (s *Server) pickupBaseURL() string {
	if s.serverBaseURL != "" {
		return s.serverBaseURL
	}
	return "http://localhost:5866"
}
