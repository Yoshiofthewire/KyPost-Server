package pgpmail

import (
	"path/filepath"
	"testing"

	"github.com/ProtonMail/gopenpgp/v3/crypto"
)

func TestGenerateIdentity(t *testing.T) {
	id, err := GenerateIdentity("Alice", "alice@example.com")
	if err != nil {
		t.Fatalf("GenerateIdentity: %v", err)
	}
	if id.Fingerprint == "" {
		t.Fatal("expected non-empty fingerprint")
	}
	if id.ArmoredPublicKey == "" {
		t.Fatal("expected non-empty armored public key")
	}
}

func TestSealOpenPrivateKeyRoundTrip(t *testing.T) {
	id, err := GenerateIdentity("Bob", "bob@example.com")
	if err != nil {
		t.Fatalf("GenerateIdentity: %v", err)
	}
	keyPath := filepath.Join(t.TempDir(), "pgp-private-key.key")

	sealed, err := id.SealPrivateKey(keyPath)
	if err != nil {
		t.Fatalf("SealPrivateKey: %v", err)
	}
	if sealed == "" {
		t.Fatal("expected non-empty sealed envelope")
	}

	opened, err := OpenPrivateKey(sealed, keyPath)
	if err != nil {
		t.Fatalf("OpenPrivateKey: %v", err)
	}
	if opened.Fingerprint != id.Fingerprint {
		t.Fatalf("fingerprint mismatch: got %s want %s", opened.Fingerprint, id.Fingerprint)
	}
}

func TestImportIdentityWithPassphrase(t *testing.T) {
	keyGen := crypto.PGP().KeyGeneration().AddUserId("Carol", "carol@example.com").New()
	key, err := keyGen.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	locked, err := crypto.PGP().LockKey(key, []byte("s3cret"))
	if err != nil {
		t.Fatalf("LockKey: %v", err)
	}
	armoredLocked, err := locked.Armor()
	if err != nil {
		t.Fatalf("Armor: %v", err)
	}

	id, err := ImportIdentity(armoredLocked, "s3cret")
	if err != nil {
		t.Fatalf("ImportIdentity: %v", err)
	}
	if id.Fingerprint != key.GetFingerprint() {
		t.Fatalf("fingerprint mismatch: got %s want %s", id.Fingerprint, key.GetFingerprint())
	}

	if _, err := ImportIdentity(armoredLocked, "wrong-passphrase"); err == nil {
		t.Fatal("expected error unlocking with wrong passphrase")
	}
}

func TestImportIdentityRejectsPublicOnlyKey(t *testing.T) {
	id, err := GenerateIdentity("Dave", "dave@example.com")
	if err != nil {
		t.Fatalf("GenerateIdentity: %v", err)
	}
	if _, err := ImportIdentity(id.ArmoredPublicKey, ""); err == nil {
		t.Fatal("expected error importing a public-only key as a private identity")
	}
}
