package processor

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"llama-lab/backend/internal/state"
)

var errNativeDeviceStale = errors.New("native device token is stale")

const (
	fcmOAuthScope = "https://www.googleapis.com/auth/firebase.messaging"
)

type nativePushMessage struct {
	Title string
	Body  string
	Data  map[string]string
}

type nativeSender interface {
	Name() string
	Supports(platform string) bool
	Send(ctx context.Context, device state.NativeDevice, message nativePushMessage) error
}

type fcmSender struct {
	projectID   string
	tokenURL    string
	sendURL     string
	clientEmail string
	privateKey  *rsa.PrivateKey
	client      *http.Client
	now         func() time.Time

	mu               sync.Mutex
	accessToken      string
	accessTokenUntil time.Time
}

type fcmServiceAccount struct {
	ProjectID   string `json:"project_id"`
	PrivateKey  string `json:"private_key"`
	ClientEmail string `json:"client_email"`
	TokenURI    string `json:"token_uri"`
}

func newNativeSendersFromEnv() []nativeSender {
	out := make([]nativeSender, 0, 1)
	if sender := newFCMSenderFromEnv(); sender != nil {
		out = append(out, sender)
	}
	return out
}

func newFCMSenderFromEnv() *fcmSender {
	serviceAccountPath := strings.TrimSpace(os.Getenv("FCM_SERVICE_ACCOUNT_FILE"))
	if serviceAccountPath == "" {
		return nil
	}

	serviceAccountData, err := os.ReadFile(serviceAccountPath)
	if err != nil {
		return nil
	}

	var sa fcmServiceAccount
	if err := json.Unmarshal(serviceAccountData, &sa); err != nil {
		return nil
	}
	clientEmail := strings.TrimSpace(sa.ClientEmail)
	if clientEmail == "" {
		return nil
	}
	privateKey, err := parseFCMPrivateKey(sa.PrivateKey)
	if err != nil {
		return nil
	}

	projectID := strings.TrimSpace(os.Getenv("FCM_PROJECT_ID"))
	if projectID == "" {
		projectID = strings.TrimSpace(sa.ProjectID)
	}
	if projectID == "" {
		return nil
	}

	tokenURL := strings.TrimSpace(os.Getenv("FCM_TOKEN_URL"))
	if tokenURL == "" {
		tokenURL = strings.TrimSpace(sa.TokenURI)
	}
	if tokenURL == "" {
		tokenURL = "https://oauth2.googleapis.com/token"
	}

	sendURL := strings.TrimSpace(os.Getenv("FCM_SEND_URL"))
	if sendURL == "" {
		sendURL = fmt.Sprintf("https://fcm.googleapis.com/v1/projects/%s/messages:send", projectID)
	}

	return &fcmSender{
		projectID:   projectID,
		tokenURL:    tokenURL,
		sendURL:     sendURL,
		clientEmail: clientEmail,
		privateKey:  privateKey,
		client:      &http.Client{Timeout: 15 * time.Second},
		now:         time.Now,
	}
}

func (s *fcmSender) Name() string {
	return "fcm"
}

func (s *fcmSender) Supports(platform string) bool {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "", "android", "ios":
		return true
	default:
		return false
	}
}

func (s *fcmSender) Send(ctx context.Context, device state.NativeDevice, message nativePushMessage) error {
	registrationToken := strings.TrimSpace(device.PushToken)
	if registrationToken == "" {
		return errors.New("missing push token")
	}
	accessToken, err := s.oauthToken(ctx)
	if err != nil {
		return err
	}

	payload := map[string]any{
		"message": map[string]any{
			"token": registrationToken,
			"notification": map[string]any{
				"title": message.Title,
				"body":  message.Body,
			},
			"data": message.Data,
		},
		"android": map[string]any{
			"priority": "HIGH",
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.sendURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	trimmed := strings.TrimSpace(string(respBody))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if isFCMStaleResponse(resp.StatusCode, trimmed) {
			return fmt.Errorf("%w: status=%d response=%s", errNativeDeviceStale, resp.StatusCode, trimmed)
		}
		return fmt.Errorf("fcm send failed: status=%d response=%s", resp.StatusCode, trimmed)
	}
	return nil
}

func (s *fcmSender) oauthToken(ctx context.Context) (string, error) {
	if s.now == nil {
		s.now = time.Now
	}
	now := s.now().UTC()

	s.mu.Lock()
	if s.accessToken != "" && now.Before(s.accessTokenUntil.Add(-1*time.Minute)) {
		token := s.accessToken
		s.mu.Unlock()
		return token, nil
	}
	s.mu.Unlock()

	token, expiresAt, err := s.requestOAuthToken(ctx, now)
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	s.accessToken = token
	s.accessTokenUntil = expiresAt
	s.mu.Unlock()
	return token, nil
}

func (s *fcmSender) requestOAuthToken(ctx context.Context, now time.Time) (string, time.Time, error) {
	assertion, err := signServiceAccountAssertion(s.clientEmail, s.privateKey, s.tokenURL, now)
	if err != nil {
		return "", time.Time{}, err
	}

	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	form.Set("assertion", assertion)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", time.Time{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	trimmed := strings.TrimSpace(string(body))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", time.Time{}, fmt.Errorf("fcm oauth failed: status=%d response=%s", resp.StatusCode, trimmed)
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", time.Time{}, fmt.Errorf("fcm oauth decode failed: %w", err)
	}
	token := strings.TrimSpace(tokenResp.AccessToken)
	if token == "" {
		return "", time.Time{}, errors.New("fcm oauth token missing")
	}
	expiresIn := tokenResp.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	return token, now.Add(time.Duration(expiresIn) * time.Second), nil
}

func signServiceAccountAssertion(clientEmail string, privateKey *rsa.PrivateKey, tokenURL string, now time.Time) (string, error) {
	headerJSON, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	claimsJSON, err := json.Marshal(map[string]any{
		"iss":   clientEmail,
		"scope": fcmOAuthScope,
		"aud":   tokenURL,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	})
	if err != nil {
		return "", err
	}

	encodedHeader := base64.RawURLEncoding.EncodeToString(headerJSON)
	encodedClaims := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := encodedHeader + "." + encodedClaims

	hash := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, hash[:])
	if err != nil {
		return "", err
	}

	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func selectNativeSender(senders []nativeSender, platform string) nativeSender {
	for _, sender := range senders {
		if sender.Supports(platform) {
			return sender
		}
	}
	return nil
}

func parseFCMPrivateKey(privateKeyPEM string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(privateKeyPEM)))
	if block == nil {
		return nil, errors.New("fcm private key pem block missing")
	}

	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("fcm private key is not RSA")
		}
		return rsaKey, nil
	}

	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}

	return nil, errors.New("unsupported fcm private key format")
}

func isFCMStaleResponse(statusCode int, response string) bool {
	lower := strings.ToLower(response)
	if strings.Contains(lower, "unregistered") || strings.Contains(lower, "notregistered") || strings.Contains(lower, "registration-token-not-registered") {
		return true
	}
	if statusCode == http.StatusNotFound && strings.Contains(lower, "requested entity was not found") {
		return true
	}
	return false
}
