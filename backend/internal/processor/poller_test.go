package processor

import (
	"testing"

	imapadapter "llama-lab/backend/internal/adapters/imap"
	"llama-lab/backend/internal/config"
)

// TestMailCacheEntriesFromMessages covers the pure conversion tickUser uses
// to opportunistically warm the mail cache with what ListUnreadInbox just
// fetched for classification (poller.go). Full tickUser integration
// (constructing a Poller against a fake IMAP dialer) isn't covered here —
// this codebase has no fake-goimap-Dialer test infrastructure, matching the
// same gap noted for adapters/imap's ListOverviews/GetMessageBodies.
func TestMailCacheEntriesFromMessages(t *testing.T) {
	messages := []imapadapter.Message{
		{
			ID: "42", Subject: "Invoice", Sender: "alice@example.com", SentTo: "me@example.com",
			CC: "cc@example.com", BCC: "bcc@example.com", Keywords: []string{"Work"},
			AtUTC: "2026-01-01T00:00:00Z", Body: "the body",
		},
		// Malformed IDs (shouldn't happen in practice, since imap.Message.ID
		// is always strconv.Itoa(uid)) must be skipped, not panic or produce
		// a garbage UID.
		{ID: "not-a-number", Subject: "bad"},
	}

	entries := mailCacheEntriesFromMessages(messages)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (malformed ID skipped), got %d: %+v", len(entries), entries)
	}

	e := entries[0]
	if e.UID != 42 || e.MessageID != "42" {
		t.Fatalf("expected uid/messageId 42, got %+v", e)
	}
	if e.Subject != "Invoice" || e.Sender != "alice@example.com" || e.SentTo != "me@example.com" {
		t.Fatalf("expected envelope fields carried over, got %+v", e)
	}
	if e.CC != "cc@example.com" || e.BCC != "bcc@example.com" {
		t.Fatalf("expected CC/BCC carried over, got %+v", e)
	}
	if len(e.Keywords) != 1 || e.Keywords[0] != "Work" {
		t.Fatalf("expected keywords carried over, got %+v", e.Keywords)
	}
	if e.Body != "the body" {
		t.Fatalf("expected body carried over so the classic cache-first path can serve it, got %q", e.Body)
	}
	// ListUnreadInbox only ever returns messages matching an IMAP UNSEEN
	// search, so Status is always "unread" regardless of flags.
	if e.Status != "unread" {
		t.Fatalf("expected status always unread, got %q", e.Status)
	}
}

func TestMailCacheEntriesFromMessages_EmptyInput(t *testing.T) {
	entries := mailCacheEntriesFromMessages(nil)
	if len(entries) != 0 {
		t.Fatalf("expected no entries for empty input, got %+v", entries)
	}
}

func TestBuildNativeNotificationText(t *testing.T) {
	tests := []struct {
		name      string
		msg       imapadapter.Message
		wantTitle string
		wantBody  string
	}{
		{
			name:      "sender and subject",
			msg:       imapadapter.Message{Sender: "alice@example.com", Subject: "Invoice #42"},
			wantTitle: "alice@example.com",
			wantBody:  "Invoice #42",
		},
		{
			name:      "missing subject",
			msg:       imapadapter.Message{Sender: "bob@example.com"},
			wantTitle: "bob@example.com",
			wantBody:  "You have a new email.",
		},
		{
			name:      "missing sender",
			msg:       imapadapter.Message{Subject: "Meeting notes"},
			wantTitle: "New Email",
			wantBody:  "Meeting notes",
		},
		{
			name:      "empty message",
			msg:       imapadapter.Message{},
			wantTitle: "New Email",
			wantBody:  "You have a new email.",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			title, body := buildNativeNotificationText(tc.msg)
			if title != tc.wantTitle || body != tc.wantBody {
				t.Fatalf("buildNativeNotificationText() = (%q, %q), want (%q, %q)", title, body, tc.wantTitle, tc.wantBody)
			}
		})
	}
}

func TestBuildNativePushData(t *testing.T) {
	tests := []struct {
		name     string
		msg      imapadapter.Message
		keywords []string
		title    string
		body     string
		want     map[string]string
	}{
		{
			name:     "populated message and keywords",
			msg:      imapadapter.Message{ID: " 123 ", Sender: " alice@example.com ", Subject: " Invoice #42 "},
			keywords: []string{"work", "billing"},
			title:    "alice@example.com",
			body:     "Invoice #42",
			want: map[string]string{
				"messageId":    "123",
				"sender":       "alice@example.com",
				"subject":      "Invoice #42",
				"senderName":   "alice@example.com",
				"emailSubject": "Invoice #42",
				"Keywords":     "work,billing",
				"title":        "alice@example.com",
				"body":         "Invoice #42",
				"url":          "/read",
			},
		},
		{
			name:     "nil keywords produce empty string, not panic",
			msg:      imapadapter.Message{ID: "1", Sender: "bob@example.com", Subject: "Hi"},
			keywords: nil,
			title:    "bob@example.com",
			body:     "Hi",
			want: map[string]string{
				"messageId":    "1",
				"sender":       "bob@example.com",
				"subject":      "Hi",
				"senderName":   "bob@example.com",
				"emailSubject": "Hi",
				"Keywords":     "",
				"title":        "bob@example.com",
				"body":         "Hi",
				"url":          "/read",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildNativePushData(tc.msg, tc.keywords, tc.title, tc.body)
			for key, want := range tc.want {
				if got[key] != want {
					t.Errorf("buildNativePushData()[%q] = %q, want %q", key, got[key], want)
				}
			}
			if len(got) != len(tc.want) {
				t.Errorf("buildNativePushData() has %d keys, want %d: %v", len(got), len(tc.want), got)
			}
		})
	}
}

func TestShouldSendNotification(t *testing.T) {
	tests := []struct {
		name          string
		settings      config.UserNotificationSettings
		selectedLabel string
		keywords      []string
		want          bool
	}{
		{
			name:     "none mode never sends",
			settings: config.UserNotificationSettings{Mode: "none", Keywords: []string{"Urgent"}},
			want:     false,
		},
		{
			name:     "all mode always sends",
			settings: config.UserNotificationSettings{Mode: "all"},
			want:     true,
		},
		{
			name:          "keywords mode matches selected label",
			settings:      config.UserNotificationSettings{Mode: "keywords", Keywords: []string{"Urgent"}},
			selectedLabel: "urgent",
			want:          true,
		},
		{
			name:     "keywords mode matches mapped keyword",
			settings: config.UserNotificationSettings{Mode: "keywords", Keywords: []string{"billing"}},
			keywords: []string{"Invoices", "Billing"},
			want:     true,
		},
		{
			name:          "keywords mode does not match when nothing selected",
			settings:      config.UserNotificationSettings{Mode: "keywords", Keywords: []string{"urgent"}},
			selectedLabel: "support",
			keywords:      []string{"helpdesk"},
			want:          false,
		},
		{
			name:          "keywords mode does not send when uncategorized",
			settings:      config.UserNotificationSettings{Mode: "keywords", Keywords: []string{"urgent"}},
			selectedLabel: "",
			keywords:      nil,
			want:          false,
		},
		{
			name:          "keywords mode sends from selected label before mailbox keyword readback",
			settings:      config.UserNotificationSettings{Mode: "keywords", Keywords: []string{"urgent"}},
			selectedLabel: "urgent",
			keywords:      nil,
			want:          true,
		},
		{
			name:          "all mode sends even when uncategorized",
			settings:      config.UserNotificationSettings{Mode: "all"},
			selectedLabel: "",
			keywords:      nil,
			want:          true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldSendNotification(tc.settings, tc.selectedLabel, tc.keywords); got != tc.want {
				t.Fatalf("shouldSendNotification() = %v, want %v", got, tc.want)
			}
		})
	}
}
