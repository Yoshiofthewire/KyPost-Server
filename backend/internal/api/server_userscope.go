package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	imapadapter "llama-lab/backend/internal/adapters/imap"
	"llama-lab/backend/internal/state"
)

// errIMAPNotConfigured is returned when a caller has not stored IMAP
// credentials yet; handlers translate it into a 400 with a clear message.
var errIMAPNotConfigured = errors.New("imap configuration is required")

func (s *Server) userConfigDir(userID string) string {
	return filepath.Join(s.configDir, "users", userID)
}

func (s *Server) userStateDir(userID string) string {
	return filepath.Join(s.stateDir, "users", userID)
}

func (s *Server) userIMAPConfigPath(userID string) string {
	return filepath.Join(s.userConfigDir(userID), "imap-config.json")
}

func (s *Server) userTuningPath(userID string) string {
	return filepath.Join(s.userConfigDir(userID), "tuning.md")
}

func (s *Server) userSettingsPath(userID string) string {
	return filepath.Join(s.userConfigDir(userID), "config.yaml")
}

func (s *Server) userStore(userID string) (*state.Store, error) {
	s.userMu.Lock()
	defer s.userMu.Unlock()
	if st, ok := s.userStores[userID]; ok {
		return st, nil
	}
	st, err := state.New(s.userStateDir(userID))
	if err != nil {
		return nil, err
	}
	s.userStores[userID] = st
	return st, nil
}

// storeFor resolves the calling user's state store from the request's
// AuthContext (requires the handler to be wrapped in withAuth).
func (s *Server) storeFor(r *http.Request) (*state.Store, error) {
	ac, ok := authFromContext(r)
	if !ok {
		return nil, errors.New("no auth context on request")
	}
	return s.userStore(ac.UserID)
}

type serverMailEntry struct {
	client    imapadapter.Client
	updatedAt string
}

// userMailClient returns a cached IMAP client for the user, rebuilt whenever
// their stored credential payload changes (keyed by the payload UpdatedAt).
// Returns errIMAPNotConfigured when the user has no stored credentials.
func (s *Server) userMailClient(userID string) (imapadapter.Client, error) {
	payload, exists, err := readIMAPConfigPayload(s.userIMAPConfigPath(userID), s.imapConfigKeyPath)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errIMAPNotConfigured
	}
	s.userMu.Lock()
	defer s.userMu.Unlock()
	if entry, ok := s.userMail[userID]; ok && entry.updatedAt == payload.UpdatedAt {
		return entry.client, nil
	}
	client := imapadapter.NewAPIClientFromStoredConfig(s.userIMAPConfigPath(userID), s.imapConfigKeyPath)
	s.userMail[userID] = &serverMailEntry{client: client, updatedAt: payload.UpdatedAt}
	return client, nil
}

func (s *Server) mailFor(r *http.Request) (imapadapter.Client, error) {
	ac, ok := authFromContext(r)
	if !ok {
		return nil, errors.New("no auth context on request")
	}
	return s.userMailClient(ac.UserID)
}

func (s *Server) invalidateUserMail(userID string) {
	s.userMu.Lock()
	delete(s.userMail, userID)
	s.userMu.Unlock()
}

// lookupUserBySubscriber maps a per-user subscriber ID back to its owning
// user, for the unauthenticated native-register endpoint. The in-memory
// index is lazily rebuilt on a miss so a subscriber ID minted after server
// start is still found without a restart.
func (s *Server) lookupUserBySubscriber(subscriberID string) (string, bool) {
	subscriberID = strings.TrimSpace(subscriberID)
	if subscriberID == "" {
		return "", false
	}
	s.userMu.Lock()
	if userID, ok := s.subIndex[subscriberID]; ok {
		s.userMu.Unlock()
		return userID, true
	}
	s.userMu.Unlock()

	s.rescanSubscriberIndex()

	s.userMu.Lock()
	defer s.userMu.Unlock()
	userID, ok := s.subIndex[subscriberID]
	return userID, ok
}

// rescanSubscriberIndex rebuilds subscriberID -> userID by reading every
// per-user state.json. Cheap at this scale (a handful of small files).
func (s *Server) rescanSubscriberIndex() {
	usersDir := filepath.Join(s.stateDir, "users")
	entries, err := os.ReadDir(usersDir)
	if err != nil {
		return
	}
	next := map[string]string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(usersDir, e.Name(), "state.json"))
		if err != nil {
			continue
		}
		var doc struct {
			SubscriberID string `json:"subscriberId"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			continue
		}
		if id := strings.TrimSpace(doc.SubscriberID); id != "" {
			next[id] = e.Name()
		}
	}
	s.userMu.Lock()
	s.subIndex = next
	s.userMu.Unlock()
}
