package infomaniakmail

import (
	"net"
	"regexp"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

var listDelimiterCommand = regexp.MustCompile(`T\d+ LIST "" ""`)

func treeEnvironment(t *testing.T, targets ...string) *environment {
	t.Helper()
	e := newEnvironmentWith(t, func(cfg *config.Config) {
		c := cfg.Connections["change"]
		c.Targets = append([]string{"mailbox/" + mailbox}, targets...)
		c.Tools = append(c.Tools, foldersList.ID, messagesList.ID, messagesGet.ID)
		cfg.Connections["tree"] = c
	})
	for _, name := range []string{"INBOX", "Kunde", "Kunde/A", "Kunde/A/B", "Kunden", "Kunde2", "Archive", "Trash"} {
		e.f.folder(t, name)
	}
	markSpecialUse(t, e.f, "Trash", imap.MailboxAttrTrash)
	return e
}

func TestTreeTargetValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		targets []string
		wantErr bool
	}{
		{"tree", []string{"folder/Kunde/*"}, false},
		{"tree of a path", []string{"folder/Kunde/Alt/*"}, false},
		{"tree and exact together", []string{"folder/Kunde", "folder/Kunde/*", "folder/Archive"}, false},
		{"all folders", []string{"folder/*"}, true},
		{"empty name", []string{"folder//*"}, true},
		{"wildcard in name", []string{"folder/A*"}, true},
		{"wildcard in middle", []string{"folder/A/*/*"}, true},
		{"percent", []string{"folder/A%/*"}, true},
		{"duplicate", []string{"folder/Kunde/*", "folder/Kunde/*"}, true},
	} {
		_, err := parseScope(append([]string{"mailbox/box@example.com"}, tc.targets...))
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr %t", tc.name, err, tc.wantErr)
		}
		if err != nil && strings.Contains(err.Error(), "Kunde") {
			t.Errorf("%s: error %q quotes a configured value", tc.name, err)
		}
	}
}

func TestTreeScopeMembership(t *testing.T) {
	bound, err := parseScope([]string{"mailbox/box@example.com", "folder/Kunde/*", "folder/Archive"})
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{
		"Kunde": true, "Kunde/A": true, "Kunde/A/B": true, "Archive": true,
		"Kunden": false, "Kunde2": false, "Kunde/": false, "kunde/A": false, "Archive/Sub": false, "Other": false,
	} {
		if got := bound.allowsFolderDelim(name, "/"); got != want {
			t.Errorf("delimiter /: %q = %t, want %t", name, got, want)
		}
	}
	// A flat hierarchy admits only the targets themselves.
	for name, want := range map[string]bool{"Kunde": true, "Archive": true, "Kunde/A": false, "Kunde.A": false} {
		if got := bound.allowsFolderDelim(name, ""); got != want {
			t.Errorf("no delimiter: %q = %t, want %t", name, got, want)
		}
	}
	// The delimiter decides: a dot hierarchy does not admit a slash name.
	if bound.allowsFolderDelim("Kunde/A", ".") || !bound.allowsFolderDelim("Kunde.A", ".") {
		t.Error("the server's delimiter must decide what a subfolder is")
	}
	// Without a tree target the check without network access stays exact.
	exact, _ := parseScope([]string{"mailbox/box@example.com", "folder/Kunde"})
	if exact.allowsFolder("Kunde/A") || !exact.allowsFolder("Kunde") {
		t.Error("folder/PATH must stay exact")
	}
	// With one, a longer name passes provisionally only when it begins with NAME.
	if !bound.allowsFolder("Kunden") || bound.allowsFolder("Other") {
		t.Error("the provisional check must be limited to names that begin with a tree target")
	}
	inbox, _ := parseScope([]string{"mailbox/box@example.com", "folder/inbox/*"})
	if !inbox.allowsFolderDelim("INBOX", "/") || !inbox.allowsFolderDelim("INBOX/Sub", "/") || inbox.allowsFolderDelim("Inbox/Sub", "/") {
		t.Error("INBOX is case-insensitive as a name only")
	}
}

func TestTreeListsTheFolderAndSubfoldersOnly(t *testing.T) {
	e := treeEnvironment(t, "folder/Kunde/*")
	e.f.add(t, "Kunde", "a@example.net", "root", base)
	e.f.add(t, "Kunde/A/B", "a@example.net", "deep", base)
	e.f.add(t, "Kunden", "a@example.net", "sibling", base)
	e.f.add(t, "Kunde2", "a@example.net", "sibling2", base)
	before := len(e.f.wire.String())

	for _, folder := range []string{"Kunde", "Kunde/A/B"} {
		if page := list(t, e, "tree", `{"folder":"`+folder+`"}`); page.Folder != folder || len(page.Messages) != 1 {
			t.Errorf("%s: page = %+v", folder, page)
		}
	}
	for _, folder := range []string{"Kunden", "Kunde2", "Archive", "kunde/A"} {
		_, err := e.invoke("infomaniak.mail.messages.list", "tree", `{"folder":"`+folder+`"}`)
		if !isInvalidRequest(err) {
			t.Errorf("%s: err = %v, want an invalid request", folder, err)
		} else if strings.Contains(err.Error(), "Kunde") {
			t.Errorf("%s: refusal %q names a folder", folder, err)
		}
	}
	// Siblings that begin with NAME were refused after the delimiter was known and never opened.
	wire := e.f.wire.String()[before:]
	if strings.Contains(wire, "Kunden") || strings.Contains(wire, "Kunde2") {
		t.Errorf("a sibling folder reached the server:\n%s", wire)
	}
	// Reading the exact folder asks nothing; each subfolder call asks once.
	if n := len(listDelimiterCommand.FindAllString(wire, -1)); n != 3 {
		t.Errorf("LIST \"\" \"\" sent %d times, want once per call on a subfolder or prefix sibling (3)", n)
	}

	_, err := e.invoke("infomaniak.mail.messages.get", "tree", `{"folder":"Kunden","uid":1,"uidvalidity":1}`)
	if !isInvalidRequest(err) {
		t.Errorf("get on a sibling: err = %v", err)
	}
}

func TestTreeAskOnlyWhenNeeded(t *testing.T) {
	// No tree target: never a LIST for the delimiter.
	e := treeEnvironment(t, "folder/Kunde")
	e.f.add(t, "Kunde", "a@example.net", "x", base)
	list(t, e, "tree", `{"folder":"Kunde"}`)
	if _, err := e.invoke("infomaniak.mail.messages.list", "tree", `{"folder":"Kunde/A"}`); !isInvalidRequest(err) {
		t.Errorf("exact target admitted a subfolder: %v", err)
	}
	if n := len(listDelimiterCommand.FindAllString(e.f.wire.String(), -1)); n != 0 {
		t.Errorf("LIST \"\" \"\" sent %d times without a folder/PATH/* target", n)
	}

	// With a tree target: exactly one per call for a subfolder, none for the exact folder.
	e = treeEnvironment(t, "folder/Kunde/*")
	e.f.add(t, "Kunde", "a@example.net", "x", base)
	e.f.add(t, "Kunde/A", "a@example.net", "y", base)
	list(t, e, "tree", `{"folder":"Kunde"}`)
	if n := len(listDelimiterCommand.FindAllString(e.f.wire.String(), -1)); n != 0 {
		t.Errorf("exact folder asked %d times, want none", n)
	}
	list(t, e, "tree", `{"folder":"Kunde/A"}`)
	if n := len(listDelimiterCommand.FindAllString(e.f.wire.String(), -1)); n != 1 {
		t.Errorf("subfolder asked %d times, want 1", n)
	}
}

func TestTreeMoveChecksSourceAndDestination(t *testing.T) {
	e := treeEnvironment(t, "folder/Kunde/*")
	src := e.f.add(t, "Kunde/A", "a@example.net", "x", base)
	validity := validityOf(t, e, "Kunde/A")

	for _, destination := range []string{"Archive", "Kunden", "Kunde2"} {
		_, err := e.confirmed("infomaniak.mail.messages.move", "tree", refArgs("Kunde/A", src, validity, `,"destination":"`+destination+`"`))
		if !isInvalidRequest(err) {
			t.Errorf("destination %s: err = %v, want an invalid request", destination, err)
		}
	}
	if commands(e, moveCommand) != 0 {
		t.Errorf("a refused destination reached MOVE:\n%s", e.f.wire.String())
	}
	if _, err := e.confirmed("infomaniak.mail.messages.move", "tree", refArgs("Kunde/A", src, validity, `,"destination":"Kunde/A/B"`)); err != nil {
		t.Fatalf("move into a subfolder: %v", err)
	}
	if n := len(listDelimiterCommand.FindAllString(e.f.wire.String(), -1)); n < 1 {
		t.Error("move into a subfolder never asked for the delimiter")
	}
	if commands(e, moveCommand) != 1 {
		t.Errorf("want one MOVE:\n%s", e.f.wire.String())
	}
}

func TestTreeTrashMustBeInsideTheScope(t *testing.T) {
	e := treeEnvironment(t, "folder/Kunde/*")
	uid := e.f.add(t, "Kunde/A", "a@example.net", "x", base)
	validity := validityOf(t, e, "Kunde/A")
	_, err := e.confirmed("infomaniak.mail.messages.delete", "tree", refArgs("Kunde/A", uid, validity, ""))
	if !isInvalidRequest(err) {
		t.Errorf("trash outside the tree: err = %v, want a refusal", err)
	}

	e = treeEnvironment(t, "folder/Kunde/*")
	e.f.folder(t, "Kunde/Trash")
	markSpecialUse(t, e.f, "Kunde/Trash", imap.MailboxAttrTrash)
	markSpecialUse(t, e.f, "Trash", imap.MailboxAttr("\\Junk"))
	uid = e.f.add(t, "Kunde/A", "a@example.net", "x", base)
	validity = validityOf(t, e, "Kunde/A")
	if _, err := e.confirmed("infomaniak.mail.messages.delete", "tree", refArgs("Kunde/A", uid, validity, "")); err != nil {
		t.Errorf("trash inside the tree: %v", err)
	}
	if n := len(listDelimiterCommand.FindAllString(e.f.wire.String(), -1)); n != 0 {
		t.Errorf("a delimiter from the special-use LIST was asked again (%d)", n)
	}
}

func TestTreeFoldersListFilter(t *testing.T) {
	e := treeEnvironment(t, "folder/Kunde/*", "folder/Archive")
	result, err := e.invoke("infomaniak.mail.folders.list", "tree", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(folderNames(decodeFolders(t, result)), ",")
	if got != "Archive,Kunde,Kunde/A,Kunde/A/B" {
		t.Errorf("folders = %s", got)
	}
	if n := len(listDelimiterCommand.FindAllString(e.f.wire.String(), -1)); n != 0 {
		t.Errorf("folders.list asked for the delimiter separately (%d)", n)
	}
}

func TestFolderOKFailsClosed(t *testing.T) {
	bound, _ := parseScope([]string{"mailbox/box@example.com", "folder/Kunde/*"})
	// A server without a delimiter admits only the target itself, with no further question.
	flat := &session{scope: bound, delimKnown: true}
	if err := flat.folderOK("op", "Kunde"); err != nil {
		t.Errorf("target: %v", err)
	}
	if err := flat.folderOK("op", "Kunde/A"); !isInvalidRequest(err) {
		t.Errorf("flat hierarchy subfolder: %v, want a refusal", err)
	}
	// A failing question refuses, and the error does not name the folder.
	server, client := net.Pipe()
	_ = server.Close()
	broken := &session{scope: bound, client: imapclient.New(client, nil)}
	err := broken.folderOK("op", "KundeSecret/A")
	if err == nil || strings.Contains(err.Error(), "KundeSecret") {
		t.Errorf("failed LIST: err = %v, want a refusal without the folder name", err)
	}
}
