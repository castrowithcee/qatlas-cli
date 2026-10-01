package infomaniakmail

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

var base = time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)

func decodeMessages(t *testing.T, raw string) MessagesPage {
	t.Helper()
	var page MessagesPage
	if err := json.Unmarshal([]byte(raw), &page); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	return page
}

func uids(page MessagesPage) []uint32 {
	out := []uint32{}
	for _, message := range page.Messages {
		out = append(out, message.UID)
	}
	return out
}

func list(t *testing.T, e *environment, connection, arguments string) MessagesPage {
	t.Helper()
	result, err := e.invoke("infomaniakmail.messages.list", connection, arguments)
	if err != nil {
		t.Fatalf("messages.list %s: %v", arguments, err)
	}
	return decodeMessages(t, result)
}

func TestMessagesListReturnsOnlyEnvelopeData(t *testing.T) {
	e := newEnvironment(t)
	e.f.folder(t, "INBOX")
	uid := e.f.add(t, "INBOX", "Alice <alice@example.net>", "Hello there", base, imap.FlagSeen)

	result, err := e.invoke("infomaniakmail.messages.list", "open", `{"folder":"INBOX"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result, "BODY-CANARY") {
		t.Fatalf("result carries message content: %s", result)
	}
	page := decodeMessages(t, result)
	if page.Folder != "INBOX" || page.UIDValidity == 0 || page.Count != 1 || page.Matched != 1 || page.HasMore {
		t.Fatalf("page = %+v", page)
	}
	m := page.Messages[0]
	if m.UID != uid || m.Subject != "Hello there" || m.Size <= 0 || m.Date != base.Format(time.RFC3339) ||
		len(m.From) != 1 || m.From[0].Address != "alice@example.net" || m.From[0].Name != "Alice" ||
		len(m.To) != 1 || m.To[0].Address != mailbox || len(m.Flags) != 1 || !strings.EqualFold(m.Flags[0], `\Seen`) {
		t.Errorf("message = %+v", m)
	}
}

// The folder is opened with EXAMINE and a listing leaves every message's \Seen flag as it was.
func TestMessagesListExaminesAndKeepsSeenUnchanged(t *testing.T) {
	e := newEnvironment(t)
	e.f.folder(t, "INBOX")
	first := e.f.add(t, "INBOX", "alice@example.net", "one", base)
	second := e.f.add(t, "INBOX", "alice@example.net", "two", base.Add(time.Hour), imap.FlagSeen)

	before := list(t, e, "open", `{"folder":"INBOX"}`)
	after := list(t, e, "open", `{"folder":"INBOX"}`)
	unread := list(t, e, "open", `{"folder":"INBOX","unread":true}`)

	wire := e.f.wire.String()
	if !strings.Contains(wire, " EXAMINE ") || regexp.MustCompile(`T\d+ SELECT`).MatchString(wire) {
		t.Errorf("wire must EXAMINE and never SELECT:\n%s", wire)
	}
	if mutating := regexp.MustCompile(`T\d+ (UID )?(STORE|APPEND|EXPUNGE|COPY|MOVE|CREATE|DELETE|RENAME|CLOSE)\b|FETCH [^\r\n]*(BODY|RFC822\.TEXT|RFC822 )`).FindString(wire); mutating != "" {
		t.Errorf("wire contains %q:\n%s", mutating, wire)
	}
	if fmt.Sprint(before.Messages) != fmt.Sprint(after.Messages) {
		t.Errorf("a second listing differs: %+v vs %+v", before.Messages, after.Messages)
	}
	if got := uids(unread); len(got) != 1 || got[0] != first {
		t.Errorf("unread = %v, want only %d unread after two listings (second %d is seen)", got, first, second)
	}
	for _, m := range after.Messages {
		seen := len(m.Flags) == 1 && strings.EqualFold(m.Flags[0], `\Seen`)
		if (m.UID == second) != seen {
			t.Errorf("message %d flags = %v changed", m.UID, m.Flags)
		}
	}
}

func TestMessagesListFixedCriteria(t *testing.T) {
	e := newEnvironment(t)
	e.f.folder(t, "INBOX")
	jan := e.f.add(t, "INBOX", "alice@example.net", "jan", time.Date(2026, 1, 10, 9, 0, 0, 0, time.UTC))
	feb := e.f.add(t, "INBOX", "bob@example.net", "feb", time.Date(2026, 2, 10, 9, 0, 0, 0, time.UTC))
	mar := e.f.add(t, "INBOX", "alice@example.net", "mar", time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC))

	for arguments, want := range map[string][]uint32{
		`{"folder":"INBOX"}`:                                            {mar, feb, jan},
		`{"folder":"INBOX","since":"2026-02-01"}`:                       {mar, feb},
		`{"folder":"INBOX","before":"2026-03-01"}`:                      {feb, jan},
		`{"folder":"INBOX","since":"2026-02-01","before":"2026-03-01"}`: {feb},
		`{"folder":"INBOX","sender":"alice@example.net"}`:               {mar, jan},
		`{"folder":"INBOX","sender":"ALICE@example.net"}`:               {mar, jan},
	} {
		if got := uids(list(t, e, "open", arguments)); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s = %v, want %v", arguments, got, want)
		}
	}
	// Nothing but typed values reaches the search: a free search string or an unknown argument is refused.
	for _, arguments := range []string{
		`{"folder":"INBOX","search":"ALL"}`, `{"folder":"INBOX","query":"TEXT secret"}`,
		`{"folder":"INBOX","since":"yesterday"}`, `{"folder":"INBOX","since":"2026-13-45"}`,
		`{"folder":"INBOX","since":"2026-03-01","before":"2026-02-01"}`,
		`{"folder":"INBOX","sender":"alice@example.net\" OR ALL"}`, `{"folder":"INBOX","sender":"everyone"}`,
	} {
		if _, err := e.invoke("infomaniakmail.messages.list", "open", arguments); err == nil {
			t.Errorf("%s was accepted", arguments)
		}
	}
}

// A folder outside the allow-list is refused before any secret is read and before any connection, and the
// refusal does not name the allowed folder.
func TestForeignFolderIsRefusedWithoutIO(t *testing.T) {
	e := newEnvironment(t)
	e.f.folder(t, "Allowed")
	e.f.folder(t, "Secret")
	e.f.add(t, "Secret", "alice@example.net", "classified", base)

	for _, folder := range []string{"Secret", "allowed", "Allowed/Child", "INBOX"} {
		_, err := e.invoke("infomaniakmail.messages.list", "allowed", fmt.Sprintf(`{"folder":%q}`, folder))
		if !isInvalidRequest(err) {
			t.Errorf("folder %q: error = %v, want an invalid request", folder, err)
			continue
		}
		if strings.Contains(err.Error(), "Allowed") || strings.Contains(err.Error(), folder) {
			t.Errorf("folder %q: refusal %q names a folder", folder, err)
		}
	}
	if e.f.dials.Load() != 0 || e.f.reads.Load() != 0 {
		t.Errorf("a refused folder caused %d dials and %d secret reads, want none", e.f.dials.Load(), e.f.reads.Load())
	}
	// Wildcards are never a folder, even on a connection without an allow-list.
	for _, folder := range []string{"*", "%", "Sec*", "Sec%", "a\\u0000b", "a\\nb"} {
		_, err := e.invoke("infomaniakmail.messages.list", "open", fmt.Sprintf(`{"folder":"%s"}`, folder))
		if !isInvalidRequest(err) {
			t.Errorf("folder %q: error = %v, want an invalid request", folder, err)
		}
	}
	if e.f.dials.Load() != 0 || e.f.reads.Load() != 0 {
		t.Errorf("a wildcard folder caused %d dials and %d secret reads, want none", e.f.dials.Load(), e.f.reads.Load())
	}

	page := list(t, e, "allowed", `{"folder":"Allowed"}`)
	if page.Folder != "Allowed" || e.f.dials.Load() != 1 {
		t.Errorf("allowed folder: page = %+v, dials = %d", page, e.f.dials.Load())
	}
}

func TestMissingFolderIsNotFoundWithoutProviderText(t *testing.T) {
	e := newEnvironment(t)
	e.f.folder(t, "INBOX")
	_, err := e.invoke("infomaniakmail.messages.list", "open", `{"folder":"Nope"}`)
	if classOf(err) != provider.ClassNotFound {
		t.Fatalf("error = %v, want not-found", err)
	}
	if text := err.Error(); strings.Contains(text, "No such mailbox") || strings.Contains(text, "imap") {
		t.Errorf("error %q carries provider text", text)
	}
}

// The sender allow-list is applied to the parsed From address, not to the SEARCH text: a lookalike address
// and a display name containing the allowed address both match the server's substring search.
func TestSenderAllowlistIsAppliedLocally(t *testing.T) {
	e := newEnvironment(t)
	e.f.folder(t, "INBOX")
	good := e.f.add(t, "INBOX", "Alice <alice@example.net>", "from alice", base)
	e.f.add(t, "INBOX", "bob@example.net", "from bob", base.Add(time.Minute))
	e.f.add(t, "INBOX", "alice@example.net.evil.example", "lookalike", base.Add(2*time.Minute))
	e.f.add(t, "INBOX", `"alice@example.net" <mallory@evil.example>`, "display name", base.Add(3*time.Minute))
	e.f.add(t, "INBOX", "xalice@example.net", "prefix", base.Add(4*time.Minute))

	page := list(t, e, "senders", `{"folder":"INBOX"}`)
	if got := uids(page); len(got) != 1 || got[0] != good {
		t.Errorf("senders connection = %v, want only %d", got, good)
	}
	// Even a sender argument that matches by substring is filtered on the parsed address.
	if got := uids(list(t, e, "open", `{"folder":"INBOX","sender":"alice@example.net"}`)); len(got) != 1 || got[0] != good {
		t.Errorf("sender argument = %v, want only %d", got, good)
	}
	// A sender outside the allow-list is refused before any connection.
	dials := e.f.dials.Load()
	if _, err := e.invoke("infomaniakmail.messages.list", "senders", `{"folder":"INBOX","sender":"bob@example.net"}`); !isInvalidRequest(err) {
		t.Errorf("foreign sender: error = %v, want an invalid request", err)
	}
	if e.f.dials.Load() != dials {
		t.Error("a refused sender caused a connection")
	}
	if got := uids(list(t, e, "senders", `{"folder":"INBOX","sender":"alice@example.net"}`)); len(got) != 1 || got[0] != good {
		t.Errorf("allowed sender argument = %v", got)
	}
}

func TestResultCountIsCappedAndNewestFirst(t *testing.T) {
	e := newEnvironment(t)
	e.f.folder(t, "INBOX")
	var all []uint32
	for i := 0; i < 130; i++ {
		all = append(all, e.f.add(t, "INBOX", "alice@example.net", fmt.Sprintf("m%d", i), base.Add(time.Duration(i)*time.Minute)))
	}

	page := list(t, e, "open", `{"folder":"INBOX"}`)
	if page.Count != defaultLimit || page.Matched != 130 || !page.HasMore || page.Messages[0].UID != all[129] ||
		page.Messages[defaultLimit-1].UID != all[130-defaultLimit] {
		t.Errorf("default page: count %d matched %d more %t first %d", page.Count, page.Matched, page.HasMore, page.Messages[0].UID)
	}
	if page := list(t, e, "open", `{"folder":"INBOX","limit":100}`); page.Count != 100 || !page.HasMore {
		t.Errorf("limit 100: count %d more %t", page.Count, page.HasMore)
	}
	if page := list(t, e, "open", `{"folder":"INBOX","limit":3}`); fmt.Sprint(uids(page)) != fmt.Sprint([]uint32{all[129], all[128], all[127]}) {
		t.Errorf("limit 3 = %v", uids(page))
	}
	for _, arguments := range []string{`{"folder":"INBOX","limit":101}`, `{"folder":"INBOX","limit":0}`, `{"folder":"INBOX","limit":-1}`} {
		if _, err := e.invoke("infomaniakmail.messages.list", "open", arguments); err == nil {
			t.Errorf("%s was accepted", arguments)
		}
	}
}

// A UID window needs the UIDVALIDITY of an earlier listing, which must still match, and spans at most 10000
// UIDs when both ends are given.
func TestUIDWindowIsBoundToFolderAndUIDValidity(t *testing.T) {
	e := newEnvironment(t)
	e.f.folder(t, "INBOX")
	e.f.folder(t, "Other")
	var all []uint32
	for i := 0; i < 6; i++ {
		all = append(all, e.f.add(t, "INBOX", "alice@example.net", fmt.Sprintf("m%d", i), base.Add(time.Duration(i)*time.Minute)))
	}
	e.f.add(t, "Other", "alice@example.net", "other", base)

	first := list(t, e, "open", `{"folder":"INBOX"}`)
	validity := first.UIDValidity
	window := func(extra string) string {
		return fmt.Sprintf(`{"folder":"INBOX","uidvalidity":%d,%s}`, validity, extra)
	}
	if got := uids(list(t, e, "open", window(`"uid_from":2,"uid_to":4`))); fmt.Sprint(got) != fmt.Sprint([]uint32{all[3], all[2], all[1]}) {
		t.Errorf("window 2..4 = %v", got)
	}
	if got := uids(list(t, e, "open", window(`"uid_from":5`))); fmt.Sprint(got) != fmt.Sprint([]uint32{all[5], all[4]}) {
		t.Errorf("window 5..* = %v", got)
	}
	if got := uids(list(t, e, "open", window(`"uid_to":2`))); fmt.Sprint(got) != fmt.Sprint([]uint32{all[1], all[0]}) {
		t.Errorf("window 1..2 = %v", got)
	}

	for name, arguments := range map[string]string{
		"no uidvalidity":        `{"folder":"INBOX","uid_from":2}`,
		"validity without UIDs": fmt.Sprintf(`{"folder":"INBOX","uidvalidity":%d}`, validity),
		"window above the cap":  window(`"uid_from":1,"uid_to":10001`),
		"inverted window":       window(`"uid_from":4,"uid_to":2`),
		"zero uid":              window(`"uid_from":0,"uid_to":2`),
	} {
		if _, err := e.invoke("infomaniakmail.messages.list", "open", arguments); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if got := uids(list(t, e, "open", window(`"uid_from":1,"uid_to":10000`))); len(got) != 6 {
		t.Errorf("a window of exactly 10000 UIDs = %v", got)
	}

	// A UIDVALIDITY of another listing is refused, and the refusal creates no listing.
	_, err := e.invoke("infomaniakmail.messages.list", "open",
		fmt.Sprintf(`{"folder":"INBOX","uidvalidity":%d,"uid_from":1}`, validity+1000))
	if !isInvalidRequest(err) {
		t.Errorf("mismatching uidvalidity: error = %v, want an invalid request", err)
	}
	// The same UID of another folder is a different message: the other folder reports its own pair.
	other := list(t, e, "open", `{"folder":"Other"}`)
	if other.Folder != "Other" || len(other.Messages) != 1 || other.UIDValidity == validity {
		t.Errorf("other folder page = %+v (INBOX validity %d)", other, validity)
	}
	_, err = e.invoke("infomaniakmail.messages.list", "open",
		fmt.Sprintf(`{"folder":"Other","uidvalidity":%d,"uid_from":1}`, validity))
	if !isInvalidRequest(err) {
		t.Errorf("uidvalidity of another folder: error = %v, want an invalid request", err)
	}
}

func TestSubjectAndAddressesAreBounded(t *testing.T) {
	e := newEnvironment(t)
	e.f.folder(t, "INBOX")
	e.f.add(t, "INBOX", "alice@example.net", strings.Repeat("s", 1000), base)
	m := list(t, e, "open", `{"folder":"INBOX"}`).Messages[0]
	if len([]rune(m.Subject)) != maxSubject {
		t.Errorf("subject has %d runes, want %d", len([]rune(m.Subject)), maxSubject)
	}
}
