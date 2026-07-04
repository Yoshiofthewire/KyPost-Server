package users

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrMigrateFreshInstallMintsDefaultAdmin(t *testing.T) {
	dir := t.TempDir()
	store, err := LoadOrMigrate(dir, filepath.Join(dir, "admin.env"))
	if err != nil {
		t.Fatalf("LoadOrMigrate: %v", err)
	}
	all, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("len(all) = %d, want 1", len(all))
	}
	u := all[0]
	if u.Role != RoleAdmin || !u.Active || !u.MustChangePassword {
		t.Fatalf("unexpected default admin: %+v", u)
	}
}

func TestLoadOrMigrateImportsLegacyAdminEnv(t *testing.T) {
	dir := t.TempDir()
	hash, err := HashPassword("hunter2")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	adminEnvPath := filepath.Join(dir, "admin.env")
	content := "ADMIN_USER=legacyadmin\nADMIN_PASS_HASH=" + hash + "\nMUST_CHANGE_PASSWORD=false\n"
	if err := os.WriteFile(adminEnvPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, err := LoadOrMigrate(dir, adminEnvPath)
	if err != nil {
		t.Fatalf("LoadOrMigrate: %v", err)
	}
	u, err := store.GetByUsername("legacyadmin")
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	if u.Role != RoleAdmin || !u.Active || u.MustChangePassword {
		t.Fatalf("unexpected migrated admin: %+v", u)
	}
	if !VerifyPassword(u, "hunter2") {
		t.Fatalf("VerifyPassword: expected migrated password to verify")
	}
}

func TestLoadOrMigrateIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	first, err := LoadOrMigrate(dir, filepath.Join(dir, "admin.env"))
	if err != nil {
		t.Fatalf("LoadOrMigrate: %v", err)
	}
	firstUsers, _ := first.List()

	second, err := LoadOrMigrate(dir, filepath.Join(dir, "admin.env"))
	if err != nil {
		t.Fatalf("LoadOrMigrate (second): %v", err)
	}
	secondUsers, _ := second.List()

	if len(firstUsers) != 1 || len(secondUsers) != 1 || firstUsers[0].ID != secondUsers[0].ID {
		t.Fatalf("expected the same single user across loads: first=%+v second=%+v", firstUsers, secondUsers)
	}
}

func TestStoreLifecycle(t *testing.T) {
	dir := t.TempDir()
	store, err := LoadOrMigrate(dir, filepath.Join(dir, "admin.env"))
	if err != nil {
		t.Fatalf("LoadOrMigrate: %v", err)
	}

	u, err := store.Create("alice", "correct-horse", RoleUser)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !VerifyPassword(u, "correct-horse") {
		t.Fatalf("VerifyPassword: expected new user's password to verify")
	}

	if _, err := store.Create("alice", "other", RoleUser); err != ErrUsernameTaken {
		t.Fatalf("Create duplicate: err = %v, want ErrUsernameTaken", err)
	}

	if _, err := store.SetRole(u.ID, RoleAdmin); err != nil {
		t.Fatalf("SetRole: %v", err)
	}
	got, err := store.Get(u.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Role != RoleAdmin {
		t.Fatalf("Role = %v, want admin", got.Role)
	}

	if _, err := store.SetPassword(u.ID, "new-password", true); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	got, _ = store.Get(u.ID)
	if !got.MustChangePassword || !VerifyPassword(got, "new-password") {
		t.Fatalf("unexpected state after SetPassword: %+v", got)
	}

	if _, err := store.Deactivate(u.ID); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}
	got, _ = store.Get(u.ID)
	if got.Active {
		t.Fatalf("expected deactivated user to be inactive")
	}

	if _, err := store.Reactivate(u.ID); err != nil {
		t.Fatalf("Reactivate: %v", err)
	}
	got, _ = store.Get(u.ID)
	if !got.Active {
		t.Fatalf("expected reactivated user to be active")
	}

	if _, err := store.Get("does-not-exist"); err != ErrNotFound {
		t.Fatalf("Get unknown: err = %v, want ErrNotFound", err)
	}
}
