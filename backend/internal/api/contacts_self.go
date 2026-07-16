package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// contactSelfPayload is the request body for POST /api/contacts/{id}/self.
type contactSelfPayload struct {
	Self bool `json:"self"`
}

// handleContactSelf marks or unmarks a contact as the caller's own contact
// card (contacts.Contact.IsSelf) — the one handlePGPQRKey includes in the
// PGP QR key-exchange response. At most one contact can hold the flag;
// store.SetSelf clears any previous holder.
func (s *Server) handleContactSelf(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	store, err := s.contactsFor(r)
	if err != nil {
		http.Error(w, "failed to open contacts store", http.StatusInternalServerError)
		return
	}
	uid := strings.TrimSpace(r.PathValue("id"))
	if uid == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	var payload contactSelfPayload
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&payload); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	updated, found, err := store.SetSelf(uid, payload.Self)
	if err != nil {
		http.Error(w, "failed to update contact", http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "contact not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}
