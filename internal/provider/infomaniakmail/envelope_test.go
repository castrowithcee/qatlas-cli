package infomaniakmail

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
)

// envelopeMessage follows the envelope example of RFC 3501 section 7.4.2, with the members this task adds.
func envelopeMessage(extraHeaders string) string {
	return "Date: Mon, 7 Feb 1994 21:52:25 -0800\r\n" +
		"From: Fred Foobar <foobar@example.net>\r\n" +
		"Sender: Terry Gray <gray@example.org>\r\n" +
		"Reply-To: Replies <replies@example.net>\r\n" +
		"To: mooch@example.com\r\n" +
		"Cc: Cc One <cc1@example.net>, cc2@example.net\r\n" +
		"Bcc: hidden@example.net\r\n" +
		"Subject: afternoon meeting\r\n" +
		"In-Reply-To: <A1@example.net> <A2@example.net>\r\n" +
		"Message-ID: <B27397-0100000@example.net>\r\n" + extraHeaders +
		"MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=outer\r\n\r\n" +
		"--outer\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nhello\r\n" +
		"--outer\r\nContent-Type: image/png; name=logo.png; x-tag=one\r\nContent-Transfer-Encoding: base64\r\n" +
		"Content-Disposition: inline; filename=logo.png\r\nContent-ID: <logo@example.net>\r\n" +
		"Content-Description: Company logo\r\nContent-Language: en, de-CH\r\n" +
		"Content-Location: https://example.net/img/logo.png\r\n\r\ncG5n\r\n--outer--\r\n"
}

func appendAt(t *testing.T, e *environment, raw string, when time.Time) uint32 {
	t.Helper()
	data, err := e.f.user.Append("INBOX", literal{bytes.NewReader([]byte(raw))}, &imap.AppendOptions{Time: when})
	if err != nil {
		t.Fatal(err)
	}
	return uint32(data.UID)
}

func TestEnvelopeAndAttachmentFieldsArriveExactly(t *testing.T) {
	e := newEnvironment(t)
	e.f.folder(t, "INBOX")
	received := time.Date(1994, 2, 8, 6, 53, 1, 0, time.FixedZone("x", 3600))
	uid := appendAt(t, e, envelopeMessage(""), received)
	validity := validityOf(t, e, "INBOX")

	list := list(t, e, "open", `{"folder":"INBOX"}`).Messages[0]
	got := getMessage(t, e, "open", getArgs("INBOX", uid, validity))
	for name, m := range map[string]Message{"list": list, "get": got.Message} {
		want := Message{
			Sender:       []Address{{Name: "Terry Gray", Address: "gray@example.org"}},
			ReplyTo:      []Address{{Name: "Replies", Address: "replies@example.net"}},
			Cc:           []Address{{Name: "Cc One", Address: "cc1@example.net"}, {Address: "cc2@example.net"}},
			Bcc:          []Address{{Address: "hidden@example.net"}},
			InReplyTo:    []string{"<A1@example.net>", "<A2@example.net>"},
			InternalDate: "1994-02-08T05:53:01Z", MessageID: "<B27397-0100000@example.net>",
		}
		if !reflect.DeepEqual(m.Sender, want.Sender) || !reflect.DeepEqual(m.ReplyTo, want.ReplyTo) ||
			!reflect.DeepEqual(m.Cc, want.Cc) || !reflect.DeepEqual(m.Bcc, want.Bcc) ||
			!reflect.DeepEqual(m.InReplyTo, want.InReplyTo) || m.InternalDate != want.InternalDate ||
			m.MessageID != want.MessageID || m.SenderTruncated || m.ReplyToTruncated || m.CcTruncated ||
			m.BccTruncated || m.InReplyToTruncated {
			t.Errorf("%s: message = %+v", name, m)
		}
		if m.Date != "1994-02-08T05:52:25Z" || m.From[0].Address != "foobar@example.net" {
			t.Errorf("%s: existing members changed: %+v", name, m)
		}
	}
	if len(got.Attachments) != 1 {
		t.Fatalf("attachments = %+v", got.Attachments)
	}
	want := Attachment{Part: "2", Name: "logo.png", Type: "image/png", Size: 4, Disposition: "inline",
		ContentID: "<logo@example.net>", Description: "Company logo", Encoding: "base64",
		Language: []string{"en", "de-CH"}, Location: "https://example.net/img/logo.png",
		Parameters: map[string]string{"name": "logo.png", "x-tag": "one"}}
	if !reflect.DeepEqual(got.Attachments[0], want) {
		t.Errorf("attachment = %+v\nwant %+v", got.Attachments[0], want)
	}
}

// An answer without the new members keeps the former output byte for byte.
func TestFormerOutputIsUnchangedWithoutNewMembers(t *testing.T) {
	envelope := &imap.Envelope{
		Date: time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC), Subject: "Hello",
		From: []imap.Address{{Name: "Alice", Mailbox: "alice", Host: "example.net"}},
		To:   []imap.Address{{Mailbox: "box", Host: "example.com"}},
	}
	message := messageOf(7, envelope, []imap.Flag{imap.FlagSeen}, 123, time.Time{})
	raw, err := json.Marshal(MessageContent{Folder: "INBOX", UIDValidity: 9, Message: message,
		Body: "text", BodyType: "text/plain", Attachments: []Attachment{
			attachmentOf(mimePart{path: []int{2}, single: &imap.BodyStructureSinglePart{Type: "application", Subtype: "pdf", Size: 5}}),
		}, AttachmentCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"folder":"INBOX","uidvalidity":9,"uid":7,"date":"2026-02-01T12:00:00Z",` +
		`"from":[{"name":"Alice","address":"alice@example.net"}],"to":[{"address":"box@example.com"}],` +
		`"subject":"Hello","flags":["\\Seen"],"size":123,"body":"text","body_type":"text/plain",` +
		`"body_truncated":false,"attachments":[{"part":"2","type":"application/pdf","size":5}],` +
		`"attachment_count":1,"attachments_truncated":false}`
	if string(raw) != want {
		t.Errorf("output =\n%s\nwant\n%s", raw, want)
	}
}

func TestEnvelopeListsAreBoundedAndMarked(t *testing.T) {
	e := newEnvironment(t)
	e.f.folder(t, "INBOX")
	var cc, ids []string
	for i := 0; i < 21; i++ {
		cc = append(cc, fmt.Sprintf("c%d@example.net", i))
		ids = append(ids, fmt.Sprintf("<id%d@example.net>", i))
	}
	appendAt(t, e, "From: a@example.net\r\nTo: b@example.net\r\nCc: "+strings.Join(cc, ", ")+
		"\r\nIn-Reply-To: "+strings.Join(ids, " ")+"\r\nSubject: x\r\n\r\nbody\r\n", base)
	m := list(t, e, "open", `{"folder":"INBOX"}`).Messages[0]
	if len(m.Cc) != 20 || !m.CcTruncated || m.Cc[19].Address != "c19@example.net" ||
		len(m.InReplyTo) != 20 || !m.InReplyToTruncated || m.InReplyTo[19] != "<id19@example.net>" || m.BccTruncated {
		t.Errorf("cc %d (%t), in_reply_to %d (%t)", len(m.Cc), m.CcTruncated, len(m.InReplyTo), m.InReplyToTruncated)
	}
}

func TestEnvelopeTextIsCleanedAndMalformedIDsDropped(t *testing.T) {
	envelope := &imap.Envelope{
		Sender:    []imap.Address{{Name: "Eve\x00\n" + strings.Repeat("n", 300), Mailbox: "e\tve", Host: "example.net"}},
		InReplyTo: []string{"ok@example.net", "bad id@example.net", "<x>@y", ""},
	}
	m := messageOf(1, envelope, nil, 1, time.Time{})
	if len(m.Sender) != 1 || strings.ContainsAny(m.Sender[0].Name+m.Sender[0].Address, "\x00\n\t") ||
		len([]rune(m.Sender[0].Name)) != maxName {
		t.Errorf("sender = %+v", m.Sender)
	}
	if !reflect.DeepEqual(m.InReplyTo, []string{"<ok@example.net>"}) || m.InReplyToTruncated {
		t.Errorf("in_reply_to = %v", m.InReplyTo)
	}
}

func part(single imap.BodyStructureSinglePart) Attachment {
	single.Type, single.Subtype = "application", "octet-stream"
	return attachmentOf(mimePart{path: []int{1}, single: &single})
}

func TestAttachmentMembersAreCleanedAndBounded(t *testing.T) {
	languages := []string{}
	for i := 0; i < 10; i++ {
		languages = append(languages, "l"+strconv.Itoa(i)+strings.Repeat("x", 40))
	}
	a := part(imap.BodyStructureSinglePart{
		ID: "<a\x00b>" + strings.Repeat("i", 300), Description: "d\r\nesc" + strings.Repeat("d", 300),
		Encoding: strings.Repeat("e", 40),
		Extended: &imap.BodyStructureSinglePartExt{
			Disposition: &imap.BodyStructureDisposition{Value: "att\x01" + strings.Repeat("a", 40)},
			Language:    languages,
		},
	})
	if len(a.ContentID) != 256 || len(a.Description) != 256 || len(a.Encoding) != 32 || len(a.Disposition) != 32 ||
		strings.ContainsAny(a.ContentID+a.Description+a.Disposition, "\x00\x01\r\n") ||
		len(a.Language) != 8 || len(a.Language[0]) != 32 {
		t.Errorf("attachment = %+v", a)
	}
	// Bounds are bytes, never a split character.
	if got := cleanBytes(strings.Repeat("ä", 20), 31); got != strings.Repeat("ä", 15) {
		t.Errorf("cleanBytes = %q", got)
	}
}

func TestLocationWithAccessValuesOrUnparsableIsLeftOut(t *testing.T) {
	for location, want := range map[string]string{
		"https://example.net/a/b.png":   "https://example.net/a/b.png",
		"images/logo.png":               "images/logo.png",
		"https://example.net/a?token=1": "",
		"https://example.net/a?":        "",
		"https://user:pw@example.net/a": "",
		"https://token@example.net/a":   "",
		"https://example.net/a#frag":    "",
		"https://example.net/a#":        "",
		"https://exa\x7fmple.net/":      "",
		"http://[::1/":                  "",
		"%zz":                           "",
		"  ":                            "",
		"":                              "",
		"https://example.net/" + strings.Repeat("p", 2000): "https://example.net/" + strings.Repeat("p", 1004),
	} {
		a := part(imap.BodyStructureSinglePart{Extended: &imap.BodyStructureSinglePartExt{Location: location}})
		if a.Location != want {
			t.Errorf("location %.40q = %.60q, want %.60q", location, a.Location, want)
		}
	}
}

func TestParametersBeyondTheBoundsAreLeftOutAndMarked(t *testing.T) {
	many := map[string]string{}
	for i := 0; i < 17; i++ {
		many["k"+strconv.Itoa(i)] = "v"
	}
	for name, params := range map[string]map[string]string{
		"17 entries": many, "long key": {strings.Repeat("k", 65): "v"}, "long value": {"k": strings.Repeat("v", 257)},
	} {
		a := part(imap.BodyStructureSinglePart{Params: params})
		if a.Parameters != nil || !a.ParametersTruncated {
			t.Errorf("%s: parameters = %v, truncated %t", name, a.Parameters, a.ParametersTruncated)
		}
	}
	delete(many, "k16")
	edge := map[string]string{strings.Repeat("k", 64): strings.Repeat("v", 256), "c\n": "a\x00b"}
	for k, v := range many {
		edge[k] = v
	}
	delete(edge, "k15")
	delete(edge, "k14")
	a := part(imap.BodyStructureSinglePart{Params: edge})
	if len(a.Parameters) != 16 || a.ParametersTruncated || a.Parameters["c "] != "a b" {
		t.Errorf("parameters at the bound = %v, truncated %t", a.Parameters, a.ParametersTruncated)
	}
	if a := part(imap.BodyStructureSinglePart{}); a.Parameters != nil || a.ParametersTruncated {
		t.Errorf("no parameters: %+v", a)
	}
}

// INTERNALDATE and the attachment members come with the requests that already exist.
func TestFetchCountPerCallIsUnchanged(t *testing.T) {
	e := newEnvironment(t)
	e.f.folder(t, "INBOX")
	uid := appendAt(t, e, envelopeMessage(""), base)
	validity := validityOf(t, e, "INBOX")
	fetches := func() int { return len(regexp.MustCompile(`T\d+ UID FETCH`).FindAllString(e.f.wire.String(), -1)) }

	before := fetches()
	list(t, e, "open", `{"folder":"INBOX"}`)
	if n := fetches() - before; n != 1 {
		t.Errorf("messages.list issued %d FETCH commands, want 1", n)
	}
	before = fetches()
	getMessage(t, e, "open", getArgs("INBOX", uid, validity))
	if n := fetches() - before; n != 2 {
		t.Errorf("messages.get issued %d FETCH commands, want 2 (structure and text)", n)
	}
	wire := e.f.wire.String()
	if !strings.Contains(wire, "INTERNALDATE") || !strings.Contains(wire, "BODYSTRUCTURE") {
		t.Errorf("wire lacks INTERNALDATE or BODYSTRUCTURE:\n%s", wire)
	}
}

// Every member a tool returns is in its output schema and in its fields.
func TestEveryOutputMemberIsInSchemaAndFields(t *testing.T) {
	e := newEnvironment(t)
	e.f.folder(t, "INBOX")
	uid := appendAt(t, e, envelopeMessage(""), base)
	validity := validityOf(t, e, "INBOX")
	reg := registry(t)
	for operation, arguments := range map[string]string{
		"infomaniak.mail.messages.list": `{"folder":"INBOX"}`,
		"infomaniak.mail.messages.get":  getArgs("INBOX", uid, validity),
	} {
		raw, err := e.invoke(operation, "open", arguments)
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			t.Fatal(err)
		}
		for _, d := range reg.Provider(Provider) {
			if d.ID != operation {
				continue
			}
			var schema map[string]any
			if err := json.Unmarshal(d.OutputSchema, &schema); err != nil {
				t.Fatal(err)
			}
			fields := map[string]bool{}
			for _, f := range d.Fields {
				fields[f.Name] = true
			}
			var problems []string
			check := func(where string, members map[string]any, properties map[string]any, named bool) {
				for key := range members {
					if _, ok := properties[key]; !ok {
						problems = append(problems, where+key+" (schema)")
					}
					// The messages array is described by the members of its entries.
					if named && !fields[key] && key != "messages" {
						problems = append(problems, where+key+" (fields)")
					}
				}
			}
			properties := schema["properties"].(map[string]any)
			check("", out, properties, true)
			for key := range properties {
				if !fields[key] && key != "messages" {
					problems = append(problems, key+" (schema member without field)")
				}
			}
			if operation == "infomaniak.mail.messages.list" {
				item := properties["messages"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
				check("messages.", out["messages"].([]any)[0].(map[string]any), item, true)
			} else {
				item := properties["attachments"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
				check("attachments.", out["attachments"].([]any)[0].(map[string]any), item, false)
				// The attachment members are named in the field text of attachments.
				var text string
				for _, f := range d.Fields {
					if f.Name == "attachments" {
						text = f.Description
					}
				}
				for key := range item {
					if key != "part" && key != "name" && key != "type" && key != "size" && !strings.Contains(text, key) {
						problems = append(problems, "attachments."+key+" (not described)")
					}
				}
			}
			sort.Strings(problems)
			if len(problems) > 0 {
				t.Errorf("%s: %v", operation, problems)
			}
		}
	}
}
