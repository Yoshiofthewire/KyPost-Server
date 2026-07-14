package pgpmail

import (
	"path/filepath"
	"testing"
	"time"
)

func TestPickupStoreCreateAndViewOnce(t *testing.T) {
	dir := t.TempDir()
	store := NewPickupStore(filepath.Join(dir, "pickup"), filepath.Join(dir, "pickup.key"))

	id, err := store.Create("user-1", "bob@example.com", "Hello", "secret body", time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	subject, body, err := store.View(id)
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if subject != "Hello" || body != "secret body" {
		t.Fatalf("unexpected view result: subject=%q body=%q", subject, body)
	}

	if _, _, err := store.View(id); err != ErrPickupExpired {
		t.Fatalf("expected ErrPickupExpired on second view, got %v", err)
	}
}

func TestPickupStoreViewUnknownID(t *testing.T) {
	dir := t.TempDir()
	store := NewPickupStore(filepath.Join(dir, "pickup"), filepath.Join(dir, "pickup.key"))
	if _, _, err := store.View("does-not-exist"); err != ErrPickupNotFound {
		t.Fatalf("expected ErrPickupNotFound, got %v", err)
	}
}

func TestPickupStoreExpiresByTTL(t *testing.T) {
	dir := t.TempDir()
	store := NewPickupStore(filepath.Join(dir, "pickup"), filepath.Join(dir, "pickup.key"))

	id, err := store.Create("user-1", "bob@example.com", "Hello", "secret body", -time.Minute)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, _, err := store.View(id); err != ErrPickupExpired {
		t.Fatalf("expected ErrPickupExpired for a record created already-expired, got %v", err)
	}
}

func TestPickupStoreSweepRemovesOldRecords(t *testing.T) {
	dir := t.TempDir()
	baseDir := filepath.Join(dir, "pickup")
	store := NewPickupStore(baseDir, filepath.Join(dir, "pickup.key"))

	id, err := store.Create("user-1", "bob@example.com", "Hello", "secret body", time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Sweep(0); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if _, _, err := store.View(id); err != ErrPickupNotFound {
		t.Fatalf("expected record swept (not found), got %v", err)
	}
}
