package imap

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-msgauth/dkim"
)

const testMailHeaders = "From: alice@example.com\r\n" +
	"To: bob@example.net\r\n" +
	"Subject: hello\r\n" +
	"Date: Fri, 11 Jul 2003 21:00:37 -0700 (PDT)\r\n" +
	"Message-ID: <test@example.com>\r\n"

const testMailBody = "This is a perfectly ordinary test message.\r\n"

const testMail = testMailHeaders + "\r\n" + testMailBody

// dkimTestFixture holds a freshly generated RSA key pair and a matching DNS
// TXT record string, plus a lookupTXT function that serves it only for the
// expected selector/domain — used to sign and verify messages hermetically,
// with no live network access.
type dkimTestFixture struct {
	key      *rsa.PrivateKey
	domain   string
	selector string
	txt      string
}

func newDKIMTestFixture(t *testing.T, domain, selector string) *dkimTestFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pubBytes, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}
	txt := "v=DKIM1; k=rsa; p=" + base64.StdEncoding.EncodeToString(pubBytes)
	return &dkimTestFixture{key: key, domain: domain, selector: selector, txt: txt}
}

// lookupTXT serves this fixture's TXT record only for its own
// selector._domainkey.domain query, and errors for anything else — so a
// test asserting a wrong-domain or wrong-key scenario can't accidentally
// pass by falling through to some other fixture's record.
func (f *dkimTestFixture) lookupTXT(name string) ([]string, error) {
	want := f.selector + "._domainkey." + f.domain
	if name != want {
		return nil, errors.New("no such TXT record: " + name)
	}
	return []string{f.txt}, nil
}

func (f *dkimTestFixture) sign(t *testing.T, mail string, expiration time.Time) []byte {
	t.Helper()
	var b bytes.Buffer
	err := dkim.Sign(&b, strings.NewReader(mail), &dkim.SignOptions{
		Domain:     f.domain,
		Selector:   f.selector,
		Signer:     f.key,
		Hash:       crypto.SHA256,
		Expiration: expiration,
	})
	if err != nil {
		t.Fatalf("dkim.Sign: %v", err)
	}
	return b.Bytes()
}

func TestVerifyDKIMForDomainWithLookup_ValidSignaturePasses(t *testing.T) {
	f := newDKIMTestFixture(t, "example.com", "sel1")
	signed := f.sign(t, testMail, time.Time{})

	if !verifyDKIMForDomainWithLookup(signed, "example.com", f.lookupTXT) {
		t.Fatal("expected a genuinely valid, freshly-signed message to verify")
	}
}

func TestVerifyDKIMForDomainWithLookup_WrongDomainFails(t *testing.T) {
	f := newDKIMTestFixture(t, "example.com", "sel1")
	signed := f.sign(t, testMail, time.Time{})

	if verifyDKIMForDomainWithLookup(signed, "attacker.example", f.lookupTXT) {
		t.Fatal("expected a valid signature for example.com to NOT satisfy a check for a different domain")
	}
}

func TestVerifyDKIMForDomainWithLookup_TamperedBodyFails(t *testing.T) {
	f := newDKIMTestFixture(t, "example.com", "sel1")
	signed := f.sign(t, testMail, time.Time{})

	tampered := bytes.Replace(signed, []byte("perfectly ordinary"), []byte("PERFECTLY ORDINARY"), 1)
	if bytes.Equal(tampered, signed) {
		t.Fatal("test setup invalid: tamper replacement did not change the message")
	}

	if verifyDKIMForDomainWithLookup(tampered, "example.com", f.lookupTXT) {
		t.Fatal("expected a body-tampered message to fail DKIM verification")
	}
}

func TestVerifyDKIMForDomainWithLookup_ExpiredSignatureFails(t *testing.T) {
	f := newDKIMTestFixture(t, "example.com", "sel1")
	signed := f.sign(t, testMail, time.Now().Add(-time.Hour))

	if verifyDKIMForDomainWithLookup(signed, "example.com", f.lookupTXT) {
		t.Fatal("expected an expired signature to fail verification")
	}
}

func TestVerifyDKIMForDomainWithLookup_NoSignaturePresentFails(t *testing.T) {
	if verifyDKIMForDomainWithLookup([]byte(testMail), "example.com", func(string) ([]string, error) {
		t.Fatal("lookupTXT must not be called when there is no DKIM-Signature header at all")
		return nil, nil
	}) {
		t.Fatal("expected an unsigned message to fail verification")
	}
}

func TestVerifyDKIMForDomainWithLookup_WrongPublishedKeyFails(t *testing.T) {
	f := newDKIMTestFixture(t, "example.com", "sel1")
	signed := f.sign(t, testMail, time.Time{})

	// A different fixture's TXT record (i.e. a different, unrelated public
	// key) is what actually gets published at the same DNS name — the
	// signature was produced with f's private key, so it must not verify
	// against a different key.
	other := newDKIMTestFixture(t, "example.com", "sel1")
	if verifyDKIMForDomainWithLookup(signed, "example.com", other.lookupTXT) {
		t.Fatal("expected verification against a different published key to fail")
	}
}

func TestVerifyDKIMForDomainWithLookup_EmptyInputsFailClosed(t *testing.T) {
	f := newDKIMTestFixture(t, "example.com", "sel1")
	signed := f.sign(t, testMail, time.Time{})

	if verifyDKIMForDomainWithLookup(nil, "example.com", f.lookupTXT) {
		t.Fatal("expected nil raw message to fail")
	}
	if verifyDKIMForDomainWithLookup(signed, "", f.lookupTXT) {
		t.Fatal("expected empty domain to fail")
	}
}

// TestVerifyDKIMForDomain_PublicEntrypointFailsWithoutNetwork is a light
// smoke test of the exported VerifyDKIMForDomain wrapper (which always uses
// dkim.Verify's real net.LookupTXT default, unreachable from tests without
// a real DNS-published key): an unsigned message must still fail closed
// without making any network call at all, since there is no DKIM-Signature
// header to even attempt a lookup for.
func TestVerifyDKIMForDomain_PublicEntrypointFailsWithoutNetwork(t *testing.T) {
	if VerifyDKIMForDomain([]byte(testMail), "example.com") {
		t.Fatal("expected an unsigned message to fail via the public entrypoint")
	}
}
