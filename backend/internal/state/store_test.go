package state

import (
	"testing"
	"time"
)

func TestNotificationSubscriptionsSyncAcrossStoreInstances(t *testing.T) {
	dir := t.TempDir()

	daemonStore, err := New(dir)
	if err != nil {
		t.Fatalf("New daemon store: %v", err)
	}
	serverStore, err := New(dir)
	if err != nil {
		t.Fatalf("New server store: %v", err)
	}

	sub := NotificationSubscription{
		Endpoint:  "https://push.example/endpoint-1",
		Auth:      "auth-token",
		P256DH:    "p256-token",
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := serverStore.UpsertNotificationSubscription(sub); err != nil {
		t.Fatalf("UpsertNotificationSubscription: %v", err)
	}

	subs := daemonStore.ListNotificationSubscriptions()
	if len(subs) != 1 {
		t.Fatalf("ListNotificationSubscriptions len = %d, want 1", len(subs))
	}
	if subs[0].Endpoint != sub.Endpoint {
		t.Fatalf("endpoint = %q, want %q", subs[0].Endpoint, sub.Endpoint)
	}
}

func TestUpsertNotificationSubscriptionPreservesLatestSharedState(t *testing.T) {
	dir := t.TempDir()

	daemonStore, err := New(dir)
	if err != nil {
		t.Fatalf("New daemon store: %v", err)
	}
	serverStore, err := New(dir)
	if err != nil {
		t.Fatalf("New server store: %v", err)
	}

	if err := daemonStore.SetCheckpoint("uid-42"); err != nil {
		t.Fatalf("SetCheckpoint: %v", err)
	}

	sub := NotificationSubscription{
		Endpoint:  "https://push.example/endpoint-2",
		Auth:      "auth-token",
		P256DH:    "p256-token",
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := serverStore.UpsertNotificationSubscription(sub); err != nil {
		t.Fatalf("UpsertNotificationSubscription: %v", err)
	}

	reloadedStore, err := New(dir)
	if err != nil {
		t.Fatalf("New reloaded store: %v", err)
	}
	if got := reloadedStore.Checkpoint(); got != "uid-42" {
		t.Fatalf("checkpoint = %q, want %q", got, "uid-42")
	}
}
