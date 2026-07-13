package api

import "testing"

func TestNormalizeNativeTransport(t *testing.T) {
	tests := []struct {
		name      string
		transport string
		platform  string
		want      string
	}{
		// Explicit transports are returned as-is.
		{"fcm explicit", "fcm", "android", "fcm"},
		{"apns explicit", "apns", "android", "apns"},
		{"unifiedpush explicit", "unifiedpush", "android", "unifiedpush"},

		// Empty transport is derived from platform.
		{"empty to apns via ios", "", "ios", "apns"},
		{"empty to apns via macos", "", "macos", "apns"},
		{"empty to fcm via android", "", "android", "fcm"},
		{"empty to fcm via unknown", "", "windows", "fcm"},

		// Case-insensitivity.
		{"FCM uppercase", "FCM", "android", "fcm"},
		{"APNS uppercase", "APNS", "ios", "apns"},
		{"UnifiedPush mixed case", "UnifiedPush", "android", "unifiedpush"},

		// Whitespace handling.
		{"fcm with spaces", "  fcm  ", "android", "fcm"},
		{"ios platform with spaces", "", "  ios  ", "apns"},

		// Invalid explicit transports default to fcm.
		{"invalid explicit", "bogus", "android", "fcm"},

		// Case-insensitive platform derivation.
		{"ios mixed case derivation", "", "IOS", "apns"},
		{"MacOS mixed case derivation", "", "MacOS", "apns"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeNativeTransport(tt.transport, tt.platform)
			if got != tt.want {
				t.Errorf("normalizeNativeTransport(%q, %q) = %q, want %q", tt.transport, tt.platform, got, tt.want)
			}
		})
	}
}
