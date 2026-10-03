package infomaniakmail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// addRaw appends a complete message source and returns its UID.
func (f *fixture) addRaw(t *testing.T, folder, raw string, flags ...imap.Flag) uint32 {
	t.Helper()
	data, err := f.user.Append(folder, literal{bytes.NewReader([]byte(raw))}, &imap.AppendOptions{Time: base, Flags: flags})
	if err != nil {
		t.Fatalf("append to %s: %v", folder, err)
	}
	return uint32(data.UID)
}

func header(from, subject string) string {
	return fmt.Sprintf("From: %s\r\nTo: Box <%s>\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\n",
		from, mailbox, subject, base.Format(time.RFC1123Z))
}

func wrapBase64(data []byte) string {
	encoded := base64.StdEncoding.EncodeToString(data)
	var out strings.Builder
	for len(encoded) > 76 {
		out.WriteString(encoded[:76] + "\r\n")
		encoded = encoded[76:]
	}
	return out.String() + encoded + "\r\n"
}

// mixed builds a multipart/mixed message: a text/plain (quoted-printable, ISO-8859-1) next to its HTML
// alternative, then the given attachment parts.
func mixed(from, subject string, attachments ...string) string {
	raw := header(from, subject) + "Content-Type: multipart/mixed; boundary=outer\r\n\r\n" +
		"--outer\r\nContent-Type: multipart/alternative; boundary=inner\r\n\r\n" +
		"--inner\r\nContent-Type: text/plain; charset=iso-8859-1\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n" +
		"Gr=FC=DFe,=\r\n tail\r\nsecond line\r\n" +
		"--inner\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>HTML-CANARY</p>\r\n--inner--\r\n"
	for _, part := range attachments {
		raw += "--outer\r\n" + part
	}
	return raw + "--outer--\r\n"
}

func pdfPart(name string, content []byte) string {
	return "Content-Type: application/pdf; name=\"" + name + "\"\r\nContent-Transfer-Encoding: base64\r\n" +
		"Content-Disposition: attachment; filename=\"" + name + "\"\r\n\r\n" + wrapBase64(content)
}

func validityOf(t *testing.T, e *environment, folder string) uint32 {
	t.Helper()
	return list(t, e, "open", `{"folder":"`+folder+`"}`).UIDValidity
}

func getArgs(folder string, uid, validity uint32) string {
	return fmt.Sprintf(`{"folder":%q,"uid":%d,"uidvalidity":%d}`, folder, uid, validity)
}

func attachmentArgs(folder string, uid, validity uint32, part string, extra string) string {
	return fmt.Sprintf(`{"folder":%q,"uid":%d,"uidvalidity":%d,"part":%q%s}`, folder, uid, validity, part, extra)
}

func getMessage(t *testing.T, e *environment, connection, arguments string) MessageContent {
	t.Helper()
	result, err := e.invoke("infomaniakmail.messages.get", connection, arguments)
	if err != nil {
		t.Fatalf("messages.get %s: %v", arguments, err)
	}
	var out MessageContent
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("decode %q: %v", result, err)
	}
	return out
}

func TestMessagesGetReturnsDecodedTextAndAttachmentMetadata(t *testing.T) {
	e := newEnvironment(t)
	e.f.folder(t, "INBOX")
	content := []byte("%PDF-fake-content")
	uid := e.f.addRaw(t, "INBOX", mixed("Alice <alice@example.net>", "Report",
		pdfPart("report.pdf", content),
		"Content-Type: image/png\r\nContent-Transfer-Encoding: base64\r\nContent-ID: <logo>\r\n\r\n"+wrapBase64([]byte("png")),
	))
	validity := validityOf(t, e, "INBOX")

	got := getMessage(t, e, "open", getArgs("INBOX", uid, validity))
	if got.Folder != "INBOX" || got.UIDValidity != validity || got.UID != uid || got.Subject != "Report" ||
		len(got.From) != 1 || got.From[0].Address != "alice@example.net" {
		t.Fatalf("message = %+v", got)
	}
	if got.BodyType != "text/plain" || got.BodyTruncated || got.Body != "Grüße, tail\nsecond line" {
		t.Errorf("body = %q (%s, truncated %t)", got.Body, got.BodyType, got.BodyTruncated)
	}
	if strings.Contains(got.Body, "HTML-CANARY") {
		t.Errorf("the HTML alternative leaked into the text")
	}
	if got.AttachmentCount != 2 || len(got.Attachments) != 2 || got.AttachmentsTruncated {
		t.Fatalf("attachments = %+v", got.Attachments)
	}
	first, second := got.Attachments[0], got.Attachments[1]
	if first.Part != "2" || first.Name != "report.pdf" || first.Type != "application/pdf" || first.Size <= 0 ||
		second.Part != "3" || second.Name != "" || second.Type != "image/png" {
		t.Errorf("attachments = %+v", got.Attachments)
	}
}

func TestMessagesGetFallsBackToHTMLSourceAndSinglePartMessages(t *testing.T) {
	e := newEnvironment(t)
	e.f.folder(t, "INBOX")
	html := e.f.addRaw(t, "INBOX", header("alice@example.net", "html")+
		"Content-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n"+
		wrapBase64([]byte("<b>Hi é</b><script>x</script>")))
	plain := e.f.addRaw(t, "INBOX", header("alice@example.net", "plain")+"Content-Type: text/plain\r\n\r\nplain text\r\n")
	validity := validityOf(t, e, "INBOX")

	got := getMessage(t, e, "open", getArgs("INBOX", html, validity))
	if got.BodyType != "text/html" || got.Body != "<b>Hi é</b><script>x</script>" || len(got.Attachments) != 0 {
		t.Errorf("html message = %+v", got)
	}
	got = getMessage(t, e, "open", getArgs("INBOX", plain, validity))
	if got.BodyType != "text/plain" || strings.TrimSpace(got.Body) != "plain text" {
		t.Errorf("plain message = %+v", got)
	}
}

func TestMessagesGetBoundsTheText(t *testing.T) {
	e := newEnvironment(t)
	e.f.folder(t, "INBOX")
	long := e.f.addRaw(t, "INBOX", header("alice@example.net", "long")+
		"Content-Type: text/plain; charset=utf-8\r\n\r\n"+strings.Repeat("é", maxBodyChars+500)+"\r\n")
	huge := e.f.addRaw(t, "INBOX", header("alice@example.net", "huge")+
		"Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n"+
		wrapBase64(bytes.Repeat([]byte("a"), 3*maxBodyFetch)))
	validity := validityOf(t, e, "INBOX")

	got := getMessage(t, e, "open", getArgs("INBOX", long, validity))
	if !got.BodyTruncated || len([]rune(got.Body)) != maxBodyChars {
		t.Errorf("long: truncated %t, %d characters", got.BodyTruncated, len([]rune(got.Body)))
	}
	got = getMessage(t, e, "open", getArgs("INBOX", huge, validity))
	if !got.BodyTruncated || len([]rune(got.Body)) != maxBodyChars || strings.Trim(got.Body, "a") != "" {
		t.Errorf("huge: truncated %t, %d characters", got.BodyTruncated, len([]rune(got.Body)))
	}
	if e.f.wire.String() == "" || !strings.Contains(e.f.wire.String(), "BODY.PEEK[1]<0."+strconv.Itoa(maxBodyFetch)+">") {
		t.Errorf("the text fetch is not a bounded BODY.PEEK range:\n%s", e.f.wire.String())
	}
}

func TestMessagesGetCleansAndCapsAttachmentMetadata(t *testing.T) {
	e := newEnvironment(t)
	e.f.folder(t, "INBOX")
	var parts []string
	for i := 0; i < maxAttachments+5; i++ {
		parts = append(parts, pdfPart(fmt.Sprintf("f%d.pdf", i), []byte("x")))
	}
	parts[0] = pdfPart("=?utf-8?q?caf=C3=A9?=.pdf", []byte("x"))
	uid := e.f.addRaw(t, "INBOX", mixed("alice@example.net", "many", parts...))
	got := getMessage(t, e, "open", getArgs("INBOX", uid, validityOf(t, e, "INBOX")))
	if got.AttachmentCount != maxAttachments+5 || len(got.Attachments) != maxAttachments || !got.AttachmentsTruncated ||
		got.Attachments[0].Name != "café.pdf" {
		t.Errorf("count %d, listed %d, truncated %t, first %+v", got.AttachmentCount, len(got.Attachments),
			got.AttachmentsTruncated, got.Attachments[0])
	}
	if text, cut := cleanBody("a\x00b\r\nc d\te", 100); text != "a b\nc d\te" || cut {
		t.Errorf("cleanBody = %q, %t", text, cut)
	}
}

// Reading uses EXAMINE and BODY.PEEK only; \Seen stays as it was and no command changes the mailbox.
func TestReadingKeepsFlagsUnchangedAndUsesPeek(t *testing.T) {
	e := filesEnvironment(t, t.TempDir())
	e.f.folder(t, "INBOX")
	unread := e.f.addRaw(t, "INBOX", mixed("alice@example.net", "unread", pdfPart("a.pdf", []byte("data"))))
	seen := e.f.addRaw(t, "INBOX", mixed("alice@example.net", "seen", pdfPart("a.pdf", []byte("data"))), imap.FlagSeen)
	validity := validityOf(t, e, "INBOX")

	for _, uid := range []uint32{unread, seen} {
		getMessage(t, e, "open", getArgs("INBOX", uid, validity))
		if _, err := e.invoke("infomaniakmail.attachments.get", "open", attachmentArgs("INBOX", uid, validity, "2", "")); err != nil {
			t.Fatal(err)
		}
	}
	// Every command the client sent is on a line that starts with its tag: server responses never do.
	command := regexp.MustCompile(`(?m)^\s*T\d+ (.*)$`)
	peeks := 0
	for _, match := range command.FindAllStringSubmatch(e.f.wire.String(), -1) {
		line := match[1]
		switch {
		case strings.Contains(line, "BODY.PEEK["):
			peeks++
		case strings.Contains(line, "BODY["), regexp.MustCompile(`\bRFC822(\.TEXT|\.HEADER)?[ )]`).MatchString(line):
			t.Errorf("client command reads content without PEEK: %s", line)
		}
		if regexp.MustCompile(`^(UID )?(STORE|APPEND|EXPUNGE|COPY|MOVE|CREATE|DELETE|RENAME|CLOSE|SELECT)\b`).MatchString(line) {
			t.Errorf("client command changes or selects: %s", line)
		}
	}
	if peeks < 4 {
		t.Errorf("found %d BODY.PEEK commands, want one per text and attachment read", peeks)
	}
	if !strings.Contains(e.f.wire.String(), " EXAMINE ") || !strings.Contains(e.f.wire.String(), "BODYSTRUCTURE") {
		t.Errorf("expected EXAMINE and BODYSTRUCTURE on the wire")
	}
	// The flags in the server are untouched: the unread message is still unread, the seen one still seen.
	page := list(t, e, "open", `{"folder":"INBOX","unread":true}`)
	if got := uids(page); len(got) != 1 || got[0] != unread {
		t.Errorf("unread after reading = %v, want only %d", got, unread)
	}
	after := list(t, e, "open", `{"folder":"INBOX"}`)
	for _, m := range after.Messages {
		isSeen := len(m.Flags) == 1 && strings.EqualFold(m.Flags[0], `\Seen`)
		if (m.UID == seen) != isSeen {
			t.Errorf("message %d flags = %v changed", m.UID, m.Flags)
		}
	}
}

// attachments.get declares local file access, like every tool that can write a local file, so a connection
// that releases no directory does not offer it; messages.get needs none.
func TestAttachmentsGetNeedsAReleasedDirectory(t *testing.T) {
	e := newEnvironment(t)
	e.f.folder(t, "INBOX")
	uid := e.f.addRaw(t, "INBOX", mixed("alice@example.net", "x", pdfPart("a.pdf", []byte("data"))))
	validity := validityOf(t, e, "INBOX")
	_, err := e.invoke("infomaniakmail.attachments.get", "open", attachmentArgs("INBOX", uid, validity, "2", ""))
	if application.ErrorCode(err) != "unsupported-capability" {
		t.Errorf("err = %v, want unsupported-capability", err)
	}
	getMessage(t, e, "open", getArgs("INBOX", uid, validity))
}

func TestReadRefusesForeignScopeBeforeSecretAndNetwork(t *testing.T) {
	e := filesEnvironment(t, t.TempDir())
	e.f.folder(t, "INBOX")
	e.f.folder(t, "Allowed")
	e.f.folder(t, "Secret")
	for _, tool := range []string{"messages.get", "attachments.get"} {
		arguments := getArgs("Secret", 1, 1)
		if tool == "attachments.get" {
			arguments = attachmentArgs("Secret", 1, 1, "2", "")
		}
		_, err := e.invoke("infomaniakmail."+tool, "allowed", arguments)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "Allowed") {
			t.Errorf("%s err = %v, want an invalid request that names no allowed folder", tool, err)
		}
		for _, bad := range []string{`{"folder":"INBOX","uid":0,"uidvalidity":1}`, `{"folder":"INBOX","uid":1}`,
			`{"folder":"IN*","uid":1,"uidvalidity":1}`, `{"folder":"INBOX","uid":1,"uidvalidity":1,"section":"HEADER"}`} {
			if _, err := e.invoke("infomaniakmail."+tool, "open", bad); err == nil {
				t.Errorf("%s accepted %s", tool, bad)
			}
		}
	}
	if e.f.dials.Load() != 0 || e.f.reads.Load() != 0 {
		t.Errorf("dials = %d, secret reads = %d, want none", e.f.dials.Load(), e.f.reads.Load())
	}
}

func TestReadChecksUIDValiditySenderAndExistence(t *testing.T) {
	e := filesEnvironment(t, t.TempDir())
	e.f.folder(t, "INBOX")
	allowed := e.f.addRaw(t, "INBOX", mixed("Alice <alice@example.net>", "ok", pdfPart("a.pdf", []byte("data"))))
	foreign := e.f.addRaw(t, "INBOX", mixed("mallory@example.org", "no", pdfPart("a.pdf", []byte("data"))))
	lookalike := e.f.addRaw(t, "INBOX", mixed("\"alice@example.net\" <mallory@example.org>", "no", pdfPart("a.pdf", []byte("data"))))
	validity := validityOf(t, e, "INBOX")

	if _, err := e.invoke("infomaniakmail.messages.get", "open", getArgs("INBOX", allowed, validity+1)); !isInvalidRequest(err) {
		t.Errorf("wrong uidvalidity: err = %v, want invalid request", err)
	}
	if _, err := e.invoke("infomaniakmail.attachments.get", "open", attachmentArgs("INBOX", allowed, validity+1, "2", "")); !isInvalidRequest(err) {
		t.Errorf("wrong uidvalidity: err = %v, want invalid request", err)
	}
	for _, uid := range []uint32{foreign, lookalike, 9999} {
		_, err := e.invoke("infomaniakmail.messages.get", "senders", getArgs("INBOX", uid, validity))
		if classOf(err) != provider.ClassNotFound {
			t.Errorf("messages.get %d: err = %v, want not-found", uid, err)
		}
		if err != nil && (strings.Contains(err.Error(), "example.org") || strings.Contains(err.Error(), "alice")) {
			t.Errorf("error %q leaks details", err)
		}
		_, err = e.invoke("infomaniakmail.attachments.get", "senders", attachmentArgs("INBOX", uid, validity, "2", ""))
		if classOf(err) != provider.ClassNotFound {
			t.Errorf("attachments.get %d: err = %v, want not-found", uid, err)
		}
	}
	got := getMessage(t, e, "senders", getArgs("INBOX", allowed, validity))
	if got.UID != allowed || got.Subject != "ok" {
		t.Errorf("allowed message = %+v", got)
	}
}

func TestAttachmentsGetInline(t *testing.T) {
	e := filesEnvironment(t, t.TempDir())
	e.f.folder(t, "INBOX")
	content := bytes.Repeat([]byte{0, 1, 2, 254, 255, 'x'}, 5000)
	uid := e.f.addRaw(t, "INBOX", mixed("alice@example.net", "inline",
		pdfPart("report.pdf", content),
		"Content-Type: text/csv; charset=utf-8\r\nContent-Disposition: attachment; filename=\"t.csv\"\r\n"+
			"Content-Transfer-Encoding: quoted-printable\r\n\r\na=3Db,=\r\nc\r\n"))
	validity := validityOf(t, e, "INBOX")

	result, err := e.invoke("infomaniakmail.attachments.get", "open", attachmentArgs("INBOX", uid, validity, "2", ""))
	if err != nil {
		t.Fatal(err)
	}
	var out AttachmentContent
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	decoded, _ := base64.StdEncoding.DecodeString(out.ContentBase64)
	if !bytes.Equal(decoded, content) || out.Size != int64(len(content)) || out.SHA256 != hex.EncodeToString(sum[:]) ||
		out.Name != "report.pdf" || out.Type != "application/pdf" || out.Part != "2" || out.UID != uid ||
		out.Folder != "INBOX" || out.UIDValidity != validity {
		t.Errorf("result = %+v", out)
	}
	result, err = e.invoke("infomaniakmail.attachments.get", "open", attachmentArgs("INBOX", uid, validity, "3", ""))
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(result), &out)
	if decoded, _ := base64.StdEncoding.DecodeString(out.ContentBase64); string(decoded) != "a=b,c" {
		t.Errorf("quoted-printable attachment = %q", decoded)
	}

	// Only a part the message's own structure lists as an attachment can be read: not the text, not a part
	// that does not exist, not a malformed number.
	for _, part := range []string{"1", "1.1", "1.2", "4", "9"} {
		_, err := e.invoke("infomaniakmail.attachments.get", "open", attachmentArgs("INBOX", uid, validity, part, ""))
		if classOf(err) != provider.ClassNotFound {
			t.Errorf("part %s: err = %v, want not-found", part, err)
		}
	}
	for _, part := range []string{"", "0", "1.", "a", "1..2", "../1", "1.2.3.4.5.6.7.8.9", "HEADER", "2 ", "2.TEXT"} {
		if _, err := e.invoke("infomaniakmail.attachments.get", "open", attachmentArgs("INBOX", uid, validity, part, "")); err == nil {
			t.Errorf("part %q was accepted", part)
		}
	}
}

func TestAttachmentsGetInlineLimit(t *testing.T) {
	e := filesEnvironment(t, t.TempDir())
	e.f.folder(t, "INBOX")
	rng := rand.New(rand.NewSource(1))
	over := make([]byte, maxInlineBytes+1)
	rng.Read(over)
	huge := make([]byte, maxInlineEncoded)
	rng.Read(huge)
	exact := make([]byte, maxInlineBytes)
	rng.Read(exact)
	overUID := e.f.addRaw(t, "INBOX", mixed("alice@example.net", "over", pdfPart("over.bin", over)))
	hugeUID := e.f.addRaw(t, "INBOX", mixed("alice@example.net", "huge", pdfPart("huge.bin", huge)))
	exactUID := e.f.addRaw(t, "INBOX", mixed("alice@example.net", "exact", pdfPart("exact.bin", exact)))
	validity := validityOf(t, e, "INBOX")

	for _, uid := range []uint32{overUID, hugeUID} {
		_, err := e.invoke("infomaniakmail.attachments.get", "open", attachmentArgs("INBOX", uid, validity, "2", ""))
		if !isInvalidRequest(err) || !strings.Contains(err.Error(), "local_path") {
			t.Errorf("uid %d: err = %v, want an invalid request that points to local_path", uid, err)
		}
	}
	result, err := e.invoke("infomaniakmail.attachments.get", "open", attachmentArgs("INBOX", exactUID, validity, "2", ""))
	if err != nil {
		t.Fatalf("a 4 MiB attachment must pass: %v", err)
	}
	var out AttachmentContent
	_ = json.Unmarshal([]byte(result), &out)
	if out.Size != maxInlineBytes {
		t.Errorf("size = %d", out.Size)
	}
}

// filesEnvironment releases dir for writing on every connection: attachments.get declares local file access
// and is offered only to a connection that releases a directory, also for an inline read.
func filesEnvironment(t *testing.T, dir string) *environment {
	t.Helper()
	return newEnvironmentWith(t, func(cfg *config.Config) {
		for name, connection := range cfg.Connections {
			connection.Files = config.Files{Write: []string{dir}}
			cfg.Connections[name] = connection
		}
	})
}

func (e *environment) invokeConfirmed(operation, connection, arguments string) (string, error) {
	response, err := e.core.Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: connection, Arguments: []byte(arguments), Confirmed: true,
	})
	return string(response.Result), err
}

func localArg(path string) string { return `,"local_path":` + strconv.Quote(path) }

func TestAttachmentsGetToLocalPath(t *testing.T) {
	dir := t.TempDir()
	e := filesEnvironment(t, dir)
	e.f.folder(t, "INBOX")
	rng := rand.New(rand.NewSource(2))
	content := make([]byte, maxInlineBytes+1234) // larger than the inline limit
	rng.Read(content)
	uid := e.f.addRaw(t, "INBOX", mixed("alice@example.net", "file", pdfPart("big.pdf", content)))
	validity := validityOf(t, e, "INBOX")
	path := filepath.Join(dir, "big.pdf")

	result, err := e.invoke("infomaniakmail.attachments.get", "open", attachmentArgs("INBOX", uid, validity, "2", localArg(path)))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	var out AttachmentContent
	if err := json.Unmarshal([]byte(result), &out); err != nil || out.ContentBase64 != "" || strings.Contains(result, "content_base64") ||
		out.Size != int64(len(content)) || out.SHA256 != hex.EncodeToString(sum[:]) || out.Name != "big.pdf" || out.Part != "2" {
		t.Fatalf("result = %.300s, %v", result, err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, content) {
		t.Fatalf("file differs from the attachment (%d bytes)", len(got))
	}

	// An existing file is kept without confirmation and replaced with it.
	if _, err := e.invoke("infomaniakmail.attachments.get", "open", attachmentArgs("INBOX", uid, validity, "2", localArg(path))); application.ErrorCode(err) != "confirmation-required" {
		t.Fatalf("err = %v, want the overwrite confirmation", err)
	}
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.invoke("infomaniakmail.attachments.get", "open", attachmentArgs("INBOX", uid, validity, "2", localArg(path))); application.ErrorCode(err) != "confirmation-required" {
		t.Fatalf("err = %v, want the overwrite confirmation", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "old" {
		t.Fatalf("file = %d bytes, want it unchanged", len(got))
	}
	if _, err := e.invokeConfirmed("infomaniakmail.attachments.get", "open", attachmentArgs("INBOX", uid, validity, "2", localArg(path))); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, content) {
		t.Fatalf("file not replaced (%d bytes)", len(got))
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("directory holds %d entries, want only the target", len(entries))
	}
}

func TestAttachmentsGetToLocalPathRefusalsLeaveNothing(t *testing.T) {
	released, other := t.TempDir(), t.TempDir()
	e := filesEnvironment(t, released)
	e.f.folder(t, "INBOX")
	uid := e.f.addRaw(t, "INBOX", mixed("alice@example.net", "file", pdfPart("a.pdf", []byte("data"))))
	validity := validityOf(t, e, "INBOX")
	dials, reads := e.f.dials.Load(), e.f.reads.Load()

	// A path outside the release is refused before secret and network, without naming either directory.
	_, err := e.invoke("infomaniakmail.attachments.get", "open", attachmentArgs("INBOX", uid, validity, "2", localArg(filepath.Join(other, "x"))))
	if err == nil || strings.Contains(err.Error(), other) || strings.Contains(err.Error(), released) {
		t.Errorf("err = %v", err)
	}
	if e.f.dials.Load() != dials || e.f.reads.Load() != reads {
		t.Errorf("a refused path caused network or secret access")
	}
	// A missing part, an unreadable message, or a wrong UIDVALIDITY leaves no temporary file behind either.
	for _, arguments := range []string{
		attachmentArgs("INBOX", uid, validity, "1", localArg(filepath.Join(released, "x"))),
		attachmentArgs("INBOX", 9999, validity, "2", localArg(filepath.Join(released, "x"))),
		attachmentArgs("INBOX", uid, validity+1, "2", localArg(filepath.Join(released, "x"))),
	} {
		if _, err := e.invoke("infomaniakmail.attachments.get", "open", arguments); err == nil {
			t.Errorf("%s was accepted", arguments)
		}
	}
	if entries, _ := os.ReadDir(released); len(entries) != 0 {
		t.Errorf("released directory holds %d entries, want none", len(entries))
	}
}

func TestMailContentIsRedactedAndErrorsCarryNoServerText(t *testing.T) {
	e := filesEnvironment(t, t.TempDir())
	e.f.folder(t, "INBOX")
	e.f.password.Store("wrong-password-value")
	for _, tool := range []string{"messages.get", "attachments.get"} {
		arguments := getArgs("INBOX", 1, 1)
		if tool == "attachments.get" {
			arguments = attachmentArgs("INBOX", 1, 1, "2", "")
		}
		_, err := e.invoke("infomaniakmail."+tool, "open", arguments)
		if classOf(err) != provider.ClassAuth {
			t.Fatalf("%s err = %v, want auth", tool, err)
		}
		if text := e.red.Error(err); strings.Contains(text, "wrong-password-value") || strings.Contains(text, "imap") {
			t.Errorf("error %q carries secret or server text", text)
		}
	}
}

func TestDecodeCharset(t *testing.T) {
	for charset, want := range map[string]string{
		"iso-8859-1": "é€\u0080", "windows-1252": "é€€", "iso-8859-15": "é€\u0080", "utf-8": "é€€", "x-unknown": "é€€",
	} {
		var in []byte
		switch charset {
		case "iso-8859-1":
			in = []byte{0xE9, 0xA4 - 0x80 + 0x80, 0x80}
			in = []byte{0xE9}
			want = "é"
		case "windows-1252":
			in = []byte{0xE9, 0x80}
			want = "é€"
		case "iso-8859-15":
			in = []byte{0xE9, 0xA4}
			want = "é€"
		default:
			in = []byte("é€")
			want = "é€"
			if charset == "x-unknown" {
				in = []byte{'a', 0xE9, 'b'}
				want = "a\xe9b"
			}
		}
		got := decodeCharset(in, charset)
		if charset == "x-unknown" {
			if cleaned, _ := cleanBody(got, 10); cleaned != "a�b" {
				t.Errorf("%s = %q", charset, cleaned)
			}
			continue
		}
		if got != want {
			t.Errorf("%s = %q, want %q", charset, got, want)
		}
	}
	if got := trimIncompleteRune([]byte("a\xc3")); string(got) != "a" {
		t.Errorf("trimIncompleteRune = %q", got)
	}
}

func TestAttachmentsGetCorruptContentLeavesNoFile(t *testing.T) {
	dir := t.TempDir()
	e := filesEnvironment(t, dir)
	e.f.folder(t, "INBOX")
	uid := e.f.addRaw(t, "INBOX", mixed("alice@example.net", "bad",
		"Content-Type: application/pdf; name=\"a.pdf\"\r\nContent-Transfer-Encoding: base64\r\n"+
			"Content-Disposition: attachment; filename=\"a.pdf\"\r\n\r\n!!!!not base64!!!!\r\n"))
	validity := validityOf(t, e, "INBOX")
	_, err := e.invoke("infomaniakmail.attachments.get", "open",
		attachmentArgs("INBOX", uid, validity, "2", localArg(filepath.Join(dir, "a.pdf"))))
	if classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("err = %v, want invalid-provider-response", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("directory holds %d entries, want none", len(entries))
	}
	if _, err := e.invoke("infomaniakmail.attachments.get", "open", attachmentArgs("INBOX", uid, validity, "2", "")); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("inline: err = %v, want invalid-provider-response", err)
	}
}
