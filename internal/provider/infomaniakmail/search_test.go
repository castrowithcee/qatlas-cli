package infomaniakmail

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
)

var searchLine = regexp.MustCompile(`(?m)^T\d+ UID SEARCH (.*)$`)

// searchCommands returns the arguments of every UID SEARCH that crossed the wire, as the client sent them.
func searchCommands(e *environment) []string {
	var out []string
	for _, m := range searchLine.FindAllStringSubmatch(e.f.wire.String(), -1) {
		out = append(out, strings.TrimRight(m[1], "\r"))
	}
	return out
}

// wireSearch lists once and returns the single SEARCH sent for it, after checking one connection and one SEARCH.
func wireSearch(t *testing.T, arguments string) string {
	t.Helper()
	e := newEnvironment(t)
	e.f.folder(t, "INBOX")
	e.f.add(t, "INBOX", "alice@example.net", "one", base)
	if _, err := e.invoke("infomaniak.mail.messages.list", "open", arguments); err != nil {
		t.Fatalf("%s: %v", arguments, err)
	}
	got := searchCommands(e)
	if e.f.dials.Load() != 1 || len(got) != 1 {
		t.Fatalf("%s: %d connections and %d SEARCH commands, want 1 and 1", arguments, e.f.dials.Load(), len(got))
	}
	return got[0]
}

func TestSearchWithoutNewArgumentsIsUnchanged(t *testing.T) {
	for arguments, want := range map[string]string{
		`{"folder":"INBOX"}`:                                            `ALL`,
		`{"folder":"INBOX","unread":true}`:                              `UNSEEN`,
		`{"folder":"INBOX","unread":false}`:                             `ALL`,
		`{"folder":"INBOX","since":"2026-02-01","before":"2026-03-01"}`: `SINCE "1-Feb-2026" BEFORE "1-Mar-2026"`,
		`{"folder":"INBOX","sender":"alice@example.net"}`:               `FROM "alice@example.net"`,
		`{"folder":"INBOX","uidvalidity":1,"uid_from":1,"uid_to":5,"unread":true,"since":"2026-02-01"}`: `UID 1:5 SINCE "1-Feb-2026" UNSEEN`,
	} {
		if arguments == `{"folder":"INBOX","uidvalidity":1,"uid_from":1,"uid_to":5,"unread":true,"since":"2026-02-01"}` {
			continue // needs the real UIDVALIDITY; covered by the window tests
		}
		if got := wireSearch(t, arguments); got != want {
			t.Errorf("%s: SEARCH %s, want %s", arguments, got, want)
		}
	}
}

func TestEachSearchFilterIsEncodedAsItsCriterion(t *testing.T) {
	for name, c := range map[string]struct{ arguments, want string }{
		"to":      {`"to":"bob@example.org"`, `TO "bob@example.org"`},
		"cc":      {`"cc":"carol@example.org"`, `CC "carol@example.org"`},
		"bcc":     {`"bcc":"dave@example.org"`, `BCC "dave@example.org"`},
		"subject": {`"subject":"Invoice 42"`, `SUBJECT "Invoice 42"`},
		"body":    {`"body":"needle"`, `BODY "needle"`},
		"text":    {`"text":"needle"`, `TEXT "needle"`},
		"header": {`"header":[{"name":"X-Spam-Flag","value":"YES"},{"name":"List-Id","value":""}]`,
			`HEADER "X-Spam-Flag" "YES" HEADER "List-Id" ""`},
		"header from":    {`"header":[{"name":"From","value":"bob@example.org"}]`, `FROM "bob@example.org"`},
		"larger":         {`"larger":1000`, `LARGER 1000`},
		"smaller":        {`"smaller":4294967295`, `SMALLER 4294967295`},
		"larger smaller": {`"larger":10,"smaller":11`, `LARGER 10 SMALLER 11`},
		"on":             {`"on":"2026-02-03"`, `ON "3-Feb-2026"`},
		"sent_since":     {`"sent_since":"2026-02-03"`, `SENTSINCE "3-Feb-2026"`},
		"sent_before":    {`"sent_before":"2026-02-03"`, `SENTBEFORE "3-Feb-2026"`},
		"sent_since and before": {`"sent_since":"2026-02-03","sent_before":"2026-02-10"`,
			`SENTSINCE "3-Feb-2026" SENTBEFORE "10-Feb-2026"`},
		"sent_on":      {`"sent_on":"2026-02-03"`, `SENTON "3-Feb-2026"`},
		"flagged":      {`"flagged":true`, `FLAGGED`},
		"not flagged":  {`"flagged":false`, `UNFLAGGED`},
		"answered":     {`"answered":true`, `ANSWERED`},
		"not answered": {`"answered":false`, `UNANSWERED`},
		"deleted":      {`"deleted":true`, `DELETED`},
		"not deleted":  {`"deleted":false`, `UNDELETED`},
		"draft":        {`"draft":true`, `DRAFT`},
		"not draft":    {`"draft":false`, `UNDRAFT`},
		"seen":         {`"seen":true`, `SEEN`},
		"not seen":     {`"seen":false`, `UNSEEN`},
		"keyword":      {`"keyword":["$Forwarded","work"]`, `KEYWORD $Forwarded KEYWORD work`},
		"unkeyword":    {`"unkeyword":["$Junk"]`, `UNKEYWORD $Junk`},
		"keyword pair": {`"keyword":["a"],"unkeyword":["b"]`, `KEYWORD a UNKEYWORD b`},
		"combined with since": {`"since":"2026-02-01","subject":"x","unread":true`,
			`SINCE "1-Feb-2026" SUBJECT "x" UNSEEN`},
		"combined with sender": {`"sender":"alice@example.net","text":"x"`, `FROM "alice@example.net" TEXT "x"`},
	} {
		got := wireSearch(t, `{"folder":"INBOX",`+c.arguments+`}`)
		if got != c.want {
			t.Errorf("%s: SEARCH %s, want %s", name, got, c.want)
		}
	}
}

// Special characters never form an atom or another command: they are an IMAP string, quoted or as a literal.
func TestSearchSpecialCharactersAreIMAPStrings(t *testing.T) {
	for name, c := range map[string]struct{ value, want string }{
		"quote and backslash": {`a\"b\\c`, `SUBJECT "a\"b\\c"`},
		"injection attempt":   {`x\" OR ALL \"`, `SUBJECT "x\" OR ALL \""`},
		"parentheses":         {`(ALL)`, `SUBJECT "(ALL)"`},
		"space and star":      {`a *%`, `SUBJECT "a *%"`},
	} {
		got := wireSearch(t, `{"folder":"INBOX","subject":"`+c.value+`"}`)
		if got != c.want {
			t.Errorf("%s: SEARCH %s, want %s", name, got, c.want)
		}
	}
	// Non-ASCII text cannot be quoted: it is a literal, and the charset is announced.
	got := wireSearch(t, `{"folder":"INBOX","body":"Grüße"}`)
	if !strings.Contains(got, "CHARSET UTF-8") || !strings.Contains(got, "BODY {") {
		t.Errorf("non-ASCII body: SEARCH %q, want CHARSET UTF-8 and a literal", got)
	}
}

func TestSearchFiltersNarrowTheResult(t *testing.T) {
	e := newEnvironment(t)
	e.f.folder(t, "INBOX")
	a := e.f.add(t, "INBOX", "alice@example.net", "invoice", base, imap.FlagFlagged, imap.Flag("work"))
	b := e.f.add(t, "INBOX", "bob@example.net", "hello", base.Add(48*time.Hour), imap.FlagSeen)
	for arguments, want := range map[string][]uint32{
		`{"folder":"INBOX","subject":"invoice"}`:                          {a},
		`{"folder":"INBOX","flagged":true}`:                               {a},
		`{"folder":"INBOX","flagged":false}`:                              {b},
		`{"folder":"INBOX","seen":true}`:                                  {b},
		`{"folder":"INBOX","seen":false}`:                                 {a},
		`{"folder":"INBOX","keyword":["work"]}`:                           {a},
		`{"folder":"INBOX","unkeyword":["work"]}`:                         {b},
		`{"folder":"INBOX","on":"2026-02-01"}`:                            {a},
		`{"folder":"INBOX","sent_on":"2026-02-03"}`:                       {b},
		`{"folder":"INBOX","header":[{"name":"Subject","value":"hell"}]}`: {b},
		`{"folder":"INBOX","text":"BODY-CANARY-hello"}`:                   {b},
		`{"folder":"INBOX","subject":"invoice","seen":true}`:              {},
	} {
		if got := uids(list(t, e, "open", arguments)); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s = %v, want %v", arguments, got, want)
		}
	}
}

func TestInvalidSearchFiltersAreRefusedWithoutIO(t *testing.T) {
	e := newEnvironment(t)
	e.f.folder(t, "INBOX")
	long := strings.Repeat("a", 257)
	six := `[{"name":"A","value":""},{"name":"B","value":""},{"name":"C","value":""},{"name":"D","value":""}]`
	for name, extra := range map[string]string{
		"overlong to":             `"to":"` + long + `"`,
		"overlong subject":        `"subject":"` + long + `"`,
		"overlong body":           `"body":"` + long + `"`,
		"overlong multibyte":      `"text":"` + strings.Repeat("ü", 129) + `"`,
		"empty text":              `"text":""`,
		"control character":       `"subject":"a\u0001b"`,
		"newline":                 `"body":"a\nb"`,
		"delete character":        `"cc":"a\u007fb"`,
		"empty header name":       `"header":[{"name":"","value":"x"}]`,
		"header name colon":       `"header":[{"name":"X:Y","value":"x"}]`,
		"header name space":       `"header":[{"name":"X Y","value":"x"}]`,
		"header name non-ASCII":   `"header":[{"name":"Xü","value":"x"}]`,
		"overlong header name":    `"header":[{"name":"` + strings.Repeat("N", 65) + `","value":"x"}]`,
		"overlong header value":   `"header":[{"name":"X","value":"` + long + `"}]`,
		"header value control":    `"header":[{"name":"X","value":"a\u0000"}]`,
		"four headers":            `"header":` + six,
		"unknown header member":   `"header":[{"name":"X","value":"","extra":1}]`,
		"six keywords":            `"keyword":["a","b","c","d","e","f"]`,
		"six unkeywords":          `"unkeyword":["a","b","c","d","e","f"]`,
		"keyword backslash":       `"keyword":["\\Seen"]`,
		"unkeyword backslash":     `"unkeyword":["\\Seen"]`,
		"keyword with space":      `"keyword":["a b"]`,
		"keyword with paren":      `"keyword":["a(b"]`,
		"keyword with quote":      `"keyword":["a\"b"]`,
		"keyword with bracket":    `"keyword":["a]"]`,
		"keyword with star":       `"keyword":["a*"]`,
		"empty keyword":           `"keyword":[""]`,
		"overlong keyword":        `"keyword":["` + strings.Repeat("k", 65) + `"]`,
		"same keyword both":       `"keyword":["Work"],"unkeyword":["work"]`,
		"invalid date on":         `"on":"2026-02-30"`,
		"invalid sent_since":      `"sent_since":"yesterday"`,
		"invalid sent_before":     `"sent_before":"2026-13-01"`,
		"invalid sent_on":         `"sent_on":"2026-1-1"`,
		"on with since":           `"on":"2026-02-03","since":"2026-02-01"`,
		"on with before":          `"on":"2026-02-03","before":"2026-02-05"`,
		"sent_on with since":      `"sent_on":"2026-02-03","sent_since":"2026-02-01"`,
		"sent_on with before":     `"sent_on":"2026-02-03","sent_before":"2026-02-05"`,
		"sent_since not before":   `"sent_since":"2026-02-03","sent_before":"2026-02-03"`,
		"sent_since after":        `"sent_since":"2026-02-04","sent_before":"2026-02-03"`,
		"seen with unread":        `"seen":false,"unread":true`,
		"seen true with unread":   `"seen":true,"unread":true`,
		"smaller equals larger":   `"larger":5,"smaller":5`,
		"smaller below larger":    `"larger":5,"smaller":4`,
		"smaller zero":            `"smaller":0`,
		"negative larger":         `"larger":-1`,
		"larger too big":          `"larger":4294967296`,
		"fractional larger":       `"larger":1.5`,
		"flag as string":          `"flagged":"true"`,
		"unknown search argument": `"or":"ALL"`,
	} {
		before := e.f.dials.Load()
		if _, err := e.invoke("infomaniak.mail.messages.list", "open", `{"folder":"INBOX",`+extra+`}`); err == nil {
			t.Errorf("%s was accepted", name)
		}
		if e.f.dials.Load() != before || e.f.reads.Load() != 0 {
			t.Errorf("%s: %d dials, %d secret reads, want none", name, e.f.dials.Load()-before, e.f.reads.Load())
		}
	}
	if len(searchCommands(e)) != 0 {
		t.Error("a refused filter reached the server")
	}
}

// The checks of checkQuery hold even where the schema does not catch the value.
func TestCheckQueryRefusesWhatTheSchemaAllows(t *testing.T) {
	bound, err := parseScope([]string{"mailbox/box@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	empty, tab, name := "", "a\tb", "X"
	for name, input := range map[string]messagesArguments{
		"empty to":   {Folder: "INBOX", searchArguments: searchArguments{To: &empty}},
		"tab":        {Folder: "INBOX", searchArguments: searchArguments{Text: &tab}},
		"bad header": {Folder: "INBOX", searchArguments: searchArguments{Header: []headerArgument{{Name: "a:b"}}}},
		"keyword":    {Folder: "INBOX", searchArguments: searchArguments{Keyword: []string{`\Seen`}}},
	} {
		if _, err := bound.checkQuery(input); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := bound.checkQuery(messagesArguments{Folder: "INBOX",
		searchArguments: searchArguments{Header: []headerArgument{{Name: name, Value: ""}}}}); err != nil {
		t.Errorf("an empty header value is valid: %v", err)
	}
}

// Text and header filters only narrow the server search: the allow-list still checks each parsed From address.
func TestSearchFiltersDoNotWidenTheSenderAllowlist(t *testing.T) {
	e := newEnvironment(t)
	e.f.folder(t, "INBOX")
	good := e.f.add(t, "INBOX", "alice@example.net", "report", base)
	e.f.add(t, "INBOX", "bob@example.net", "report", base.Add(time.Minute))
	e.f.add(t, "INBOX", `"alice@example.net" <mallory@example.org>`, "report", base.Add(2*time.Minute))
	e.f.add(t, "INBOX", "alice@example.net.example.org", "report", base.Add(3*time.Minute))

	for _, arguments := range []string{
		`{"folder":"INBOX","subject":"report"}`,
		`{"folder":"INBOX","text":"report"}`,
		`{"folder":"INBOX","body":"BODY-CANARY"}`,
		`{"folder":"INBOX","header":[{"name":"Subject","value":"report"}]}`,
		`{"folder":"INBOX","header":[{"name":"From","value":"bob@example.net"}]}`,
		`{"folder":"INBOX","header":[{"name":"From","value":"example"}]}`,
	} {
		got := uids(list(t, e, "senders", arguments))
		for _, uid := range got {
			if uid != good {
				t.Errorf("%s returned foreign message %d", arguments, uid)
			}
		}
	}
	if got := uids(list(t, e, "senders", `{"folder":"INBOX","text":"report"}`)); len(got) != 1 || got[0] != good {
		t.Errorf("allow-listed text search = %v, want only %d", got, good)
	}
	if got := uids(list(t, e, "senders", `{"folder":"INBOX","header":[{"name":"From","value":"bob@example.net"}]}`)); len(got) != 0 {
		t.Errorf("a From header filter for a foreign sender returned %v", got)
	}
}
