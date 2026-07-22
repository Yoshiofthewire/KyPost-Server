package imap

import (
	"bytes"
	"strings"

	"github.com/emersion/go-msgauth/dkim"
)

// VerifyDKIMForDomain reports whether raw (a complete RFC 5322 message,
// headers + body) carries at least one cryptographically valid DKIM
// signature whose d= domain exactly matches domain.
//
// This replaces trusting a stored/claimed Authentication-Results header
// (which an account holder can forge into their own mailbox via IMAP
// APPEND, with zero MTA involvement) with real verification: dkim.Verify
// looks up the signing domain's public key from DNS and recomputes the
// signature over the message's canonicalized headers/body. An attacker
// without that domain's private key cannot produce a signature this
// function will accept, regardless of what headers they can write into
// their own mailbox.
//
// Exact-match on domain (no subdomain/suffix matching) — domain is always
// the candidate send-as address's own domain, and DKIM's own identity-
// alignment concept (DMARC's "relaxed" vs "strict" alignment) isn't needed
// for this one-address-at-a-time check.
func VerifyDKIMForDomain(raw []byte, domain string) bool {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" || len(raw) == 0 {
		return false
	}
	return verifyDKIMForDomainWithLookup(raw, domain, nil)
}

// verifyDKIMForDomainWithLookup is VerifyDKIMForDomain's testable core: a
// nil lookupTXT defers to dkim.Verify's own default (net.LookupTXT); tests
// in this package inject a fake lookup instead of requiring live DNS.
func verifyDKIMForDomainWithLookup(raw []byte, domain string, lookupTXT func(string) ([]string, error)) bool {
	var verifications []*dkim.Verification
	var err error
	if lookupTXT == nil {
		verifications, err = dkim.Verify(bytes.NewReader(raw))
	} else {
		verifications, err = dkim.VerifyWithOptions(bytes.NewReader(raw), &dkim.VerifyOptions{LookupTXT: lookupTXT})
	}
	if err != nil {
		// A malformed message, or dkim.ErrTooManySignatures alongside a
		// partial result — fail closed either way rather than trusting a
		// partial verification list.
		return false
	}
	for _, v := range verifications {
		if v.Err == nil && strings.EqualFold(strings.TrimSpace(v.Domain), domain) {
			return true
		}
	}
	return false
}
