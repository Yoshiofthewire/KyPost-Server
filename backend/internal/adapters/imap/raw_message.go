package imap

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	goimap "github.com/BrianLeishman/go-imap"
)

// FetchRawMessage issues a raw UID FETCH for BODY.PEEK[] against uid in the
// currently selected mailbox — the complete, untouched RFC 5322 byte stream
// (headers + body, exactly as stored), not a MIME-parsed copy. This is
// deliberately distinct from every other body-reading path in this package
// (GetMessageBodies, ListOverviews, etc.), which all go through go-imap's
// own MIME parser: DKIM signature verification (see dkim_verify.go) must
// operate on the exact bytes a signature was originally computed over, and
// a re-parsed/re-serialized copy would not reliably reproduce them.
func (c *APIClient) FetchRawMessage(ctx context.Context, uid int) ([]byte, error) {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	d, err := c.ensureConnectedLocked()
	if err != nil {
		return nil, err
	}

	cmd := "UID FETCH " + strconv.Itoa(uid) + " BODY.PEEK[]"
	raw, err := d.Exec(cmd, true, goimap.RetryCount, nil)
	if err != nil {
		return nil, fmt.Errorf("imap fetch raw message: %w", err)
	}

	records, err := d.ParseFetchResponse(raw)
	if err != nil {
		return nil, fmt.Errorf("imap fetch raw message: %w", err)
	}

	out, err := parseRawMessageRecord(records, uid)
	if err != nil {
		return nil, fmt.Errorf("imap fetch raw message: %w", err)
	}
	return out, nil
}

// parseRawMessageRecord extracts the full BODY[] literal for uid from parsed
// FETCH records — the pure logic behind FetchRawMessage, split out so it can
// be tested directly against synthetic records without a live IMAP
// connection. Mirrors parseHeaderFieldsRecords's token-walking approach (see
// auth_results.go for the verified token-shape notes that apply equally
// here), but matches on the bare "BODY[" marker (no HEADER.FIELDS clause)
// and returns the single matching UID's raw bytes rather than a per-UID
// slice of header lines.
func parseRawMessageRecord(records [][]*goimap.Token, uid int) ([]byte, error) {
	for _, tks := range records {
		tks = unwrapTokens(tks)

		var (
			recordUID  int
			uidFound   bool
			value      string
			valueFound bool
		)

		for i := 0; i < len(tks); i++ {
			t := tks[i]

			if strings.EqualFold(t.Str, "UID") {
				if i+1 >= len(tks) {
					return nil, fmt.Errorf("parse raw message: UID token has no following value")
				}
				if tks[i+1].Type != goimap.TNumber {
					return nil, fmt.Errorf("parse raw message: expected TNumber after UID, got %s", goimap.GetTokenName(tks[i+1].Type))
				}
				recordUID = tks[i+1].Num
				uidFound = true
				i++
				continue
			}

			// BODY[] marker (no HEADER.FIELDS clause): the value is the first
			// TAtom/TQuoted/TNil token that follows the "]" terminator.
			if strings.HasPrefix(strings.ToUpper(t.Str), "BODY[") {
				var got bool
				for j := i + 1; j < len(tks); j++ {
					switch tks[j].Type {
					case goimap.TNil:
						value = ""
						got = true
					case goimap.TAtom, goimap.TQuoted:
						value = tks[j].Str
						got = true
					}
					if got {
						break
					}
				}
				if !got {
					return nil, fmt.Errorf("parse raw message: BODY[] token has no value")
				}
				valueFound = true
			}
		}

		if !uidFound {
			return nil, fmt.Errorf("parse raw message: record has no UID token")
		}
		if recordUID != uid {
			continue
		}
		if !valueFound {
			return nil, fmt.Errorf("parse raw message: no BODY[] value found for UID %d", uid)
		}
		return []byte(value), nil
	}

	return nil, fmt.Errorf("parse raw message: no record found for UID %d", uid)
}
