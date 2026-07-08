// Package contacts implements the per-user address book: a local, CardDAV-
// syncable contact list stored as a sibling file next to state.json and
// decisions.json in each user's state directory.
package contacts

import "fmt"

func etagForRev(rev int64) string {
	return fmt.Sprintf("rev-%d", rev)
}

// Contact is one address book entry. UID is the stable CardDAV/vCard
// identity (also the DAV resource name); Rev is a per-user monotonic
// revision bumped on every mutation, and is the single mechanism behind
// both the CardDAV ETag and the mobile sync cursor.
type Contact struct {
	UID       string `json:"uid"`
	Rev       int64  `json:"rev"`
	Deleted   bool   `json:"deleted,omitempty"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`

	FormattedName string           `json:"fn"`
	GivenName     string           `json:"givenName,omitempty"`
	FamilyName    string           `json:"familyName,omitempty"`
	MiddleName    string           `json:"middleName,omitempty"`
	Prefix        string           `json:"prefix,omitempty"`
	Suffix        string           `json:"suffix,omitempty"`
	Nickname      string           `json:"nickname,omitempty"`
	Org           string           `json:"org,omitempty"`
	Title         string           `json:"title,omitempty"`
	Emails        []ContactValue   `json:"emails,omitempty"`
	Phones        []ContactValue   `json:"phones,omitempty"`
	Addresses     []ContactAddress `json:"addresses,omitempty"`
	Notes         string           `json:"notes,omitempty"`
	Birthday      string           `json:"birthday,omitempty"` // YYYY-MM-DD
}

// ETag is the CardDAV entity tag / mobile-sync change marker for this
// revision of the contact. It is derived from Rev rather than stored, so
// there is exactly one source of truth for "has this changed".
func (c Contact) ETag() string {
	return etagForRev(c.Rev)
}

type ContactValue struct {
	Label string `json:"label,omitempty"` // "home", "work", "mobile", etc.
	Value string `json:"value"`
}

type ContactAddress struct {
	Label      string `json:"label,omitempty"`
	Street     string `json:"street,omitempty"`
	City       string `json:"city,omitempty"`
	Region     string `json:"region,omitempty"`
	PostalCode string `json:"postalCode,omitempty"`
	Country    string `json:"country,omitempty"`
}

// tombstone clears every PII-bearing field, keeping only the identity and
// bookkeeping fields, so a deleted contact doesn't leak its data forever in
// the sync history.
func (c *Contact) tombstone() {
	c.Deleted = true
	c.FormattedName = ""
	c.GivenName = ""
	c.FamilyName = ""
	c.MiddleName = ""
	c.Prefix = ""
	c.Suffix = ""
	c.Nickname = ""
	c.Org = ""
	c.Title = ""
	c.Emails = nil
	c.Phones = nil
	c.Addresses = nil
	c.Notes = ""
	c.Birthday = ""
}
