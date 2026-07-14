package api

import (
	imapadapter "llama-lab/backend/internal/adapters/imap"
	"llama-lab/backend/internal/contacts"
	"llama-lab/backend/internal/pgpmail"
)

// decryptPGPMessageContent decrypts c's PGPEncryptedPayload with userID's
// stored private key and, if the sender is a known contact with a PGP key,
// verifies an embedded signature. On any failure (no identity configured,
// wrong key, corrupt payload) it returns c with a PGPDecryptError set rather
// than erroring the whole inbox fetch — one bad message must not break the
// list.
func (s *Server) decryptPGPMessageContent(userID string, c imapadapter.MessageContent) imapadapter.MessageContent {
	c.PGPEncrypted = true
	u, err := s.users.Get(userID)
	if err != nil || u.PGPPrivateKeyEnc == "" {
		c.PGPDecryptError = "no pgp identity configured for this account"
		c.PGPEncryptedPayload = ""
		return c
	}
	identity, err := pgpmail.OpenPrivateKey(u.PGPPrivateKeyEnc, s.pgpPrivateKeyPath)
	if err != nil {
		c.PGPDecryptError = "failed to load pgp identity"
		c.PGPEncryptedPayload = ""
		return c
	}

	var signerKeys []string
	if contactsStore, cerr := s.userContactsStore(userID); cerr == nil {
		signerKeys = allKnownPGPKeys(contactsStore)
	}

	result, err := pgpmail.DecryptMIME(c.PGPEncryptedPayload, identity, signerKeys)
	if err != nil {
		c.PGPDecryptError = "failed to decrypt message"
		c.PGPEncryptedPayload = ""
		return c
	}
	body, attachments, err := pgpmail.ParseContent(result.Content)
	if err != nil {
		c.PGPDecryptError = "failed to parse decrypted message"
		c.PGPEncryptedPayload = ""
		return c
	}

	c.Body = body
	c.HasAttachments = len(attachments) > 0
	c.PGPSigned = result.Signed
	c.PGPVerified = result.Verified
	c.PGPSignerFingerprint = result.SignerFingerprint
	c.PGPEncryptedPayload = ""
	return c
}

// decryptPGPUnreadMessage mirrors decryptPGPMessageContent for the
// imapadapter.UnreadMessage shape used by ListUnreadMessages's classic
// (non-delta) inbox path.
func (s *Server) decryptPGPUnreadMessage(userID string, msg imapadapter.UnreadMessage) imapadapter.UnreadMessage {
	msg.PGPEncrypted = true
	u, err := s.users.Get(userID)
	if err != nil || u.PGPPrivateKeyEnc == "" {
		msg.PGPDecryptError = "no pgp identity configured for this account"
		msg.PGPEncryptedPayload = ""
		return msg
	}
	identity, err := pgpmail.OpenPrivateKey(u.PGPPrivateKeyEnc, s.pgpPrivateKeyPath)
	if err != nil {
		msg.PGPDecryptError = "failed to load pgp identity"
		msg.PGPEncryptedPayload = ""
		return msg
	}

	var signerKeys []string
	if contactsStore, cerr := s.userContactsStore(userID); cerr == nil {
		signerKeys = allKnownPGPKeys(contactsStore)
	}

	result, err := pgpmail.DecryptMIME(msg.PGPEncryptedPayload, identity, signerKeys)
	if err != nil {
		msg.PGPDecryptError = "failed to decrypt message"
		msg.PGPEncryptedPayload = ""
		return msg
	}
	body, attachments, err := pgpmail.ParseContent(result.Content)
	if err != nil {
		msg.PGPDecryptError = "failed to parse decrypted message"
		msg.PGPEncryptedPayload = ""
		return msg
	}

	msg.Body = body
	msg.HasAttachments = len(attachments) > 0
	msg.PGPSigned = result.Signed
	msg.PGPVerified = result.Verified
	msg.PGPSignerFingerprint = result.SignerFingerprint
	msg.PGPEncryptedPayload = ""
	return msg
}

// allKnownPGPKeys returns every contact's armored public key, offered as the
// candidate signer set when verifying an inbound signed-and-encrypted
// message: the sender isn't known in advance, so all are tried — DecryptMIME
// only reports success against whichever key actually produced the
// signature.
func allKnownPGPKeys(store *contacts.Store) []string {
	var keys []string
	for _, c := range store.List() {
		if c.PGPKey != "" {
			keys = append(keys, c.PGPKey)
		}
	}
	return keys
}
