package infomaniakmail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

var appendCommand = regexp.MustCompile(`T\d+ APPEND\b`)

// draftEnvironment has INBOX and a Drafts folder marked \Drafts, and connections that offer the three draft
// tools and release dir for reading.
func draftEnvironment(t *testing.T, dir string) *environment {
	t.Helper()
	e := newEnvironmentWith(t, func(cfg *config.Config) {
		drafting := func(targets ...string) config.Connection {
			c := cfg.Connections["open"]
			c.Targets = append([]string{"mailbox/" + mailbox}, targets...)
			c.Tools = []string{draftsCreate.ID, draftsUpdate.ID, draftsDelete.ID}
			c.Files = config.Files{Read: []string{dir}}
			return c
		}
		cfg.Connections["draft"] = drafting()
		cfg.Connections["draftfolders"] = drafting("folder/INBOX", "folder/Drafts")
		cfg.Connections["draftnodrafts"] = drafting("folder/INBOX")
		cfg.Connections["draftsenders"] = drafting("sender/alice@example.net")
		nofiles := drafting()
		nofiles.Files = config.Files{}
		cfg.Connections["draftnofiles"] = nofiles
		notools := drafting()
		notools.Tools = nil
		cfg.Connections["draftnotools"] = notools
	})
	for _, name := range []string{"INBOX", "Drafts"} {
		e.f.folder(t, name)
	}
	markSpecialUse(t, e.f, "Drafts", imap.MailboxAttrDrafts)
	return e
}

func createDraft(t *testing.T, e *environment, connection, arguments string) DraftResult {
	t.Helper()
	result, err := e.confirmed("infomaniakmail.drafts.create", connection, arguments)
	if err != nil {
		t.Fatalf("drafts.create %s: %v", arguments, err)
	}
	var got DraftResult
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatalf("decode %q: %v", result, err)
	}
	return got
}

func jsonString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func TestDraftsCreateStoresAStructuredDraftWithAttachments(t *testing.T) {
	dir := t.TempDir()
	file := []byte("%PDF-local-attachment\x00\x01\xff")
	if err := os.WriteFile(filepath.Join(dir, "Rechnung Ü.pdf"), file, 0o600); err != nil {
		t.Fatal(err)
	}
	inline := []byte("inline attachment content")
	e := draftEnvironment(t, dir)
	args := fmt.Sprintf(`{"to":["a@example.net","b@example.net"],"cc":["c@example.net"],"bcc":["d@example.net"],`+
		`"subject":"Grüße: Rechnung","body":"Hallo Wörld\nBODY-CANARY","attachments":[`+
		`{"local_path":%s},{"name":"note.txt","content_base64":%q,"content_type":"text/plain"}]}`,
		jsonString(filepath.Join(dir, "Rechnung Ü.pdf")), base64.StdEncoding.EncodeToString(inline))

	got := createDraft(t, e, "draft", args)
	validity := validityOf(t, e, "Drafts")
	if got.Folder != "Drafts" || got.UID == 0 || got.UIDValidity != validity || got.Size <= 0 || len(got.Attachments) != 2 {
		t.Fatalf("result = %+v", got)
	}
	sum := sha256.Sum256(file)
	if first := got.Attachments[0]; first.Name != "Rechnung Ü.pdf" || first.Type != "application/pdf" ||
		first.Size != int64(len(file)) || first.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("attachment 0 = %+v", first)
	}
	sum = sha256.Sum256(inline)
	if second := got.Attachments[1]; second.Name != "note.txt" || second.Type != "text/plain" ||
		second.Size != int64(len(inline)) || second.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("attachment 1 = %+v", second)
	}
	raw, _ := json.Marshal(got)
	for _, leaked := range []string{"BODY-CANARY", base64.StdEncoding.EncodeToString(inline), "Hallo"} {
		if strings.Contains(string(raw), leaked) {
			t.Errorf("result carries content %q: %s", leaked, raw)
		}
	}

	if flags := flagsOf(t, e, "Drafts", got.UID); len(flags) != 1 || !hasFlag(flags, imap.FlagDraft) {
		t.Errorf("flags = %v, want only \\Draft", flags)
	}
	if commands(e, appendCommand) != 1 || commands(e, storeCommand)+commands(e, moveCommand)+commands(e, expungeCommand) != 0 {
		t.Errorf("want exactly one APPEND and nothing else:\n%s", e.f.wire.String())
	}
	read := getMessage(t, e, "open", getArgs("Drafts", got.UID, validity))
	if read.Subject != "Grüße: Rechnung" || len(read.From) != 1 || read.From[0].Address != mailbox || len(read.To) != 2 ||
		!strings.HasPrefix(read.Body, "Hallo Wörld\nBODY-CANARY") || read.BodyType != "text/plain" ||
		len(read.Attachments) != 2 || read.Attachments[0].Name != "Rechnung Ü.pdf" || read.Attachments[1].Name != "note.txt" {
		t.Errorf("stored draft = %+v", read)
	}
	if len(uids(list(t, e, "open", `{"folder":"INBOX"}`))) != 0 {
		t.Errorf("INBOX changed")
	}
}

func TestDraftsCreateWithoutAttachmentsIsOnePlainPart(t *testing.T) {
	e := draftEnvironment(t, t.TempDir())
	got := createDraft(t, e, "draft", `{"to":["a@example.net"]}`)
	if len(got.Attachments) != 0 {
		t.Errorf("attachments = %+v", got.Attachments)
	}
	read := getMessage(t, e, "open", getArgs("Drafts", got.UID, validityOf(t, e, "Drafts")))
	if read.Subject != "" || read.Body != "" || len(read.Attachments) != 0 {
		t.Errorf("draft = %+v", read)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestDraftToolsNeedConfirmationToolsListAndAFileRelease(t *testing.T) {
	e := draftEnvironment(t, t.TempDir())
	e.f.addRaw(t, "Drafts", header(mailbox, "d")+"\r\nbody\r\n", imap.FlagDraft)
	validity := validityOf(t, e, "Drafts")
	dials := e.f.dials.Load()
	create := `{"to":["a@example.net"]}`
	update := refArgs("Drafts", 1, validity, `,"to":["a@example.net"]`)
	del := refArgs("Drafts", 1, validity, "")
	for _, tool := range []struct{ name, args string }{{"create", create}, {"update", update}, {"delete", del}} {
		operation := "infomaniakmail.drafts." + tool.name
		var needed *application.ConfirmationRequiredError
		if _, err := e.invoke(operation, "draft", tool.args); !asError(err, &needed) {
			t.Errorf("%s without confirm: err = %v, want confirmation required", tool.name, err)
		}
		if _, err := e.confirmed(operation, "draftnotools", tool.args); err == nil {
			t.Errorf("%s ran through a connection without a tools list", tool.name)
		}
		if _, err := e.confirmed(operation, "open", tool.args); err == nil {
			t.Errorf("%s ran through a connection without a tools list", tool.name)
		}
	}
	// create and update read local files and need a release; delete does not.
	for _, tool := range []struct{ name, args string }{{"create", create}, {"update", update}} {
		if _, err := e.confirmed("infomaniakmail.drafts."+tool.name, "draftnofiles", tool.args); err == nil {
			t.Errorf("%s ran through a connection without a file release", tool.name)
		}
	}
	if e.f.dials.Load() != dials || commands(e, appendCommand)+commands(e, expungeCommand)+commands(e, storeCommand) != 0 {
		t.Errorf("a refused call reached the server:\n%s", e.f.wire.String())
	}
	if _, err := e.confirmed("infomaniakmail.drafts.delete", "draftnofiles", del); err != nil {
		t.Errorf("delete without a file release: %v", err)
	}
}

func TestDraftsFolderIsTheOneAllowedDraftsFolder(t *testing.T) {
	args := `{"to":["a@example.net"]}`
	for _, tc := range []struct {
		name       string
		caps       imap.CapSet
		connection string
		setup      func(*testing.T, *environment)
	}{
		{"outside the folder targets", nil, "draftnodrafts", nil},
		{"no SPECIAL-USE", imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapMove: {}, imap.CapUIDPlus: {}}, "draft", nil},
		{"none marked", nil, "draft", func(t *testing.T, e *environment) {
			markSpecialUse(t, e.f, "Drafts", imap.MailboxAttrSent)
		}},
		{"two marked", nil, "draft", func(t *testing.T, e *environment) {
			e.f.folder(t, "More")
			markSpecialUse(t, e.f, "More", imap.MailboxAttrDrafts)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := draftEnvironment(t, t.TempDir())
			if tc.caps != nil {
				e = draftEnvironmentCaps(t, tc.caps)
			}
			if tc.setup != nil {
				tc.setup(t, e)
			}
			_, err := e.confirmed("infomaniakmail.drafts.create", tc.connection, args)
			if !isInvalidRequest(err) {
				t.Fatalf("err = %v, want an invalid request", err)
			}
			for _, leaked := range []string{"Drafts", "More", "INBOX"} {
				if strings.Contains(err.Error(), leaked) {
					t.Errorf("error %q names %s", err, leaked)
				}
			}
			if commands(e, appendCommand) != 0 {
				t.Errorf("an APPEND was sent:\n%s", e.f.wire.String())
			}
		})
	}
}

func draftEnvironmentCaps(t *testing.T, caps imap.CapSet) *environment {
	t.Helper()
	dir := t.TempDir()
	e := newEnvironmentCaps(t, caps, func(cfg *config.Config) {
		c := cfg.Connections["open"]
		c.Tools = []string{draftsCreate.ID, draftsUpdate.ID, draftsDelete.ID}
		c.Files = config.Files{Read: []string{dir}}
		cfg.Connections["draft"] = c
	})
	for _, name := range []string{"INBOX", "Drafts"} {
		e.f.folder(t, name)
	}
	markSpecialUse(t, e.f, "Drafts", imap.MailboxAttrDrafts)
	return e
}

// An injection attempt in any field is refused as an invalid request before a secret is read or a
// connection opened.
func TestDraftsCreateRefusesHeaderInjectionInEveryField(t *testing.T) {
	e := draftEnvironment(t, t.TempDir())
	dials, reads := e.f.dials.Load(), e.f.reads.Load()
	content := base64.StdEncoding.EncodeToString([]byte("x"))
	attachment := func(name, contentType string) string {
		return fmt.Sprintf(`"attachments":[{"name":%s,"content_base64":%q,"content_type":%s}]`,
			jsonString(name), content, jsonString(contentType))
	}
	injections := []string{"x\r\nBcc: evil@example.org", "x\nBcc: evil@example.org", "x\rBcc: evil@example.org",
		"x Bcc: evil@example.org", "x\u0085Bcc: evil@example.org", "x\x00y", "x\x1by", "x\ty"}
	var cases []string
	for _, bad := range injections {
		b := jsonString(bad)
		addr := jsonString("a@example.net" + bad)
		display := jsonString("Evil" + bad + " <a@example.net>")
		cases = append(cases,
			`{"to":["a@example.net"],"subject":`+b+`}`,
			`{"to":[`+addr+`]}`,
			`{"to":[`+display+`]}`,
			`{"to":["a@example.net"],"cc":[`+addr+`]}`,
			`{"to":["a@example.net"],"bcc":[`+addr+`]}`,
			`{"to":["a@example.net"],`+attachment("x"+bad+".pdf", "application/pdf")+`}`,
			`{"to":["a@example.net"],`+attachment("a.pdf", "application/pdf"+bad)+`}`,
		)
	}
	cases = append(cases,
		`{"to":["Alice <a@example.net>"]}`, `{"to":["a@example.net, b@example.net"]}`, `{"to":["<a@example.net>"]}`,
		`{"to":["a@example.net"],`+attachment("../x.pdf", "application/pdf")+`}`,
		`{"to":["a@example.net"],`+attachment(`a\x.pdf`, "application/pdf")+`}`,
		`{"to":["a@example.net"],`+attachment("a.pdf", "multipart/mixed")+`}`,
		`{"to":["a@example.net"],`+attachment("a.pdf", "message/rfc822")+`}`,
		`{"to":["a@example.net"],`+attachment("a.pdf", "application/pdf; boundary=x")+`}`,
		`{"to":["a@example.net"],`+attachment("a.pdf", "text")+`}`,
		`{"to":["a@example.net"],"subject":"`+strings.Repeat("x", 257)+`"}`,
	)
	for _, args := range cases {
		_, err := e.confirmed("infomaniakmail.drafts.create", "draft", args)
		if err == nil {
			t.Errorf("accepted %s", args)
			continue
		}
		if !isInvalidRequest(err) {
			t.Errorf("%.80q: err = %v, want an invalid request", args, err)
		}
	}
	if e.f.dials.Load() != dials || e.f.reads.Load() != reads || commands(e, appendCommand) != 0 {
		t.Errorf("dials %d -> %d, secret reads %d -> %d, APPENDs %d", dials, e.f.dials.Load(), reads, e.f.reads.Load(),
			commands(e, appendCommand))
	}
}

// build never lets a validated but hostile value escape its header: the parsed message has exactly the
// headers Qatlas writes, and every value reads back as given.
func TestDraftBuildWritesOnlyItsOwnHeaders(t *testing.T) {
	now = func() time.Time { return time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC) }
	t.Cleanup(func() { now = time.Now })
	subject := "Bcc: evil@example.org; X-Injected: 1 " + strings.Repeat("Grüße ", 30)
	name := `Ü "quoted"; filename=evil.exe.pdf`
	spec := draftSpec{
		from: mailbox, to: []string{"a@example.net", "b@example.net"}, cc: []string{"c@example.net"},
		bcc: []string{"d@example.net"}, subject: subject, body: "line one\r\nline two\rline three\n.\n\nBcc: not a header\n",
		attachments: []draftAttachment{{name: name, contentType: "application/pdf", content: bytes.Repeat([]byte{0, 1, 2, 255}, 100)}},
	}
	raw, summary, err := spec.build()
	if err != nil {
		t.Fatal(err)
	}
	if len(summary) != 1 || summary[0].Name != name || summary[0].Size != 400 {
		t.Errorf("summary = %+v", summary)
	}
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for key := range message.Header {
		keys = append(keys, key)
	}
	want := map[string]bool{"From": true, "To": true, "Cc": true, "Bcc": true, "Subject": true, "Date": true,
		"Message-Id": true, "Mime-Version": true, "Content-Type": true}
	if len(keys) != len(want) {
		t.Errorf("headers = %v", keys)
	}
	for _, key := range keys {
		if !want[key] {
			t.Errorf("unexpected header %s", key)
		}
	}
	decoded, err := new(mime.WordDecoder).DecodeHeader(message.Header.Get("Subject"))
	if err != nil || decoded != subject {
		t.Errorf("subject = %q, %v", decoded, err)
	}
	for key, wantList := range map[string][]string{"To": {"a@example.net", "b@example.net"}, "Cc": {"c@example.net"},
		"Bcc": {"d@example.net"}, "From": {mailbox}} {
		list, err := message.Header.AddressList(key)
		if err != nil || len(list) != len(wantList) {
			t.Errorf("%s = %v, %v", key, list, err)
			continue
		}
		for i := range list {
			if list[i].Address != wantList[i] || list[i].Name != "" {
				t.Errorf("%s[%d] = %v", key, i, list[i])
			}
		}
	}
	if date, err := message.Header.Date(); err != nil || !date.Equal(now()) {
		t.Errorf("date = %v, %v", date, err)
	}
	if id := message.Header.Get("Message-Id"); !regexp.MustCompile(`^<[0-9a-f]{32}@example\.com>$`).MatchString(id) {
		t.Errorf("message id = %q", id)
	}
	for _, line := range strings.Split(string(raw[:bytes.Index(raw, []byte("\r\n\r\n"))]), "\r\n") {
		if len(line) > 998 {
			t.Errorf("header line of %d bytes", len(line))
		}
	}
	if bytes.Contains(raw, []byte("\n")) && bytes.Contains(bytes.ReplaceAll(raw, []byte("\r\n"), nil), []byte("\n")) {
		t.Errorf("a bare line feed in the message")
	}
	// The injected-looking text stays inside the one text part and is quoted-printable encoded.
	mediaType, params, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/mixed" || params["boundary"] == "" {
		t.Fatalf("content type = %q, %v", mediaType, err)
	}
}

func TestDraftsUpdateReplacesOneDraftWithOneAppendAndOneExpunge(t *testing.T) {
	e := draftEnvironment(t, t.TempDir())
	old := createDraft(t, e, "draft", `{"to":["a@example.net"],"subject":"old","body":"OLD-CANARY"}`)
	other := createDraft(t, e, "draft", `{"to":["z@example.net"],"subject":"other"}`)
	// A message another client has marked \Deleted stays: only the one UID is expunged.
	marked := e.f.addRaw(t, "Drafts", header(mailbox, "marked")+"\r\nbody\r\n", imap.FlagDraft, imap.FlagDeleted)
	validity := validityOf(t, e, "Drafts")
	before := commands(e, appendCommand)

	result, err := e.confirmed("infomaniakmail.drafts.update", "draft",
		refArgs("Drafts", old.UID, validity, `,"to":["b@example.net"],"subject":"new","body":"NEW-CANARY","attachments":[{"name":"a.txt","content_base64":"aGk="}]`))
	if err != nil {
		t.Fatal(err)
	}
	var got DraftResult
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatal(err)
	}
	if got.Folder != "Drafts" || got.ReplacedUID != old.UID || got.UID == 0 || got.UID == old.UID || got.UIDValidity != validity ||
		len(got.Attachments) != 1 || got.Attachments[0].Name != "a.txt" || got.Attachments[0].Size != 2 {
		t.Fatalf("result = %+v", got)
	}
	present := uids(list(t, e, "open", `{"folder":"Drafts"}`))
	if len(present) != 3 || !contains(present, other.UID) || !contains(present, marked) || !contains(present, got.UID) ||
		contains(present, old.UID) {
		t.Errorf("drafts = %v", present)
	}
	if !hasFlag(flagsOf(t, e, "Drafts", got.UID), imap.FlagDraft) {
		t.Errorf("new draft flags = %v", flagsOf(t, e, "Drafts", got.UID))
	}
	read := getMessage(t, e, "open", getArgs("Drafts", got.UID, validity))
	if read.Subject != "new" || !strings.HasPrefix(read.Body, "NEW-CANARY") || len(read.To) != 1 || read.To[0].Address != "b@example.net" {
		t.Errorf("new draft = %+v", read)
	}
	wire := e.f.wire.String()
	if commands(e, appendCommand)-before != 1 || commands(e, expungeCommand) != 1 ||
		!regexp.MustCompile(`T\d+ UID EXPUNGE `+strconv.Itoa(int(old.UID))+`\b`).MatchString(wire) ||
		regexp.MustCompile(`T\d+ EXPUNGE`).MatchString(wire) {
		t.Errorf("want one APPEND and one UID EXPUNGE of the old UID:\n%s", wire)
	}
	if strings.Contains(result, "NEW-CANARY") {
		t.Errorf("result carries content: %s", result)
	}
}

func contains(list []uint32, uid uint32) bool {
	for _, item := range list {
		if item == uid {
			return true
		}
	}
	return false
}

// Neither draft tool reaches a message without \Draft, a message outside the drafts folder, or a stale
// UIDVALIDITY, and nothing is written.
func TestDraftChangesAreBoundToDraftsInTheDraftsFolder(t *testing.T) {
	e := draftEnvironment(t, t.TempDir())
	plain := e.f.addRaw(t, "Drafts", header(mailbox, "plain")+"\r\nbody\r\n")
	inbox := e.f.addRaw(t, "INBOX", header(mailbox, "inbox")+"\r\nbody\r\n", imap.FlagDraft)
	draft := createDraft(t, e, "draft", `{"to":["a@example.net"]}`)
	validity, inboxValidity := validityOf(t, e, "Drafts"), validityOf(t, e, "INBOX")
	before := commands(e, appendCommand)

	update := func(folder string, uid, validity uint32) string {
		return refArgs(folder, uid, validity, `,"to":["b@example.net"]`)
	}
	for _, tc := range []struct {
		name, tool, args string
		missing          bool
	}{
		{"update without flag", "update", update("Drafts", plain, validity), true},
		{"delete without flag", "delete", refArgs("Drafts", plain, validity, ""), true},
		{"update missing uid", "update", update("Drafts", 9999, validity), true},
		{"delete missing uid", "delete", refArgs("Drafts", 9999, validity, ""), true},
		{"update in inbox", "update", update("INBOX", inbox, inboxValidity), false},
		{"delete in inbox", "delete", refArgs("INBOX", inbox, inboxValidity, ""), false},
		{"update stale validity", "update", update("Drafts", draft.UID, validity+1), false},
		{"delete stale validity", "delete", refArgs("Drafts", draft.UID, validity+1, ""), false},
	} {
		_, err := e.confirmed("infomaniakmail.drafts."+tc.tool, "draft", tc.args)
		switch {
		case tc.missing && classOf(err) != provider.ClassNotFound, !tc.missing && !isInvalidRequest(err):
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}
	if present := uids(list(t, e, "open", `{"folder":"Drafts"}`)); len(present) != 2 || !contains(present, plain) ||
		!contains(present, draft.UID) {
		t.Errorf("drafts = %v", present)
	}
	if !contains(uids(list(t, e, "open", `{"folder":"INBOX"}`)), inbox) {
		t.Errorf("the inbox message is gone")
	}
	if commands(e, appendCommand) != before || commands(e, expungeCommand) != 0 || commands(e, storeCommand) != 0 {
		t.Errorf("a refused change reached the server:\n%s", e.f.wire.String())
	}
}

func TestDraftsDeleteRemovesOnlyThatDraft(t *testing.T) {
	e := draftEnvironment(t, t.TempDir())
	keep := createDraft(t, e, "draftfolders", `{"to":["a@example.net"],"subject":"keep"}`)
	gone := createDraft(t, e, "draftfolders", `{"to":["a@example.net"],"subject":"gone"}`)
	marked := e.f.addRaw(t, "Drafts", header(mailbox, "marked")+"\r\nbody\r\n", imap.FlagDraft, imap.FlagDeleted)
	validity := validityOf(t, e, "Drafts")

	result, err := e.confirmed("infomaniakmail.drafts.delete", "draftfolders", refArgs("Drafts", gone.UID, validity, ""))
	if err != nil {
		t.Fatal(err)
	}
	var got MessageState
	if err := json.Unmarshal([]byte(result), &got); err != nil || got.Folder != "Drafts" || got.UID != gone.UID || got.UIDValidity != validity {
		t.Fatalf("result = %s, %v", result, err)
	}
	if present := uids(list(t, e, "open", `{"folder":"Drafts"}`)); len(present) != 2 || !contains(present, keep.UID) ||
		!contains(present, marked) {
		t.Errorf("drafts = %v", present)
	}
	if commands(e, storeCommand) != 1 || commands(e, expungeCommand) != 1 || commands(e, appendCommand) != 2 ||
		!regexp.MustCompile(`T\d+ UID EXPUNGE `+strconv.Itoa(int(gone.UID))+`\b`).MatchString(e.f.wire.String()) {
		t.Errorf("want one STORE and one UID EXPUNGE of the one UID:\n%s", e.f.wire.String())
	}
}

func TestDraftsUpdateAndDeleteNeedUIDPlusBeforeAnyWrite(t *testing.T) {
	e := draftEnvironmentCaps(t, imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapMove: {}, imap.CapSpecialUse: {}})
	uid := e.f.addRaw(t, "Drafts", header(mailbox, "d")+"\r\nbody\r\n", imap.FlagDraft)
	validity := validityOf(t, e, "Drafts")
	for tool, extra := range map[string]string{"update": `,"to":["a@example.net"]`, "delete": ``} {
		_, err := e.confirmed("infomaniakmail.drafts."+tool, "draft", refArgs("Drafts", uid, validity, extra))
		if classOf(err) != provider.ClassProviderError || !strings.Contains(err.Error(), "nothing was changed") {
			t.Errorf("%s: err = %v, want a provider error that says nothing was changed", tool, err)
		}
	}
	if commands(e, appendCommand)+commands(e, storeCommand)+commands(e, expungeCommand) != 0 || len(uids(list(t, e, "open", `{"folder":"Drafts"}`))) != 1 {
		t.Errorf("something was written:\n%s", e.f.wire.String())
	}
	// create needs no UIDPLUS; the new UID is simply not reported.
	if got := createDraft(t, e, "draft", `{"to":["a@example.net"]}`); got.Folder != "Drafts" {
		t.Errorf("result = %+v", got)
	}
}

func TestDraftChangesWithAnUnclearOutcomeAreReportedAndNeverRepeated(t *testing.T) {
	for _, tc := range []struct {
		tool, extra, command string
		wants                []string
		appends              int32
	}{
		{"create", `{"to":["a@example.net"]}`, `APPEND`, []string{"may have been applied"}, 1},
		{"update", `,"to":["a@example.net"]`, `APPEND`, []string{"new draft may have been created", "old draft still exists"}, 1},
		{"update", `,"to":["a@example.net"]`, `UID STORE`, []string{"new draft was created as uid", "old draft may still exist", "drafts.delete"}, 1},
		{"update", `,"to":["a@example.net"]`, `UID EXPUNGE`, []string{"new draft was created as uid", "marked as deleted"}, 1},
		{"delete", ``, `UID STORE`, []string{"may have been applied"}, 0},
		{"delete", ``, `UID EXPUNGE`, []string{"marked as deleted"}, 0},
	} {
		t.Run(tc.tool+" "+tc.command, func(t *testing.T) {
			e := draftEnvironment(t, t.TempDir())
			uid := e.f.addRaw(t, "Drafts", header(mailbox, "d")+"\r\nbody\r\n", imap.FlagDraft)
			validity := validityOf(t, e, "Drafts")
			arguments := tc.extra
			if tc.tool != "create" {
				arguments = refArgs("Drafts", uid, validity, tc.extra)
			}
			var writes atomic.Int32
			real := dialIMAP
			dialIMAP = func(ctx context.Context) (net.Conn, error) {
				conn, err := real(ctx)
				if err != nil {
					return nil, err
				}
				return dropAfter{Conn: conn, match: regexp.MustCompile(`T\d+ ` + tc.command + ` `), writes: &writes}, nil
			}
			t.Cleanup(func() { dialIMAP = real })
			dials := e.f.dials.Load()

			_, err := e.confirmed("infomaniakmail.drafts."+tc.tool, "draft", arguments)
			if err == nil {
				t.Fatal("no error")
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err = %v, want it to say %q", err, want)
				}
			}
			if writes.Load() != 1 || e.f.dials.Load() != dials+1 {
				t.Errorf("%s written %d times over %d connections, want once over one", tc.command, writes.Load(), e.f.dials.Load()-dials)
			}
			if class := classOf(err); class != provider.ClassUnreachable && class != provider.ClassTimeout {
				t.Errorf("class = %q", class)
			}
		})
	}
}

// A tagged refusal of the removal after the new draft exists still names the new draft.
func TestDraftsUpdateRemovalRefusedAfterTheNewDraftExists(t *testing.T) {
	e := draftEnvironment(t, t.TempDir())
	uid := e.f.addRaw(t, "Drafts", header(mailbox, "d")+"\r\nbody\r\n", imap.FlagDraft)
	validity := validityOf(t, e, "Drafts")
	real := dialIMAP
	dialIMAP = func(ctx context.Context) (net.Conn, error) {
		conn, err := real(ctx)
		if err != nil {
			return nil, err
		}
		return &rewriteStore{Conn: conn}, nil
	}
	t.Cleanup(func() { dialIMAP = real })
	_, err := e.confirmed("infomaniakmail.drafts.update", "draft", refArgs("Drafts", uid, validity, `,"to":["a@example.net"]`))
	if err == nil || !strings.Contains(err.Error(), "new draft was created as uid") || !strings.Contains(err.Error(), "old draft may still exist") {
		t.Fatalf("err = %v", err)
	}
	if got := len(uids(list(t, e, "open", `{"folder":"Drafts"}`))); got != 2 {
		t.Errorf("drafts = %d, want the old and the new one", got)
	}
}

// rewriteStore turns the STORE of the removal into a request the server answers with BAD, so the old draft
// is neither marked nor removed.
type rewriteStore struct{ net.Conn }

func (r *rewriteStore) Write(p []byte) (int, error) {
	if i := bytes.Index(p, []byte(" UID STORE ")); i >= 0 {
		p = bytes.Replace(p, []byte(" UID STORE "), []byte(" UID BOGUS "), 1)
	}
	return r.Conn.Write(p)
}

func TestDraftLimitsAndLocalFileRelease(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	big := bytes.Repeat([]byte{'a'}, 9<<20)
	for _, name := range []string{"big1.bin", "big2.bin"} {
		if err := os.WriteFile(filepath.Join(dir, name), big, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	e := draftEnvironment(t, dir)
	dials, reads := e.f.dials.Load(), e.f.reads.Load()
	var many []string
	for i := 0; i < 11; i++ {
		many = append(many, `{"name":"a.txt","content_base64":"aGk="}`)
	}
	var recipients []string
	for i := 0; i < 51; i++ {
		recipients = append(recipients, fmt.Sprintf(`"u%d@example.net"`, i))
	}
	tooLarge := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'a'}, 4<<20+1))
	for name, args := range map[string]string{
		"recipients":       `{"to":[` + strings.Join(recipients, ",") + `]}`,
		"split recipients": `{"to":[` + strings.Join(recipients[:20], ",") + `],"cc":[` + strings.Join(recipients[20:40], ",") + `],"bcc":[` + strings.Join(recipients[40:], ",") + `]}`,
		"attachments":      `{"to":["a@example.net"],"attachments":[` + strings.Join(many, ",") + `]}`,
		"inline size":      `{"to":["a@example.net"],"attachments":[{"name":"a.bin","content_base64":"` + tooLarge + `"}]}`,
		"bad base64":       `{"to":["a@example.net"],"attachments":[{"name":"a.bin","content_base64":"!!!"}]}`,
		"no name":          `{"to":["a@example.net"],"attachments":[{"content_base64":"aGk="}]}`,
		"both sources":     `{"to":["a@example.net"],"attachments":[{"name":"a","content_base64":"aGk=","local_path":"` + filepath.Join(dir, "big1.bin") + `"}]}`,
		"no source":        `{"to":["a@example.net"],"attachments":[{"name":"a"}]}`,
		"outside release":  `{"to":["a@example.net"],"attachments":[{"local_path":"` + filepath.Join(outside, "secret.txt") + `"}]}`,
		"missing file":     `{"to":["a@example.net"],"attachments":[{"local_path":"` + filepath.Join(dir, "none.bin") + `"}]}`,
		"directory":        `{"to":["a@example.net"],"attachments":[{"local_path":"` + dir + `"}]}`,
		"total size": `{"to":["a@example.net"],"attachments":[{"local_path":"` + filepath.Join(dir, "big1.bin") +
			`"},{"local_path":"` + filepath.Join(dir, "big2.bin") + `"}]}`,
		"no recipient": `{"to":[]}`,
		"body NUL":     `{"to":["a@example.net"],"body":"a\u0000b"}`,
		"unknown":      `{"to":["a@example.net"],"headers":{"X":"y"}}`,
	} {
		_, err := e.confirmed("infomaniakmail.drafts.create", "draft", args)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if strings.Contains(err.Error(), outside) || strings.Contains(err.Error(), dir) || strings.Contains(err.Error(), "secret.txt") {
			t.Errorf("%s: error names a path: %v", name, err)
		}
	}
	if e.f.dials.Load() != dials || e.f.reads.Load() != reads || commands(e, appendCommand) != 0 {
		t.Errorf("a refusal reached the credential or the server: dials %d -> %d, reads %d -> %d",
			dials, e.f.dials.Load(), reads, e.f.reads.Load())
	}
	// Two files that fit separately fit together up to the limit: one of 9 MiB is accepted.
	got := createDraft(t, e, "draft", `{"to":["a@example.net"],"attachments":[{"local_path":"`+filepath.Join(dir, "big1.bin")+`"}]}`)
	if len(got.Attachments) != 1 || got.Attachments[0].Size != int64(len(big)) || got.Attachments[0].Type != "application/octet-stream" ||
		got.Attachments[0].Name != "big1.bin" || got.Size < int64(len(big)) {
		t.Errorf("result = %+v", got)
	}
}

func TestDraftsRespectTheSenderTargets(t *testing.T) {
	e := draftEnvironment(t, t.TempDir())
	// The mailbox is not on the sender list, so its drafts could not be read back: refused up front.
	if _, err := e.confirmed("infomaniakmail.drafts.create", "draftsenders", `{"to":["a@example.net"]}`); !isInvalidRequest(err) {
		t.Errorf("err = %v, want an invalid request", err)
	}
	// A draft whose From is not on the sender list answers not-found for update and delete.
	uid := e.f.addRaw(t, "Drafts", header("mallory@example.org", "d")+"\r\nbody\r\n", imap.FlagDraft)
	validity := validityOf(t, e, "Drafts")
	for tool, extra := range map[string]string{"update": `,"to":["a@example.net"]`, "delete": ``} {
		if _, err := e.confirmed("infomaniakmail.drafts."+tool, "draftsenders", refArgs("Drafts", uid, validity, extra)); err == nil {
			t.Errorf("%s reached a draft of a foreign sender", tool)
		}
	}
	if commands(e, appendCommand)+commands(e, expungeCommand) != 0 || len(uids(list(t, e, "open", `{"folder":"Drafts"}`))) != 1 {
		t.Errorf("something was written:\n%s", e.f.wire.String())
	}
}

func TestDraftErrorsCarryNoServerTextOrPassword(t *testing.T) {
	e := draftEnvironment(t, t.TempDir())
	e.f.password.Store("wrong")
	_, err := e.confirmed("infomaniakmail.drafts.create", "draft", `{"to":["a@example.net"]}`)
	if classOf(err) != provider.ClassAuth {
		t.Fatalf("err = %v, want auth", err)
	}
	if text := e.red.Error(err); strings.Contains(text, passwordVal) || strings.Contains(strings.ToLower(text), "imap") {
		t.Errorf("error %q", text)
	}
}
