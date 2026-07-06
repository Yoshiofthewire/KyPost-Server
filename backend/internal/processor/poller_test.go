package processor

import (
	"testing"

	imapadapter "llama-lab/backend/internal/adapters/imap"
	"llama-lab/backend/internal/config"
)

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
