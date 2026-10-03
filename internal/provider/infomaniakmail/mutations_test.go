package infomaniakmail

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/emersion/go-imap/v2"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

var (
	storeCommand   = regexp.MustCompile(`T\d+ (UID )?STORE\b`)
	moveCommand    = regexp.MustCompile(`T\d+ (UID )?(MOVE|COPY)\b`)
	expungeCommand = regexp.MustCompile(`T\d+ (UID )?EXPUNGE\b`)
)

func commands(e *environment, re *regexp.Regexp) int {
	return len(re.FindAllString(e.f.wire.String(), -1))
}

// markSpecialUse gives a folder of the in-memory server a SPECIAL-USE attribute, which its public API does
// not offer.
func markSpecialUse(t *testing.T, f *fixture, name string, attr imap.MailboxAttr) {
	t.Helper()
	boxes := reflect.ValueOf(f.user).Elem().FieldByName("mailboxes")
	box := boxes.MapIndex(reflect.ValueOf(name))
	if !box.IsValid() {
		t.Fatalf("no folder %s", name)
	}
	field := box.Elem().FieldByName("specialUse")
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(reflect.ValueOf([]imap.MailboxAttr{attr}))
}

// changeEnvironment has INBOX, Archive, and a Trash folder marked \Trash.
func changeEnvironment(t *testing.T) *environment {
	t.Helper()
	e := newEnvironment(t)
	for _, name := range []string{"INBOX", "Archive", "Trash"} {
		e.f.folder(t, name)
	}
	markSpecialUse(t, e.f, "Trash", imap.MailboxAttrTrash)
	return e
}

func refArgs(folder string, uid, validity uint32, extra string) string {
	return fmt.Sprintf(`{"folder":%q,"uid":%d,"uidvalidity":%d%s}`, folder, uid, validity, extra)
}

func flagsOf(t *testing.T, e *environment, folder string, uid uint32) []string {
	t.Helper()
	for _, m := range list(t, e, "open", `{"folder":"`+folder+`"}`).Messages {
		if m.UID == uid {
			return m.Flags
		}
	}
	return nil
}

func hasFlag(flags []string, want imap.Flag) bool {
	for _, f := range flags {
		if strings.EqualFold(f, string(want)) {
			return true
		}
	}
	return false
}

func TestChangesNeedConfirmationAndAToolsList(t *testing.T) {
	e := changeEnvironment(t)
	uid := e.f.add(t, "INBOX", "alice@example.net", "one", base)
	validity := validityOf(t, e, "INBOX")
	dials := e.f.dials.Load()
	for _, tool := range []struct{ name, extra string }{
		{"flag", `,"flag":"seen","set":true`}, {"move", `,"destination":"Archive"`}, {"delete", ``}, {"expunge", ``},
	} {
		operation := "infomaniakmail.messages." + tool.name
		args := refArgs("INBOX", uid, validity, tool.extra)
		var needed *application.ConfirmationRequiredError
		if _, err := e.invoke(operation, "change", args); !asError(err, &needed) {
			t.Errorf("%s without confirm: err = %v, want confirmation required", tool.name, err)
		}
		// The connection without a tools list offers no change tool, whatever its permissions.
		if _, err := e.confirmed(operation, "open", args); err == nil {
			t.Errorf("%s ran through a connection without a tools list", tool.name)
		}
	}
	if e.f.dials.Load() != dials || e.f.reads.Load() != 1 {
		t.Errorf("dials = %d (was %d), secret reads = %d, want only the listing", e.f.dials.Load(), dials, e.f.reads.Load())
	}
	if commands(e, storeCommand)+commands(e, moveCommand)+commands(e, expungeCommand) != 0 {
		t.Errorf("a refused change reached the server:\n%s", e.f.wire.String())
	}
}

func asError[T error](err error, target *T) bool {
	for err != nil {
		if typed, ok := err.(T); ok {
			*target = typed
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}

func TestMessagesFlagSetsAndClearsOneFlagWithOneStore(t *testing.T) {
	e := changeEnvironment(t)
	uid := e.f.add(t, "INBOX", "alice@example.net", "one", base)
	other := e.f.add(t, "INBOX", "alice@example.net", "two", base.Add(time.Hour))
	validity := validityOf(t, e, "INBOX")

	result, err := e.confirmed("infomaniakmail.messages.flag", "change", refArgs("INBOX", uid, validity, `,"flag":"seen","set":true`))
	if err != nil {
		t.Fatal(err)
	}
	var got FlagResult
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatal(err)
	}
	if got.Folder != "INBOX" || got.UID != uid || got.UIDValidity != validity || got.Flag != "seen" || !got.Set ||
		len(got.Flags) != 1 || !strings.EqualFold(got.Flags[0], `\Seen`) {
		t.Errorf("result = %+v", got)
	}
	if !hasFlag(flagsOf(t, e, "INBOX", uid), imap.FlagSeen) || len(flagsOf(t, e, "INBOX", other)) != 0 {
		t.Errorf("flags: target %v, other %v", flagsOf(t, e, "INBOX", uid), flagsOf(t, e, "INBOX", other))
	}
	if _, err := e.confirmed("infomaniakmail.messages.flag", "change", refArgs("INBOX", uid, validity, `,"flag":"flagged","set":true`)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.confirmed("infomaniakmail.messages.flag", "change", refArgs("INBOX", uid, validity, `,"flag":"seen","set":false`)); err != nil {
		t.Fatal(err)
	}
	if flags := flagsOf(t, e, "INBOX", uid); hasFlag(flags, imap.FlagSeen) || !hasFlag(flags, imap.FlagFlagged) {
		t.Errorf("flags = %v, want only Flagged", flags)
	}

	wire := e.f.wire.String()
	if n := commands(e, storeCommand); n != 3 {
		t.Errorf("STORE commands = %d, want exactly one per call:\n%s", n, wire)
	}
	if !regexp.MustCompile(`T\d+ SELECT`).MatchString(wire) {
		t.Errorf("a change must SELECT the folder:\n%s", wire)
	}
	if regexp.MustCompile(`BODY\[[^\]]*\]`).MatchString(strings.ReplaceAll(wire, "BODY.PEEK", "")) ||
		moveCommand.MatchString(wire) || expungeCommand.MatchString(wire) {
		t.Errorf("a flag change did more than STORE:\n%s", wire)
	}
}

func TestMessagesFlagRefusesUnknownFlagsBeforeNetwork(t *testing.T) {
	e := changeEnvironment(t)
	for _, extra := range []string{`,"flag":"deleted","set":true`, `,"flag":"\\Deleted","set":true`, `,"flag":"seen"`,
		`,"flag":"seen","set":"yes"`, `,"set":true`, `,"flag":"seen","set":true,"keyword":"x"`} {
		if _, err := e.confirmed("infomaniakmail.messages.flag", "change", refArgs("INBOX", 1, 1, extra)); err == nil {
			t.Errorf("accepted %s", extra)
		}
	}
	if e.f.dials.Load() != 0 || e.f.reads.Load() != 0 {
		t.Errorf("dials = %d, secret reads = %d, want none", e.f.dials.Load(), e.f.reads.Load())
	}
}

func TestMessagesMoveMovesExactlyOneMessage(t *testing.T) {
	e := changeEnvironment(t)
	uid := e.f.add(t, "INBOX", "alice@example.net", "one", base)
	stay := e.f.add(t, "INBOX", "alice@example.net", "two", base.Add(time.Hour))
	validity := validityOf(t, e, "INBOX")

	result, err := e.confirmed("infomaniakmail.messages.move", "changefolders", refArgs("INBOX", uid, validity, `,"destination":"Archive"`))
	if err != nil {
		t.Fatal(err)
	}
	var got MoveResult
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatal(err)
	}
	if got.Folder != "INBOX" || got.UID != uid || got.Destination != "Archive" || got.DestinationUID == 0 ||
		got.DestinationUIDValidity == 0 {
		t.Errorf("result = %+v", got)
	}
	if source := uids(list(t, e, "open", `{"folder":"INBOX"}`)); len(source) != 1 || source[0] != stay {
		t.Errorf("INBOX = %v, want only %d", source, stay)
	}
	if moved := uids(list(t, e, "open", `{"folder":"Archive"}`)); len(moved) != 1 || moved[0] != got.DestinationUID {
		t.Errorf("Archive = %v, want %d", moved, got.DestinationUID)
	}
	if n := commands(e, moveCommand); n != 1 || regexp.MustCompile(`T\d+ UID MOVE`).FindString(e.f.wire.String()) == "" {
		t.Errorf("want exactly one UID MOVE, got %d:\n%s", n, e.f.wire.String())
	}
	if commands(e, storeCommand) != 0 || commands(e, expungeCommand) != 0 {
		t.Errorf("a move must not STORE or EXPUNGE:\n%s", e.f.wire.String())
	}
}

func TestChangesStayInsideTheFolderTargets(t *testing.T) {
	e := changeEnvironment(t)
	e.f.folder(t, "Secret")
	uid := e.f.add(t, "INBOX", "alice@example.net", "one", base)
	validity := validityOf(t, e, "INBOX")
	dials, reads := e.f.dials.Load(), e.f.reads.Load()

	for _, tc := range []struct{ tool, folder, extra string }{
		{"flag", "Secret", `,"flag":"seen","set":true`}, {"move", "Secret", `,"destination":"Archive"`},
		{"delete", "Secret", ``}, {"expunge", "Secret", ``},
		{"move", "INBOX", `,"destination":"Secret"`}, {"move", "INBOX", `,"destination":"INBOX"`},
		{"move", "INBOX", `,"destination":"Arch*"`}, {"move", "INBOX", `,"destination":"inbox"`},
	} {
		_, err := e.confirmed("infomaniakmail."+"messages."+tc.tool, "changefolders", refArgs(tc.folder, uid, validity, tc.extra))
		if !isInvalidRequest(err) {
			t.Errorf("%s %s%s: err = %v, want an invalid request", tc.tool, tc.folder, tc.extra, err)
			continue
		}
		for _, leaked := range []string{"Secret", "Archive", "Trash"} {
			if strings.Contains(err.Error(), leaked) {
				t.Errorf("%s: error %q names %s", tc.tool, err, leaked)
			}
		}
	}
	if e.f.dials.Load() != dials || e.f.reads.Load() != reads {
		t.Errorf("dials %d -> %d, secret reads %d -> %d, want none", dials, e.f.dials.Load(), reads, e.f.reads.Load())
	}
}

func TestChangesCheckUIDValidityAndSenderBeforeAnyChange(t *testing.T) {
	e := changeEnvironment(t)
	allowed := e.f.add(t, "INBOX", "Alice <alice@example.net>", "ok", base)
	foreign := e.f.add(t, "INBOX", "mallory@example.org", "no", base)
	validity := validityOf(t, e, "INBOX")

	for _, tc := range []struct{ tool, extra string }{
		{"flag", `,"flag":"seen","set":true`}, {"move", `,"destination":"Archive"`}, {"delete", ``}, {"expunge", ``},
	} {
		operation := "infomaniakmail.messages." + tc.tool
		if _, err := e.confirmed(operation, "change", refArgs("INBOX", allowed, validity+1, tc.extra)); !isInvalidRequest(err) {
			t.Errorf("%s wrong uidvalidity: err = %v, want invalid request", tc.tool, err)
		}
		var messages []string
		for _, uid := range []uint32{foreign, 9999} {
			_, err := e.confirmed(operation, "changesenders", refArgs("INBOX", uid, validity, tc.extra))
			if classOf(err) != provider.ClassNotFound {
				t.Errorf("%s %d: err = %v, want not-found", tc.tool, uid, err)
				continue
			}
			messages = append(messages, err.Error())
		}
		if len(messages) == 2 && messages[0] != messages[1] {
			t.Errorf("%s: a forbidden sender answers %q, a missing UID %q", tc.tool, messages[0], messages[1])
		}
	}
	if n := commands(e, storeCommand) + commands(e, moveCommand) + commands(e, expungeCommand); n != 0 {
		t.Errorf("%d changing commands reached the server:\n%s", n, e.f.wire.String())
	}
	if len(flagsOf(t, e, "INBOX", foreign)) != 0 || len(uids(list(t, e, "open", `{"folder":"INBOX"}`))) != 2 {
		t.Errorf("the forbidden message changed")
	}
}

func TestMessagesDeleteMovesToTheTrashFolderOnly(t *testing.T) {
	e := changeEnvironment(t)
	uid := e.f.add(t, "INBOX", "alice@example.net", "one", base)
	validity := validityOf(t, e, "INBOX")

	result, err := e.confirmed("infomaniakmail.messages.delete", "changefolders", refArgs("INBOX", uid, validity, ""))
	if err != nil {
		t.Fatal(err)
	}
	var got MoveResult
	if err := json.Unmarshal([]byte(result), &got); err != nil || got.Destination != "Trash" || got.UID != uid {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if len(uids(list(t, e, "open", `{"folder":"INBOX"}`))) != 0 || len(uids(list(t, e, "open", `{"folder":"Trash"}`))) != 1 {
		t.Errorf("the message is not in the trash only")
	}
	if commands(e, moveCommand) != 1 || commands(e, expungeCommand) != 0 || commands(e, storeCommand) != 0 {
		t.Errorf("want one UID MOVE and nothing else:\n%s", e.f.wire.String())
	}
	// A message in the trash is not deleted a second time.
	trashed := list(t, e, "open", `{"folder":"Trash"}`)
	if _, err := e.confirmed("infomaniakmail.messages.delete", "changefolders",
		refArgs("Trash", trashed.Messages[0].UID, trashed.UIDValidity, "")); !isInvalidRequest(err) {
		t.Errorf("delete from the trash: err = %v, want invalid request", err)
	}
}

func TestMessagesDeleteRefusesWithoutAnUnambiguousAllowedTrash(t *testing.T) {
	cases := map[string]func(t *testing.T, e *environment) string{
		// A folder named Trash without the attribute is never taken for the trash.
		"no attribute": func(t *testing.T, e *environment) string {
			for _, name := range []string{"INBOX", "Trash"} {
				e.f.folder(t, name)
			}
			return "change"
		},
		"two trash folders": func(t *testing.T, e *environment) string {
			for _, name := range []string{"INBOX", "Trash", "Bin"} {
				e.f.folder(t, name)
			}
			markSpecialUse(t, e.f, "Trash", imap.MailboxAttrTrash)
			markSpecialUse(t, e.f, "Bin", imap.MailboxAttrTrash)
			return "change"
		},
		"trash outside the folder targets": func(t *testing.T, e *environment) string {
			for _, name := range []string{"INBOX", "Archive", "Trash"} {
				e.f.folder(t, name)
			}
			markSpecialUse(t, e.f, "Trash", imap.MailboxAttrTrash)
			return "changenotrash"
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnvironment(t)
			connection := setup(t, e)
			uid := e.f.add(t, "INBOX", "alice@example.net", "one", base)
			validity := validityOf(t, e, "INBOX")
			_, err := e.confirmed("infomaniakmail.messages.delete", connection, refArgs("INBOX", uid, validity, ""))
			if !isInvalidRequest(err) || strings.Contains(err.Error(), "Trash") || strings.Contains(err.Error(), "Archive") {
				t.Errorf("err = %v, want an invalid request that names no folder", err)
			}
			if commands(e, moveCommand)+commands(e, storeCommand)+commands(e, expungeCommand) != 0 ||
				len(uids(list(t, e, "open", `{"folder":"INBOX"}`))) != 1 {
				t.Errorf("a refused delete changed the mailbox:\n%s", e.f.wire.String())
			}
		})
	}
}

func TestMessagesExpungeRemovesOnlyTheOneUID(t *testing.T) {
	e := changeEnvironment(t)
	target := e.f.add(t, "Trash", "alice@example.net", "gone", base)
	marked := e.f.add(t, "Trash", "alice@example.net", "marked by someone else", base.Add(time.Hour), imap.FlagDeleted)
	plain := e.f.add(t, "Trash", "alice@example.net", "plain", base.Add(2*time.Hour))
	validity := validityOf(t, e, "Trash")

	result, err := e.confirmed("infomaniakmail.messages.expunge", "change", refArgs("Trash", target, validity, ""))
	if err != nil {
		t.Fatal(err)
	}
	var got MessageState
	if err := json.Unmarshal([]byte(result), &got); err != nil || got.UID != target || got.Folder != "Trash" || got.UIDValidity != validity {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if remaining := uids(list(t, e, "open", `{"folder":"Trash"}`)); fmt.Sprint(remaining) != fmt.Sprint([]uint32{plain, marked}) {
		t.Errorf("Trash = %v, want %d and %d (the message another client marked stays)", remaining, plain, marked)
	}
	wire := e.f.wire.String()
	if !regexp.MustCompile(`T\d+ UID EXPUNGE ` + fmt.Sprint(target) + `\r?\n`).MatchString(wire) {
		t.Errorf("want UID EXPUNGE for the one UID:\n%s", wire)
	}
	for _, line := range strings.Split(wire, "\n") {
		if regexp.MustCompile(`T\d+ EXPUNGE\b`).MatchString(line) {
			t.Errorf("a folder-wide EXPUNGE was sent: %q", line)
		}
	}
	if commands(e, moveCommand) != 0 {
		t.Errorf("expunge must not move:\n%s", wire)
	}
}

// The server cannot be told to expunge one UID, so nothing is marked or removed.
func TestMessagesExpungeNeedsUIDPlus(t *testing.T) {
	e := newEnvironmentCaps(t, imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapMove: {}, imap.CapSpecialUse: {}}, nil)
	e.f.folder(t, "INBOX")
	uid := e.f.add(t, "INBOX", "alice@example.net", "one", base)
	validity := validityOf(t, e, "INBOX")

	_, err := e.confirmed("infomaniakmail.messages.expunge", "change", refArgs("INBOX", uid, validity, ""))
	if classOf(err) != provider.ClassProviderError || !strings.Contains(err.Error(), "nothing was changed") {
		t.Errorf("err = %v, want a provider error that says nothing was changed", err)
	}
	if commands(e, storeCommand)+commands(e, expungeCommand) != 0 || len(flagsOf(t, e, "INBOX", uid)) != 0 {
		t.Errorf("the message was marked or expunged:\n%s", e.f.wire.String())
	}
}

// Without MOVE no COPY, mark, and expunge sequence replaces it.
func TestMovingNeedsMove(t *testing.T) {
	e := newEnvironmentCaps(t, imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {}, imap.CapSpecialUse: {}}, nil)
	for _, name := range []string{"INBOX", "Archive", "Trash"} {
		e.f.folder(t, name)
	}
	markSpecialUse(t, e.f, "Trash", imap.MailboxAttrTrash)
	uid := e.f.add(t, "INBOX", "alice@example.net", "one", base)
	validity := validityOf(t, e, "INBOX")
	for tool, extra := range map[string]string{"move": `,"destination":"Archive"`, "delete": ``} {
		_, err := e.confirmed("infomaniakmail.messages."+tool, "change", refArgs("INBOX", uid, validity, extra))
		if classOf(err) != provider.ClassProviderError || !strings.Contains(err.Error(), "nothing was changed") {
			t.Errorf("%s: err = %v, want a provider error that says nothing was changed", tool, err)
		}
	}
	if commands(e, moveCommand)+commands(e, storeCommand)+commands(e, expungeCommand) != 0 {
		t.Errorf("a fallback reached the server:\n%s", e.f.wire.String())
	}
}

func TestDeleteNeedsSpecialUse(t *testing.T) {
	e := newEnvironmentCaps(t, imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {}, imap.CapMove: {}}, nil)
	for _, name := range []string{"INBOX", "Trash"} {
		e.f.folder(t, name)
	}
	markSpecialUse(t, e.f, "Trash", imap.MailboxAttrTrash)
	uid := e.f.add(t, "INBOX", "alice@example.net", "one", base)
	validity := validityOf(t, e, "INBOX")
	if _, err := e.confirmed("infomaniakmail.messages.delete", "change", refArgs("INBOX", uid, validity, "")); !isInvalidRequest(err) {
		t.Errorf("err = %v, want an invalid request", err)
	}
	if commands(e, moveCommand) != 0 {
		t.Errorf("a move was sent:\n%s", e.f.wire.String())
	}
}

// dropAfter ends the connection right after the client wrote a command that matches, so the server may have
// applied it while the client never reads the answer. It counts what the client wrote.
type dropAfter struct {
	net.Conn
	match  *regexp.Regexp
	writes *atomic.Int32
}

func (d dropAfter) Write(p []byte) (int, error) {
	n, err := d.Conn.Write(p)
	if err == nil && d.match.Match(p) {
		d.writes.Add(1)
		_ = d.Conn.Close()
	}
	return n, err
}

func TestAnUnclearOutcomeIsReportedAndNeverRepeated(t *testing.T) {
	for _, tc := range []struct {
		tool, extra, command, want string
	}{
		{"flag", `,"flag":"seen","set":true`, `UID STORE`, "may have been applied"},
		{"move", `,"destination":"Archive"`, `UID MOVE`, "may have been applied"},
		{"delete", ``, `UID MOVE`, "may have been applied"},
		{"expunge", ``, `UID STORE`, "may have been applied"},
		{"expunge", ``, `UID EXPUNGE`, "marked as deleted"},
	} {
		t.Run(tc.tool+" "+tc.command, func(t *testing.T) {
			e := changeEnvironment(t)
			uid := e.f.add(t, "INBOX", "alice@example.net", "one", base)
			validity := validityOf(t, e, "INBOX")
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

			_, err := e.confirmed("infomaniakmail.messages."+tc.tool, "change", refArgs("INBOX", uid, validity, tc.extra))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to say %q", err, tc.want)
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

// A tagged refusal changed nothing and says so by not claiming the opposite; the server's words stay out.
func TestAServerRefusalIsDefiniteAndCarriesNoServerText(t *testing.T) {
	e := changeEnvironment(t)
	uid := e.f.add(t, "INBOX", "alice@example.net", "one", base)
	validity := validityOf(t, e, "INBOX")

	_, err := e.confirmed("infomaniakmail.messages.move", "change", refArgs("INBOX", uid, validity, `,"destination":"Missing"`))
	if classOf(err) == "" || strings.Contains(err.Error(), "may have been applied") {
		t.Fatalf("err = %v, want a definite failure", err)
	}
	text := e.red.Error(err)
	for _, forbidden := range []string{"imap", "missing", passwordVal, "trycreate"} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Errorf("error %q contains %q", text, forbidden)
		}
	}
	if commands(e, moveCommand) != 1 {
		t.Errorf("MOVE sent %d times, want once", commands(e, moveCommand))
	}
	if len(uids(list(t, e, "open", `{"folder":"INBOX"}`))) != 1 {
		t.Errorf("the message was moved")
	}
}
