package mailmsg

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"os"
	"strings"
	"time"

	"kypost-server/backend/internal/config"
	"kypost-server/backend/internal/cryptutil"
)

// IMAPConfigPayload is a user's stored IMAP/SMTP mail credentials, encrypted
// at rest on disk. Moved here (from package api) so the SMTP-send helpers
// below — used by both the API's outbound-send handlers and the mail
// poller's own notification path — can share one definition without an
// api->processor->api import cycle.
type IMAPConfigPayload struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	Mailbox   string `json:"mailbox"`
	SMTPHost  string `json:"smtpHost,omitempty"`
	SMTPPort  int    `json:"smtpPort,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// NormalizeIMAPPayload applies default values and trimming to an IMAP config
// payload.
func NormalizeIMAPPayload(p IMAPConfigPayload) IMAPConfigPayload {
	p.Host = strings.TrimSpace(p.Host)
	p.Username = strings.TrimSpace(p.Username)
	p.Password = strings.TrimSpace(p.Password)
	p.Mailbox = strings.TrimSpace(p.Mailbox)
	p.SMTPHost = strings.TrimSpace(p.SMTPHost)
	if p.Port <= 0 {
		p.Port = 993
	}
	if p.Mailbox == "" {
		p.Mailbox = "INBOX"
	}
	if p.SMTPHost != "" && p.SMTPPort <= 0 {
		p.SMTPPort = 587
	}
	return p
}

func deriveSMTPHost(imapHost string) string {
	host := strings.TrimSpace(imapHost)
	if host == "" {
		return ""
	}
	lower := strings.ToLower(host)
	if strings.HasPrefix(lower, "imap.") {
		return "smtp." + host[len("imap."):]
	}
	if strings.Contains(lower, ".imap.") {
		return strings.Replace(host, ".imap.", ".smtp.", 1)
	}
	return host
}

// ResolveSMTPTarget derives the SMTP host/port/address to use for a user's
// outbound mail from their stored IMAP config, applying the same fallback
// chain every outbound-send call site needs: the payload's own SMTPHost/
// SMTPPort, then SMTP_HOST/SMTP_PORT env vars, then a heuristic derived from
// the IMAP host, then a hardcoded default port of 587. Returns an error
// (rather than picking a call-site-specific HTTP status) when no host can be
// determined at all — callers translate that into whatever response is
// appropriate for their context.
func ResolveSMTPTarget(payload IMAPConfigPayload) (smtpHost string, smtpPort int, addr string, err error) {
	smtpHost = strings.TrimSpace(payload.SMTPHost)
	if smtpHost == "" {
		smtpHost = strings.TrimSpace(config.EnvOrDefault("SMTP_HOST", ""))
	}
	if smtpHost == "" {
		smtpHost = deriveSMTPHost(payload.Host)
	}
	if smtpHost == "" {
		return "", 0, "", fmt.Errorf("smtp host is not configured")
	}
	smtpPort = payload.SMTPPort
	if smtpPort <= 0 {
		smtpPort = config.EnvInt("SMTP_PORT", 587)
	}
	if smtpPort <= 0 {
		smtpPort = 587
	}
	return smtpHost, smtpPort, fmt.Sprintf("%s:%d", smtpHost, smtpPort), nil
}

// SMTPSendWithTimeout wraps smtp.SendMail with a hard timeout, since the
// standard library call has none of its own and would otherwise be able to
// hang a request indefinitely on an unresponsive server.
func SMTPSendWithTimeout(addr string, auth smtp.Auth, from string, recipients []string, msg []byte, timeout time.Duration) error {
	result := make(chan error, 1)
	go func() {
		result <- smtp.SendMail(addr, auth, from, recipients, msg)
	}()

	select {
	case err := <-result:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("smtp send timed out after %s", timeout)
	}
}

// SMTPSendWithImplicitTLS delivers msg over an implicit-TLS (port 465 style)
// SMTP connection, since net/smtp only supports STARTTLS natively.
func SMTPSendWithImplicitTLS(host string, port int, username, password, from string, recipients []string, msg []byte, timeout time.Duration) error {
	addr := fmt.Sprintf("%s:%d", host, port)
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	if err != nil {
		return err
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(timeout))

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer client.Close()

	if ok, _ := client.Extension("AUTH"); ok {
		auth := smtp.PlainAuth("", username, password, host)
		if err := client.Auth(auth); err != nil {
			return err
		}
	}

	if err := client.Mail(from); err != nil {
		return err
	}
	for _, recipient := range recipients {
		if err := client.Rcpt(recipient); err != nil {
			return err
		}
	}

	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(msg); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	if err := client.Quit(); err != nil {
		return err
	}
	return nil
}

// SMTPDeliver sends msg over SMTP to recipients, choosing implicit TLS (port
// 465) or STARTTLS/plain auth otherwise.
func SMTPDeliver(smtpHost string, smtpPort int, addr, smtpUsername, smtpPassword, from string, recipients []string, msg []byte) error {
	if smtpPort == 465 {
		return SMTPSendWithImplicitTLS(smtpHost, smtpPort, smtpUsername, smtpPassword, from, recipients, msg, 45*time.Second)
	}
	auth := smtp.PlainAuth("", smtpUsername, smtpPassword, smtpHost)
	return SMTPSendWithTimeout(addr, auth, from, recipients, msg, 45*time.Second)
}

// decryptEncryptedPayload reverses the AES-GCM envelope api.writeEncryptedPayload
// (and the equivalent private write path in package api) produces, falling
// back to treating raw as plaintext for backward compatibility with config
// files written before encryption-at-rest was introduced. Kept as a small,
// private duplicate of api's own decryptEncryptedPayload (rather than an
// export from api, which mailmsg cannot import without an import cycle, or a
// relocation of api's helper, which is also used by an unrelated feature —
// the CardDAV client config — that has no reason to depend on mailmsg).
func decryptEncryptedPayload(raw []byte, keyPath string) ([]byte, error) {
	env, ok := cryptutil.ParseEnvelope(raw)
	if !ok {
		return raw, nil
	}
	key, err := cryptutil.LoadOrCreateKey(keyPath)
	if err != nil {
		return nil, err
	}
	return cryptutil.Open(env, key)
}

// ReadIMAPConfigPayload reads and decrypts the IMAP/SMTP config payload
// stored at path, decrypting it with the master key at keyPath. exists is
// false (with a nil error) when no config file has been saved yet.
func ReadIMAPConfigPayload(path, keyPath string) (IMAPConfigPayload, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return IMAPConfigPayload{}, false, nil
		}
		return IMAPConfigPayload{}, false, err
	}

	plain, err := decryptEncryptedPayload(b, keyPath)
	if err != nil {
		return IMAPConfigPayload{}, false, err
	}

	var payload IMAPConfigPayload
	if err := json.Unmarshal(plain, &payload); err != nil {
		return IMAPConfigPayload{}, false, err
	}
	return NormalizeIMAPPayload(payload), true, nil
}
