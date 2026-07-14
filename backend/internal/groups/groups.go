// Package groups implements per-user contact groups: named entities that
// contacts reference by ID (contacts.Contact.GroupIDs), stored as a sibling
// file next to contacts.json in each user's state directory.
package groups

// Group is one named contact group. Renaming is an Upsert on the existing
// ID, so contacts referencing it by ID never need rewriting.
type Group struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Rev       int64  `json:"rev"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}
