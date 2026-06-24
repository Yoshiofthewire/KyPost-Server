package imap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	goimap "github.com/BrianLeishman/go-imap"
)

type Message struct {
	ID      string
	Subject string
	Sender  string
	Body    string
}

type Client interface {
	ListUnreadInbox(ctx context.Context, sinceCheckpoint string) ([]Message, string, error)
	ListLabels(ctx context.Context) ([]string, error)
	EnsureLabel(ctx context.Context, label string) error
	ApplyLabel(ctx context.Context, messageID, label string) error
}

type StubClient struct{}

func (s *StubClient) ListUnreadInbox(_ context.Context, _ string) ([]Message, string, error) {
	return []Message{}, "", nil
}

func (s *StubClient) ListLabels(_ context.Context) ([]string, error) {
	return []string{}, nil
}

func (s *StubClient) EnsureLabel(_ context.Context, _ string) error {
	return nil
}

func (s *StubClient) ApplyLabel(_ context.Context, _ string, _ string) error {
	return nil
}

type APIClient struct {
	mu       sync.Mutex
	dialer   *goimap.Dialer
	host     string
	port     int
	username string
	password string
	mailbox  string
}

func NewAPIClientFromEnv() *APIClient {
	host := strings.TrimSpace(os.Getenv("IMAP_HOST"))
	username := strings.TrimSpace(os.Getenv("IMAP_USERNAME"))
	password := strings.TrimSpace(os.Getenv("IMAP_PASSWORD"))
	mailbox := strings.TrimSpace(os.Getenv("IMAP_MAILBOX"))
	if mailbox == "" {
		mailbox = "INBOX"
	}

	port := 993
	if raw := strings.TrimSpace(os.Getenv("IMAP_PORT")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			port = parsed
		}
	}

	return &APIClient{
		host:     host,
		port:     port,
		username: username,
		password: password,
		mailbox:  mailbox,
	}
}

func (c *APIClient) ListUnreadInbox(ctx context.Context, sinceCheckpoint string) ([]Message, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}

	d, err := c.ensureConnectedLocked()
	if err != nil {
		return nil, "", err
	}

	uids, err := d.GetUIDs("UNSEEN")
	if err != nil {
		return nil, "", fmt.Errorf("imap search unseen: %w", err)
	}
	if len(uids) == 0 {
		return []Message{}, sinceCheckpoint, nil
	}

	minUID := parseCheckpointUID(sinceCheckpoint)
	filtered := make([]int, 0, len(uids))
	for _, uid := range uids {
		if uid > minUID {
			filtered = append(filtered, uid)
		}
	}
	if len(filtered) == 0 {
		return []Message{}, sinceCheckpoint, nil
	}
	sort.Ints(filtered)

	emails, err := d.GetEmails(filtered...)
	if err != nil {
		return nil, "", fmt.Errorf("imap fetch emails: %w", err)
	}

	out := make([]Message, 0, len(filtered))
	maxUID := minUID
	for _, uid := range filtered {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		e := emails[uid]
		if e == nil {
			continue
		}
		body := strings.TrimSpace(e.Text)
		if body == "" {
			body = strings.TrimSpace(e.HTML)
		}
		out = append(out, Message{
			ID:      strconv.Itoa(uid),
			Subject: strings.TrimSpace(e.Subject),
			Sender:  strings.TrimSpace(e.From.String()),
			Body:    body,
		})
		if uid > maxUID {
			maxUID = uid
		}
	}

	next := sinceCheckpoint
	if maxUID > minUID {
		next = strconv.Itoa(maxUID)
	}
	return out, next, nil
}

func (c *APIClient) ListLabels(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	d, err := c.ensureConnectedLocked()
	if err != nil {
		return nil, err
	}

	lastUIDs, err := d.GetLastNUIDs(200)
	if err != nil {
		return nil, fmt.Errorf("imap get recent uids: %w", err)
	}
	if len(lastUIDs) == 0 {
		return []string{}, nil
	}

	ov, err := d.GetOverviews(lastUIDs...)
	if err != nil {
		return nil, fmt.Errorf("imap get overviews: %w", err)
	}

	seen := map[string]bool{}
	labels := make([]string, 0, 16)
	for _, uid := range lastUIDs {
		o := ov[uid]
		if o == nil {
			continue
		}
		for _, flag := range o.Flags {
			flag = strings.TrimSpace(flag)
			if flag == "" || strings.HasPrefix(flag, "\\") {
				continue
			}
			if seen[flag] {
				continue
			}
			seen[flag] = true
			labels = append(labels, flag)
		}
	}
	sort.Strings(labels)
	return labels, nil
}

func (c *APIClient) EnsureLabel(ctx context.Context, label string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(label) == "" {
		return errors.New("label is required")
	}
	// IMAP keywords are typically created implicitly when first applied.
	return nil
}

func (c *APIClient) ApplyLabel(ctx context.Context, messageID, label string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	uid, err := strconv.Atoi(strings.TrimSpace(messageID))
	if err != nil || uid <= 0 {
		return fmt.Errorf("invalid message id %q", messageID)
	}
	label = strings.TrimSpace(label)
	if label == "" {
		return errors.New("label is required")
	}

	d, err := c.ensureConnectedLocked()
	if err != nil {
		return err
	}

	flags := goimap.Flags{Keywords: map[string]bool{label: true}}
	if err := d.SetFlags(uid, flags); err != nil {
		return fmt.Errorf("imap set keyword %q on uid %d: %w", label, uid, err)
	}
	return nil
}

func (c *APIClient) ensureConnectedLocked() (*goimap.Dialer, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if strings.TrimSpace(c.host) == "" || strings.TrimSpace(c.username) == "" || strings.TrimSpace(c.password) == "" {
		return nil, errors.New("missing IMAP credentials; configure IMAP_HOST, IMAP_USERNAME, and IMAP_PASSWORD")
	}

	if c.dialer == nil {
		goimap.DialTimeout = 10 * time.Second
		goimap.CommandTimeout = 45 * time.Second
		goimap.RetryCount = 3

		d, err := goimap.New(c.username, c.password, c.host, c.port)
		if err != nil {
			return nil, fmt.Errorf("imap connect: %w", err)
		}
		c.dialer = d
	}

	if err := c.dialer.SelectFolder(c.mailbox); err != nil {
		if recErr := c.dialer.Reconnect(); recErr != nil {
			return nil, fmt.Errorf("imap select folder %q: %w", c.mailbox, err)
		}
		if err := c.dialer.SelectFolder(c.mailbox); err != nil {
			return nil, fmt.Errorf("imap select folder %q after reconnect: %w", c.mailbox, err)
		}
	}

	return c.dialer, nil
}

func parseCheckpointUID(checkpoint string) int {
	v := strings.TrimSpace(checkpoint)
	if v == "" {
		return 0
	}
	uid, err := strconv.Atoi(v)
	if err != nil || uid < 0 {
		return 0
	}
	return uid
}