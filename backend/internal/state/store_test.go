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

func TestMarkProcessedDoesNotWipeNotificationSubscriptions(t *testing.T) {
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
		Endpoint:  "https://push.example/endpoint-3",
		Auth:      "auth-token",
		P256DH:    "p256-token",
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := serverStore.UpsertNotificationSubscription(sub); err != nil {
		t.Fatalf("UpsertNotificationSubscription: %v", err)
	}

	// Simulate daemon writing unrelated state after registration.
	if err := daemonStore.MarkProcessed("msg-123"); err != nil {
		t.Fatalf("MarkProcessed: %v", err)
	}

	reloadedStore, err := New(dir)
	if err != nil {
		t.Fatalf("New reloaded store: %v", err)
	}
	subs := reloadedStore.ListNotificationSubscriptions()
	if len(subs) != 1 {
		t.Fatalf("ListNotificationSubscriptions len = %d, want 1", len(subs))
	}
	if subs[0].Endpoint != sub.Endpoint {
		t.Fatalf("endpoint = %q, want %q", subs[0].Endpoint, sub.Endpoint)
	}
}

func TestNativeDevicesSyncAcrossStoreInstances(t *testing.T) {
	dir := t.TempDir()

	daemonStore, err := New(dir)
	if err != nil {
		t.Fatalf("New daemon store: %v", err)
	}
	serverStore, err := New(dir)
	if err != nil {
		t.Fatalf("New server store: %v", err)
	}

	device := NativeDevice{
		DeviceID:     "device-1",
		Platform:     "android",
		PushToken:    "token-1",
		RegisteredAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := serverStore.UpsertNativeDevice(device); err != nil {
		t.Fatalf("UpsertNativeDevice: %v", err)
	}

	devices := daemonStore.ListNativeDevices()
	if len(devices) != 1 {
		t.Fatalf("ListNativeDevices len = %d, want 1", len(devices))
	}
	if devices[0].DeviceID != device.DeviceID {
		t.Fatalf("deviceId = %q, want %q", devices[0].DeviceID, device.DeviceID)
	}
}

func TestSetCheckpointDoesNotWipeNativeDevices(t *testing.T) {
	dir := t.TempDir()

	daemonStore, err := New(dir)
	if err != nil {
		t.Fatalf("New daemon store: %v", err)
	}
	serverStore, err := New(dir)
	if err != nil {
		t.Fatalf("New server store: %v", err)
	}

	device := NativeDevice{
		DeviceID:     "device-2",
		Platform:     "android",
		PushToken:    "token-2",
		RegisteredAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := serverStore.UpsertNativeDevice(device); err != nil {
		t.Fatalf("UpsertNativeDevice: %v", err)
	}

	if err := daemonStore.SetCheckpoint("uid-77"); err != nil {
		t.Fatalf("SetCheckpoint: %v", err)
	}

	reloadedStore, err := New(dir)
	if err != nil {
		t.Fatalf("New reloaded store: %v", err)
	}
	if got := reloadedStore.Checkpoint(); got != "uid-77" {
		t.Fatalf("checkpoint = %q, want %q", got, "uid-77")
	}
	devices := reloadedStore.ListNativeDevices()
	if len(devices) != 1 {
		t.Fatalf("ListNativeDevices len = %d, want 1", len(devices))
	}
	if devices[0].DeviceID != device.DeviceID {
		t.Fatalf("deviceId = %q, want %q", devices[0].DeviceID, device.DeviceID)
	}
}
