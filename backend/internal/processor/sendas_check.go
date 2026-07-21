package processor

import (
	"context"
	"strings"
	"time"

	imapadapter "kypost-server/backend/internal/adapters/imap"
	"kypost-server/backend/internal/sendas"
)

// userSendAsStore returns the cached send-as alias store for a user,
// mirroring userMailCacheStore/userRulesStore — the api process
// independently constructs its own sendas.Store over the same on-disk
// send_as_aliases.json (the HTTP handlers from Task 5), so refreshFromDiskLocked
// is what keeps the two processes' in-memory views coherent, exactly as with
// state.Store.
func (p *Poller) userSendAsStore(userID string) (*sendas.Store, error) {
	p.userMu.Lock()
	defer p.userMu.Unlock()
	if st, ok := p.sendAsStores[userID]; ok {
		return st, nil
	}
	st, err := sendas.New(p.userStateDir(userID))
	if err != nil {
		return nil, err
	}
	p.sendAsStores[userID] = st
	return st, nil
}

// checkPendingSendAsAliases advances every one of userID's pending send-as
// alias verifications by one poll tick.
//
// A pending record whose ExpiresAt has already passed is marked failed and
// is never checked again — no indefinite retry, matching the feature's
// fixed 5-minute verification window.
//
// Every other pending record is checked by searching the user's own INBOX
// for a message whose subject contains the record's VerificationCode. A
// bare subject match is not sufficient on its own — it proves only that
// *some* message with that text exists in an inbox the account owner fully
// controls, which they could trivially fake themselves. Verification
// additionally requires that message's Authentication-Results header show
// a passing DKIM or SPF verdict scoped to the candidate address's own
// domain (AuthenticationResultsPassForDomain) — a verdict the account
// owner cannot forge, since it's computed by the receiving mail server
// during the real SMTP transaction. See auth_results.go for the full
// rationale.
//
// Errors from the mail client (search/fetch failures) are logged and
// leave the affected record pending for the next tick — they are not
// escalated to the caller, matching this file's general policy of never
// letting one user's IMAP trouble abort a poll tick.
func (p *Poller) checkPendingSendAsAliases(ctx context.Context, userID string, mail imapadapter.Client) {
	store, err := p.userSendAsStore(userID)
	if err != nil {
		p.log.Error("failed to open send-as store", "user_id", userID, "error", err.Error())
		return
	}

	for _, alias := range store.List() {
		if alias.Status != "pending" {
			continue
		}

		expiresAt, perr := time.Parse(time.RFC3339, alias.ExpiresAt)
		if perr != nil || !expiresAt.After(time.Now()) {
			if err := store.MarkFailed(alias.ID); err != nil {
				p.log.Error("failed to mark expired send-as alias failed",
					"user_id", userID, "alias_id", alias.ID, "error", err.Error())
			}
			continue
		}

		matches, err := mail.SearchMessages(ctx, "INBOX", "subject", alias.VerificationCode, 10)
		if err != nil {
			p.log.Error("send-as verification search failed",
				"user_id", userID, "alias_id", alias.ID, "error", err.Error())
			continue
		}
		if len(matches) == 0 {
			continue
		}

		uids := make([]int, len(matches))
		for i, m := range matches {
			uids[i] = m.UID
		}
		headers, err := mail.FetchHeaderFields(ctx, uids, "Authentication-Results")
		if err != nil {
			p.log.Error("send-as verification header fetch failed",
				"user_id", userID, "alias_id", alias.ID, "error", err.Error())
			continue
		}

		domain := domainOf(alias.Email)
		verified := false
		for _, uid := range uids {
			if imapadapter.AuthenticationResultsPassForDomain(headers[uid], domain) {
				verified = true
				break
			}
		}
		if verified {
			if err := store.MarkVerified(alias.ID); err != nil {
				p.log.Error("failed to mark send-as alias verified",
					"user_id", userID, "alias_id", alias.ID, "error", err.Error())
			}
		}
	}

	if err := store.SweepTerminal(24 * time.Hour); err != nil {
		p.log.Error("send-as terminal sweep failed", "user_id", userID, "error", err.Error())
	}
}

// domainOf returns the portion of email after '@', lowercased, or "" if
// email has no '@' — used to scope the Authentication-Results check to the
// candidate address's own domain, never the account's own.
func domainOf(email string) string {
	if i := strings.LastIndex(email, "@"); i >= 0 && i+1 < len(email) {
		return strings.ToLower(email[i+1:])
	}
	return ""
}
