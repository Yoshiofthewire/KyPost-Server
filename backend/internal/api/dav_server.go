package api

import (
	"context"
	"errors"
	"net/http"
	"path"
	"strings"
	"time"

	"llama-lab/backend/internal/contacts"

	"github.com/emersion/go-vcard"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/carddav"
)

// davPrefix is the fixed mount point for the CardDAV surface. Address book
// discovery paths (principal, home set, address book, address objects) are
// all built under it per the depth-based resource typing that
// emersion/go-webdav's carddav.Handler expects.
const davPrefix = "/dav"

// handleCardDAV mounts the CardDAV protocol handler for the caller's own
// contacts. It is reached only after withDAVBasicAuth has authenticated the
// request (session cookies are not accepted here — native CardDAV clients
// only speak HTTP Basic Auth). When the request path names a username (i.e.
// everything under davPrefix except the well-known discovery endpoint), it
// must match the authenticated identity, or the request is rejected — this
// keeps a valid app password from one account from being pointed at another
// account's URL and getting a confusing response.
func (s *Server) handleCardDAV(w http.ResponseWriter, r *http.Request) {
	ac, ok := authFromContext(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if rel, cut := strings.CutPrefix(r.URL.Path, davPrefix+"/"); cut {
		segment, _, _ := strings.Cut(rel, "/")
		if segment != "" && segment != ac.Username {
			http.Error(w, "forbidden: address book belongs to a different user", http.StatusForbidden)
			return
		}
	}
	handler := &carddav.Handler{Backend: &contactsDAVBackend{server: s}, Prefix: davPrefix}
	handler.ServeHTTP(w, r)
}

// contactsDAVBackend adapts contacts.Store to carddav.Backend. It resolves
// the acting user from the AuthContext already injected into the request
// context by withDAVBasicAuth (Backend methods only receive a context.Context,
// not the *http.Request).
type contactsDAVBackend struct {
	server *Server
}

func (b *contactsDAVBackend) userAndStore(ctx context.Context) (AuthContext, *contacts.Store, error) {
	ac, ok := authContextFromContext(ctx)
	if !ok {
		return AuthContext{}, nil, errors.New("missing auth context")
	}
	store, err := b.server.userContactsStore(ac.UserID)
	if err != nil {
		return AuthContext{}, nil, err
	}
	return ac, store, nil
}

func (b *contactsDAVBackend) CurrentUserPrincipal(ctx context.Context) (string, error) {
	ac, ok := authContextFromContext(ctx)
	if !ok {
		return "", errors.New("missing auth context")
	}
	return path.Join(davPrefix, ac.Username) + "/", nil
}

func (b *contactsDAVBackend) AddressBookHomeSetPath(ctx context.Context) (string, error) {
	ac, ok := authContextFromContext(ctx)
	if !ok {
		return "", errors.New("missing auth context")
	}
	return path.Join(davPrefix, ac.Username, "contacts") + "/", nil
}

// addressBookPath is the one, fixed address book every user has. There is no
// multi-address-book support in v1.
func (b *contactsDAVBackend) addressBookPath(ac AuthContext) string {
	return path.Join(davPrefix, ac.Username, "contacts", "default") + "/"
}

func (b *contactsDAVBackend) objectPath(ac AuthContext, uid string) string {
	return path.Join(b.addressBookPath(ac), uid+".vcf")
}

func uidFromObjectPath(p string) string {
	return strings.TrimSuffix(path.Base(p), ".vcf")
}

func (b *contactsDAVBackend) ListAddressBooks(ctx context.Context) ([]carddav.AddressBook, error) {
	ac, ok := authContextFromContext(ctx)
	if !ok {
		return nil, errors.New("missing auth context")
	}
	return []carddav.AddressBook{{
		Path:        b.addressBookPath(ac),
		Name:        "Contacts",
		Description: "Llama Mail contacts",
	}}, nil
}

func (b *contactsDAVBackend) GetAddressBook(ctx context.Context, p string) (*carddav.AddressBook, error) {
	books, err := b.ListAddressBooks(ctx)
	if err != nil {
		return nil, err
	}
	for _, ab := range books {
		if ab.Path == p {
			return &ab, nil
		}
	}
	return nil, webdav.NewHTTPError(http.StatusNotFound, errors.New("address book not found"))
}

func (b *contactsDAVBackend) CreateAddressBook(ctx context.Context, _ *carddav.AddressBook) error {
	return webdav.NewHTTPError(http.StatusForbidden, errors.New("creating address books is not supported"))
}

func (b *contactsDAVBackend) DeleteAddressBook(ctx context.Context, _ string) error {
	return webdav.NewHTTPError(http.StatusForbidden, errors.New("deleting the address book is not supported"))
}

func (b *contactsDAVBackend) GetAddressObject(ctx context.Context, p string, _ *carddav.AddressDataRequest) (*carddav.AddressObject, error) {
	_, store, err := b.userAndStore(ctx)
	if err != nil {
		return nil, err
	}
	c, ok := store.Get(uidFromObjectPath(p))
	if !ok || c.Deleted {
		return nil, webdav.NewHTTPError(http.StatusNotFound, errors.New("contact not found"))
	}
	return &carddav.AddressObject{
		Path:    p,
		ETag:    c.ETag(),
		ModTime: parseContactTime(c.UpdatedAt),
		Card:    contactToVCard(c),
	}, nil
}

func (b *contactsDAVBackend) ListAddressObjects(ctx context.Context, p string, _ *carddav.AddressDataRequest) ([]carddav.AddressObject, error) {
	ac, store, err := b.userAndStore(ctx)
	if err != nil {
		return nil, err
	}
	list := store.List()
	out := make([]carddav.AddressObject, 0, len(list))
	for _, c := range list {
		out = append(out, carddav.AddressObject{
			Path:    b.objectPath(ac, c.UID),
			ETag:    c.ETag(),
			ModTime: parseContactTime(c.UpdatedAt),
			Card:    contactToVCard(c),
		})
	}
	return out, nil
}

// QueryAddressObjects implements the addressbook-query REPORT. v1 does not
// evaluate CARDDAV:filter prop-filters — it returns the full address book
// (a safe superset of any real match set) rather than filtering server-side.
// Clients that rely on server-side filtering will simply receive more
// results than strictly necessary; this is a documented limitation, not a
// correctness bug (see backend/internal/contacts/AGENTS.md).
func (b *contactsDAVBackend) QueryAddressObjects(ctx context.Context, p string, query *carddav.AddressBookQuery) ([]carddav.AddressObject, error) {
	return b.ListAddressObjects(ctx, p, &query.DataRequest)
}

func (b *contactsDAVBackend) PutAddressObject(ctx context.Context, p string, card vcard.Card, opts *carddav.PutAddressObjectOptions) (*carddav.AddressObject, error) {
	ac, store, err := b.userAndStore(ctx)
	if err != nil {
		return nil, err
	}
	uid := uidFromObjectPath(p)
	existing, exists := store.Get(uid)

	if opts != nil {
		if opts.IfNoneMatch.IsSet() && opts.IfNoneMatch.IsWildcard() && exists && !existing.Deleted {
			return nil, webdav.NewHTTPError(http.StatusPreconditionFailed, errors.New("contact already exists"))
		}
		if opts.IfMatch.IsSet() {
			etag, err := opts.IfMatch.ETag()
			if err != nil || !exists || existing.ETag() != etag {
				return nil, webdav.NewHTTPError(http.StatusPreconditionFailed, errors.New("etag mismatch"))
			}
		}
	}

	updated, err := store.Upsert(contactFromVCard(uid, card))
	if err != nil {
		return nil, err
	}
	return &carddav.AddressObject{
		Path:    b.objectPath(ac, updated.UID),
		ETag:    updated.ETag(),
		ModTime: parseContactTime(updated.UpdatedAt),
		Card:    card,
	}, nil
}

func (b *contactsDAVBackend) DeleteAddressObject(ctx context.Context, p string) error {
	_, store, err := b.userAndStore(ctx)
	if err != nil {
		return err
	}
	removed, err := store.Delete(uidFromObjectPath(p))
	if err != nil {
		return err
	}
	if !removed {
		return webdav.NewHTTPError(http.StatusNotFound, errors.New("contact not found"))
	}
	return nil
}

func parseContactTime(v string) time.Time {
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}
	}
	return t
}

// contactToVCard renders a Contact as a vCard 4.0 card for CardDAV GET/REPORT
// responses.
func contactToVCard(c contacts.Contact) vcard.Card {
	card := make(vcard.Card)
	card.SetValue(vcard.FieldVersion, "4.0")
	card.SetValue(vcard.FieldUID, c.UID)

	fn := strings.TrimSpace(c.FormattedName)
	if fn == "" {
		fn = "Unnamed Contact"
	}
	card.SetValue(vcard.FieldFormattedName, fn)

	if c.GivenName != "" || c.FamilyName != "" || c.MiddleName != "" || c.Prefix != "" || c.Suffix != "" {
		card.SetName(&vcard.Name{
			FamilyName:      c.FamilyName,
			GivenName:       c.GivenName,
			AdditionalName:  c.MiddleName,
			HonorificPrefix: c.Prefix,
			HonorificSuffix: c.Suffix,
		})
	}
	if c.Nickname != "" {
		card.SetValue(vcard.FieldNickname, c.Nickname)
	}
	if c.Org != "" {
		card.SetValue(vcard.FieldOrganization, c.Org)
	}
	if c.Title != "" {
		card.SetValue(vcard.FieldTitle, c.Title)
	}
	if c.Notes != "" {
		card.SetValue(vcard.FieldNote, c.Notes)
	}
	if c.Birthday != "" {
		card.SetValue(vcard.FieldBirthday, c.Birthday)
	}
	for _, e := range c.Emails {
		f := &vcard.Field{Value: e.Value}
		if e.Label != "" {
			f.Params = vcard.Params{vcard.ParamType: []string{e.Label}}
		}
		card.Add(vcard.FieldEmail, f)
	}
	for _, ph := range c.Phones {
		f := &vcard.Field{Value: ph.Value}
		if ph.Label != "" {
			f.Params = vcard.Params{vcard.ParamType: []string{ph.Label}}
		}
		card.Add(vcard.FieldTelephone, f)
	}
	for _, a := range c.Addresses {
		addr := &vcard.Address{
			Field:         &vcard.Field{},
			StreetAddress: a.Street,
			Locality:      a.City,
			Region:        a.Region,
			PostalCode:    a.PostalCode,
			Country:       a.Country,
		}
		if a.Label != "" {
			addr.Field.Params = vcard.Params{vcard.ParamType: []string{a.Label}}
		}
		card.AddAddress(addr)
	}
	return card
}

// contactFromVCard maps an incoming vCard (from a CardDAV PUT) onto a
// Contact, assigning uid as the identity regardless of what the card's own
// UID property says — the DAV resource path is authoritative.
func contactFromVCard(uid string, card vcard.Card) contacts.Contact {
	c := contacts.Contact{UID: uid}
	c.FormattedName = strings.TrimSpace(card.Value(vcard.FieldFormattedName))
	if n := card.Name(); n != nil {
		c.GivenName = n.GivenName
		c.FamilyName = n.FamilyName
		c.MiddleName = n.AdditionalName
		c.Prefix = n.HonorificPrefix
		c.Suffix = n.HonorificSuffix
	}
	c.Nickname = card.Value(vcard.FieldNickname)
	c.Org = card.Value(vcard.FieldOrganization)
	c.Title = card.Value(vcard.FieldTitle)
	c.Notes = card.Value(vcard.FieldNote)
	c.Birthday = card.Value(vcard.FieldBirthday)

	for _, f := range card[vcard.FieldEmail] {
		c.Emails = append(c.Emails, contacts.ContactValue{Label: f.Params.Get(vcard.ParamType), Value: f.Value})
	}
	for _, f := range card[vcard.FieldTelephone] {
		c.Phones = append(c.Phones, contacts.ContactValue{Label: f.Params.Get(vcard.ParamType), Value: f.Value})
	}
	for _, a := range card.Addresses() {
		label := ""
		if a.Field != nil {
			label = a.Field.Params.Get(vcard.ParamType)
		}
		c.Addresses = append(c.Addresses, contacts.ContactAddress{
			Label:      label,
			Street:     a.StreetAddress,
			City:       a.Locality,
			Region:     a.Region,
			PostalCode: a.PostalCode,
			Country:    a.Country,
		})
	}

	if c.FormattedName == "" {
		c.FormattedName = strings.TrimSpace(c.GivenName + " " + c.FamilyName)
	}
	return c
}
