package infomaniakmail

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

var openCommand = regexp.MustCompile(`T\d+ (SELECT|EXAMINE)\b`)

var allToolIDs = []string{foldersList.ID, messagesList.ID, messagesGet.ID, attachmentsGet.ID, messagesFlag.ID,
	messagesMove.ID, messagesDelete.ID, messagesExpunge.ID, draftsCreate.ID, draftsUpdate.ID, draftsDelete.ID,
	messagesSend.ID, draftsSend.ID}

// excludeEnvironment offers every tool to the connection "ex" with the given targets and exclusions, over a
// mailbox with the folders of a customer tree and the special-use folders.
func excludeEnvironment(t *testing.T, targets, excludes []string) *environment {
	t.Helper()
	dir := t.TempDir()
	e := newEnvironmentWith(t, func(cfg *config.Config) {
		c := cfg.Connections["open"]
		c.Targets = append([]string{"mailbox/" + mailbox}, targets...)
		c.Exclude = excludes
		c.Tools = allToolIDs
		c.Files = config.Files{Read: []string{dir}, Write: []string{dir}}
		cfg.Connections["ex"] = c
	})
	for _, name := range []string{"INBOX", "Drafts", "Sent", "Trash", "Archive", "Kunde", "Kunde/A", "Kunde/A/B", "Kunden"} {
		e.f.folder(t, name)
	}
	markSpecialUse(t, e.f, "Drafts", imap.MailboxAttrDrafts)
	markSpecialUse(t, e.f, "Sent", imap.MailboxAttrSent)
	markSpecialUse(t, e.f, "Trash", imap.MailboxAttrTrash)
	return e
}

func TestExcludeConfigurationThroughDecode(t *testing.T) {
	reg := registry(t)
	document := func(targets, exclude string) string {
		return `version: 4
services:
  mail:
    provider: infomaniak.mail
credentials:
  mail-secret:
    provider: infomaniak.mail
    type: env
    values:
      password: QATLAS_MAIL_PASSWORD
connections:
  main:
    service: mail
    credential: mail-secret
    targets: [` + targets + `]
    exclude: [` + exclude + `]
`
	}
	const box = "mailbox/user@example.net"
	for name, tc := range map[string]struct {
		targets, exclude string
		valid            bool
	}{
		"folder":        {box, "folder/Spam", true},
		"folder tree":   {box, "folder/Spam/*", true},
		"sender":        {box, "sender/a@example.net", true},
		"all forms":     {box + ", folder/INBOX/*", "folder/Spam, folder/Old/*, sender/a@example.net", true},
		"mailbox":       {box + ", folder/INBOX", "mailbox/other@example.net", false},
		"equals target": {box + ", folder/CanaryFolder", "folder/CanaryFolder", false},
		"tree target":   {box + ", folder/CanaryFolder/*", "folder/CanaryFolder/*", false},
		"sender target": {box + ", sender/canary@example.net", "sender/canary@example.net", false},
		"wildcard":      {box, "folder/*", false},
		"provider star": {box, "'*'", false},
	} {
		_, err := config.Decode(strings.NewReader(document(tc.targets, tc.exclude)), reg)
		if (err == nil) != tc.valid {
			t.Errorf("%s: err = %v, valid %t", name, err, tc.valid)
		}
		if err != nil && (strings.Contains(err.Error(), "CanaryFolder") || strings.Contains(err.Error(), "canary@") ||
			strings.Contains(err.Error(), "other@example.net")) {
			t.Errorf("%s: error %q quotes a configured value", name, err)
		}
	}
}

// The scope fails closed on its own, without the core validation.
func TestExcludeParsingFailsClosed(t *testing.T) {
	base := []string{"mailbox/box@example.com", "folder/Target", "folder/Tree/*", "sender/t@example.net"}
	for name, tc := range map[string]struct {
		exclude []string
		valid   bool
	}{
		"folder":               {[]string{"folder/Spam"}, true},
		"tree":                 {[]string{"folder/Spam/*"}, true},
		"sender":               {[]string{"sender/s@example.net"}, true},
		"folder, tree":         {[]string{"folder/Spam", "folder/Spam/*"}, true},
		"mailbox":              {[]string{"mailbox/other@example.net"}, false},
		"own mailbox":          {[]string{"mailbox/box@example.com"}, false},
		"empty":                {[]string{""}, false},
		"unknown kind":         {[]string{"label/x"}, false},
		"all folders":          {[]string{"folder/*"}, false},
		"percent":              {[]string{"folder/A%"}, false},
		"bad sender":           {[]string{"sender/everyone"}, false},
		"duplicate":            {[]string{"folder/Spam", "folder/Spam"}, false},
		"INBOX twice":          {[]string{"folder/INBOX", "folder/inbox"}, false},
		"folder equals target": {[]string{"folder/Target"}, false},
		"tree equals target":   {[]string{"folder/Tree/*"}, false},
		"sender equals target": {[]string{"sender/" + capitalized("t@example.net")}, false},
	} {
		_, err := parseScopeExcluding(base, tc.exclude)
		if (err == nil) != tc.valid {
			t.Errorf("%s: err = %v, valid %t", name, err, tc.valid)
		}
		if err != nil {
			for _, value := range tc.exclude {
				if _, rest, ok := strings.Cut(value, "/"); ok && len(rest) > 3 && strings.Contains(err.Error(), rest) {
					t.Errorf("%s: error %q quotes the configured value", name, err)
				}
			}
		}
	}
}

func TestExcludeScopeRules(t *testing.T) {
	bound, err := parseScopeExcluding([]string{"mailbox/box@example.com"},
		[]string{"folder/inbox", "folder/Kunde/A/*", "folder/Plain", "sender/" + capitalized("spam@example.org")})
	if err != nil {
		t.Fatal(err)
	}
	// Before login only the names a folder/PATH exclusion itself carries are known; a longer name is decided
	// with the delimiter.
	for name, want := range map[string]bool{"INBOX": false, "Inbox": false, "Kunde": true, "Kunde/A": true,
		"Plain": false, "Plain/Sub": true, "Other": true} {
		if got := bound.allowsFolder(name); got != want {
			t.Errorf("allowsFolder(%q) = %t, want %t", name, got, want)
		}
	}
	for _, tc := range []struct {
		name, delim string
		want        bool
	}{
		{"Kunde", "/", true}, {"Kunde/A", "/", true}, {"Kunde/A/B", "/", false}, {"Kunde/AB", "/", true},
		{"Kunde/A/B", "", true}, {"Kunde.A.B", ".", true}, {"Kunde/A/B", ".", true}, {"Kunde.A.B", "/", true},
		{"Plain", "/", false}, {"Plain/Sub", "/", false}, {"Plain/Sub", "", true}, {"Plainer", "/", true},
		{"INBOX", "/", false}, {"Inbox/Sub", "/", false},
	} {
		if got := bound.allowsFolderDelim(tc.name, tc.delim); got != tc.want {
			t.Errorf("allowsFolderDelim(%q, %q) = %t, want %t", tc.name, tc.delim, got, tc.want)
		}
	}
	for address, want := range map[string]bool{"spam@example.org": false, strings.ToUpper("spam@example.org"): false,
		"other@example.org": true, "box@example.com": true} {
		if got := bound.allowsSender(address); got != want {
			t.Errorf("allowsSender(%q) = %t, want %t", address, got, want)
		}
	}
	// An exclusion narrows a target list too, and the mailbox itself can be excluded as a sender.
	narrow, _ := parseScopeExcluding([]string{"mailbox/box@example.com", "folder/Kunde", "sender/a@example.net"},
		[]string{"folder/Kunde/A", "sender/box@example.com"})
	if !narrow.allowsFolderDelim("Kunde", "/") || !narrow.allowsFolderDelim("Kunde/B", "/") ||
		narrow.allowsFolderDelim("Kunde/A", "/") || narrow.allowsFolderDelim("Kunde/A/B", "/") ||
		!narrow.allowsSender("a@example.net") || narrow.allowsSender("box@example.com") {
		t.Errorf("narrowed scope = %+v", narrow)
	}
}

func TestExcludeFoldersListHidesFoldersAndSubtrees(t *testing.T) {
	e := excludeEnvironment(t, nil, []string{"folder/Trash", "folder/Kunde/A/*"})
	result, err := e.invoke("infomaniak.mail.folders.list", "ex", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(folderNames(decodeFolders(t, result)), ","); got != "Archive,Drafts,INBOX,Kunde,Kunde/A,Kunden,Sent" {
		t.Errorf("folders = %s", got)
	}
	// A folder/PATH exclusion takes the subfolders out with the folder.
	e = excludeEnvironment(t, nil, []string{"folder/Kunde", "folder/Kunden"})
	result, err = e.invoke("infomaniak.mail.folders.list", "ex", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(folderNames(decodeFolders(t, result)), ","); got != "Archive,Drafts,INBOX,Sent,Trash" {
		t.Errorf("folders = %s", got)
	}
}

// The in-memory server reports no children attributes, so the refresh is checked on the listing itself.
func TestRefreshChildrenUsesOnlyVisibleFolders(t *testing.T) {
	folder := func(name string, attrs ...string) Folder {
		return Folder{Name: name, Delimiter: "/", Attributes: attrs}
	}
	byName := map[string]Folder{
		"Kunde":   folder("Kunde", `\HasChildren`),
		"Kunden":  folder("Kunden", `\HasChildren`, `\Sent`),
		"Archive": folder("Archive", `\HasNoChildren`),
		"INBOX":   folder("INBOX", `\HasNoChildren`),
		"Plain":   folder("Plain", `\Drafts`),
		"Flat":    {Name: "Flat", Attributes: []string{`\HasChildren`}},
	}
	byName["Archive/Sub"] = folder("Archive/Sub")
	byName["Kunde/B"] = folder("Kunde/B")
	refreshChildren(byName)
	want := map[string]string{"Kunde": `\HasChildren`, "Kunden": `\HasNoChildren,\Sent`,
		"Archive": `\HasChildren`, "INBOX": `\HasNoChildren`, "Plain": `\Drafts`, "Flat": `\HasNoChildren`}
	for name, attrs := range want {
		if got := strings.Join(byName[name].Attributes, ","); got != attrs {
			t.Errorf("%s attributes = %s, want %s", name, got, attrs)
		}
	}
}

func TestExcludeFoldersListWithTargets(t *testing.T) {
	e := excludeEnvironment(t, []string{"folder/Kunde", "folder/Archive"}, []string{"folder/Kunde/A/*"})
	result, err := e.invoke("infomaniak.mail.folders.list", "ex", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(folderNames(decodeFolders(t, result)), ","); got != "Archive,Kunde,Kunde/A" {
		t.Errorf("folders = %s", got)
	}
}

// An exact excluded folder is refused before the secret and before any connection, by every tool.
func TestExcludeExactFolderIsRefusedBeforeSecretAndNetwork(t *testing.T) {
	e := excludeEnvironment(t, nil, []string{"folder/Archive", "folder/Kunde"})
	dials, reads := e.f.dials.Load(), e.f.reads.Load()
	for tool, args := range map[string]string{
		"messages.list":    `{"folder":"Archive"}`,
		"messages.get":     getArgs("Archive", 1, 1),
		"attachments.get":  attachmentArgs("Archive", 1, 1, "2", ""),
		"messages.flag":    refArgs("Archive", 1, 1, `,"flag":"seen","set":true`),
		"messages.move":    refArgs("Archive", 1, 1, `,"destination":"INBOX"`),
		"messages.delete":  refArgs("Archive", 1, 1, ""),
		"messages.expunge": refArgs("Archive", 1, 1, ""),
	} {
		_, err := e.confirmed("infomaniak.mail."+tool, "ex", args)
		if !isInvalidRequest(err) {
			t.Errorf("%s: err = %v, want an invalid request", tool, err)
		} else if strings.Contains(err.Error(), "Archive") || strings.Contains(err.Error(), "xclu") {
			t.Errorf("%s: refusal %q names the folder or an exclusion", tool, err)
		}
	}
	for _, destination := range []string{"Archive", "Kunde"} {
		_, err := e.confirmed("infomaniak.mail.messages.move", "ex", refArgs("INBOX", 1, 1, `,"destination":"`+destination+`"`))
		if !isInvalidRequest(err) {
			t.Errorf("move to %s: err = %v, want an invalid request", destination, err)
		}
	}
	if e.f.dials.Load() != dials || e.f.reads.Load() != reads {
		t.Errorf("dials = %d, secret reads = %d, want none", e.f.dials.Load()-dials, e.f.reads.Load()-reads)
	}
}

// A name that begins with an excluded folder is decided with the delimiter after the login, and never opened.
func TestExcludeTreePrefixAsksForTheDelimiterAndRefuses(t *testing.T) {
	e := excludeEnvironment(t, nil, []string{"folder/Kunde/*"})
	uid := e.f.add(t, "Kunde/A", "alice@example.net", "x", base)
	validity := validityOf(t, e, "Kunde/A")
	before := len(e.f.wire.String())
	for tool, args := range map[string]string{
		"messages.list":    `{"folder":"Kunde/A"}`,
		"messages.get":     getArgs("Kunde/A", uid, validity),
		"attachments.get":  attachmentArgs("Kunde/A", uid, validity, "2", ""),
		"messages.flag":    refArgs("Kunde/A", uid, validity, `,"flag":"seen","set":true`),
		"messages.move":    refArgs("Kunde/A", uid, validity, `,"destination":"Archive"`),
		"messages.delete":  refArgs("Kunde/A", uid, validity, ""),
		"messages.expunge": refArgs("Kunde/A", uid, validity, ""),
	} {
		_, err := e.confirmed("infomaniak.mail."+tool, "ex", args)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "Kunde") {
			t.Errorf("%s: err = %v, want an invalid request that names no folder", tool, err)
		}
	}
	_, err := e.confirmed("infomaniak.mail.messages.move", "ex", refArgs("INBOX", 1, 1, `,"destination":"Kunde/A/B"`))
	if !isInvalidRequest(err) {
		t.Errorf("move into an excluded subfolder: err = %v", err)
	}
	wire := e.f.wire.String()[before:]
	if n := len(listDelimiterCommand.FindAllString(wire, -1)); n < 7 {
		t.Errorf("LIST \"\" \"\" sent %d times, want it for every call on the subfolder", n)
	}
	if openCommand.MatchString(wire) || commands(e, storeCommand) != 0 || commands(e, expungeCommand) != 0 ||
		commands(e, moveCommand) != 0 {
		t.Errorf("an excluded folder was opened or changed:\n%s", wire)
	}
	// A sibling with the same prefix is decided by the delimiter, and folder/PATH/* keeps the folder itself.
	if _, err := e.invoke("infomaniak.mail.messages.list", "ex", `{"folder":"Kunden"}`); err != nil {
		t.Errorf("sibling of excluded subfolders: %v", err)
	}
	if _, err := e.invoke("infomaniak.mail.messages.list", "ex", `{"folder":"Kunde"}`); err != nil {
		t.Errorf("the folder of a folder/PATH/* exclusion: %v", err)
	}
}

func TestExcludeFolderTakesItsSubfolders(t *testing.T) {
	e := excludeEnvironment(t, nil, []string{"folder/Kunde"})
	e.f.add(t, "Kunde/A", "alice@example.net", "x", base)
	e.f.add(t, "Kunden", "alice@example.net", "y", base)
	for _, folder := range []string{"Kunde", "Kunde/A", "Kunde/A/B"} {
		if _, err := e.invoke("infomaniak.mail.messages.list", "ex", `{"folder":"`+folder+`"}`); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v, want an invalid request", folder, err)
		}
	}
	if page := list(t, e, "ex", `{"folder":"Kunden"}`); len(page.Messages) != 1 {
		t.Errorf("sibling: page = %+v", page)
	}
}

func TestExcludeInboxIsCaseInsensitive(t *testing.T) {
	e := excludeEnvironment(t, nil, []string{"folder/inbox"})
	dials := e.f.dials.Load()
	for _, name := range []string{"INBOX", "Inbox", "inbox"} {
		if _, err := e.invoke("infomaniak.mail.messages.list", "ex", `{"folder":"`+name+`"}`); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if e.f.dials.Load() != dials {
		t.Error("an excluded INBOX reached the network")
	}
}

func TestExcludeFolderOKFailsClosed(t *testing.T) {
	bound, _ := parseScopeExcluding([]string{"mailbox/box@example.com"}, []string{"folder/Kunde/*"})
	flat := &session{scope: bound, delimKnown: true}
	if err := flat.folderOK("op", "Kunde/A"); err != nil {
		t.Errorf("flat hierarchy: %v, want admitted (no subfolders)", err)
	}
	if err := (&session{scope: bound}).folderOK("op", "Kunde"); err != nil {
		t.Errorf("the folder itself: %v, want admitted without a question", err)
	}
	if err := (&session{scope: bound, delimKnown: true, delim: "/"}).folderOK("op", "Kunde/A"); !isInvalidRequest(err) {
		t.Errorf("subfolder: %v, want a refusal", err)
	}
	server, client := net.Pipe()
	_ = server.Close()
	broken := &session{scope: bound, client: imapclient.New(client, nil)}
	err := broken.folderOK("op", "KundeSecret/A")
	if err == nil || strings.Contains(err.Error(), "KundeSecret") {
		t.Errorf("failed LIST: err = %v, want a refusal without the folder name", err)
	}
}

func notFound(t *testing.T, label string, err error) {
	t.Helper()
	if classOf(err) != provider.ClassNotFound {
		t.Errorf("%s: err = %v, want not-found", label, err)
	}
}

func TestExcludeSenderIsLeftOutAndNotFound(t *testing.T) {
	e := excludeEnvironment(t, nil, []string{"sender/spam@example.org"})
	good := e.f.addRaw(t, "INBOX", mixed("Alice <alice@example.net>", "good", pdfPart("a.pdf", []byte("data"))))
	spam := e.f.addRaw(t, "INBOX", mixed(strings.ToUpper("spam@example.org"), "spam", pdfPart("a.pdf", []byte("data"))))
	both := e.f.addRaw(t, "INBOX", mixed("alice@example.net, spam@example.org", "both", pdfPart("a.pdf", []byte("data"))))
	missing := uint32(9999)
	validity := validityOf(t, e, "INBOX")

	page := list(t, e, "ex", `{"folder":"INBOX"}`)
	if got := uids(page); len(got) != 1 || got[0] != good {
		t.Errorf("listed UIDs = %v, want only %d", got, good)
	}
	// matched counts the server hits before the local filter.
	if page.Matched != 3 || page.Count != 1 {
		t.Errorf("matched = %d, count = %d", page.Matched, page.Count)
	}
	if got := getMessage(t, e, "ex", getArgs("INBOX", good, validity)); got.UID != good {
		t.Errorf("message = %+v", got)
	}

	answers := map[string]map[uint32]string{}
	for _, uid := range []uint32{spam, both, missing} {
		_, err := e.invoke("infomaniak.mail.messages.get", "ex", getArgs("INBOX", uid, validity))
		notFound(t, "messages.get", err)
		_, err = e.invoke("infomaniak.mail.attachments.get", "ex", attachmentArgs("INBOX", uid, validity, "2", ""))
		notFound(t, "attachments.get", err)
		for tool, extra := range map[string]string{"messages.flag": `,"flag":"seen","set":true`,
			"messages.move": `,"destination":"Archive"`, "messages.delete": "", "messages.expunge": ""} {
			_, err := e.confirmed("infomaniak.mail."+tool, "ex", refArgs("INBOX", uid, validity, extra))
			notFound(t, tool, err)
			if answers[tool] == nil {
				answers[tool] = map[uint32]string{}
			}
			if err != nil {
				answers[tool][uid] = err.Error()
			}
		}
	}
	for tool, got := range answers {
		if got[spam] != got[missing] || got[both] != got[missing] {
			t.Errorf("%s: an excluded sender answers differently from a missing UID: %q / %q", tool, got[spam], got[missing])
		}
	}
	if n := commands(e, storeCommand) + commands(e, moveCommand) + commands(e, expungeCommand); n != 0 {
		t.Errorf("%d changing commands reached the server", n)
	}
	if len(uids(list(t, e, "ex", `{"folder":"INBOX"}`))) != 1 || len(uids(list(t, e, "open", `{"folder":"INBOX"}`))) != 3 {
		t.Error("an excluded message changed")
	}
	if strings.Contains(strings.ToUpper(e.f.wire.String()), "NOT FROM") || strings.Contains(strings.ToUpper(e.f.wire.String()), " NOT ") {
		t.Errorf("a negative search reached the server:\n%s", e.f.wire.String())
	}
}

func TestExcludeSenderArgumentIsRefusedBeforeNetwork(t *testing.T) {
	e := excludeEnvironment(t, nil, []string{"sender/spam@example.org"})
	dials, reads := e.f.dials.Load(), e.f.reads.Load()
	for _, sender := range []string{"spam@example.org", capitalized("spam@example.org")} {
		_, err := e.invoke("infomaniak.mail.messages.list", "ex", `{"folder":"INBOX","sender":"`+sender+`"}`)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "spam") {
			t.Errorf("%s: err = %v, want an invalid request that names no address", sender, err)
		}
	}
	if e.f.dials.Load() != dials || e.f.reads.Load() != reads {
		t.Error("a refused sender reached the network or the secret")
	}
	e.f.add(t, "INBOX", "alice@example.net", "ok", base)
	if page := list(t, e, "ex", `{"folder":"INBOX","sender":"alice@example.net"}`); len(page.Messages) != 1 {
		t.Errorf("page = %+v", page)
	}
}

func TestExcludeSpecialFoldersRefuseTheirTools(t *testing.T) {
	smtp := func(t *testing.T) *smtpServer { return newSMTPServer(t, true, nil) }
	t.Run("trash", func(t *testing.T) {
		e := excludeEnvironment(t, nil, []string{"folder/Trash"})
		uid := e.f.add(t, "INBOX", "alice@example.net", "x", base)
		validity := validityOf(t, e, "INBOX")
		_, err := e.confirmed("infomaniak.mail.messages.delete", "ex", refArgs("INBOX", uid, validity, ""))
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "Trash") {
			t.Errorf("err = %v, want the invalid request of an unusable trash folder", err)
		}
		if commands(e, moveCommand) != 0 {
			t.Error("a MOVE was sent")
		}
	})
	t.Run("drafts", func(t *testing.T) {
		e := excludeEnvironment(t, nil, []string{"folder/Drafts"})
		uid := e.f.addRaw(t, "INBOX", header(mailbox, "d")+"\r\nbody\r\n", imap.FlagDraft)
		validity := validityOf(t, e, "INBOX")
		for tool, args := range map[string]string{"drafts.create": `{"to":["a@example.net"]}`,
			"drafts.update": refArgs("INBOX", uid, validity, `,"to":["a@example.net"]`),
			"drafts.delete": refArgs("INBOX", uid, validity, "")} {
			_, err := e.confirmed("infomaniak.mail."+tool, "ex", args)
			if !isInvalidRequest(err) || strings.Contains(err.Error(), "Drafts") {
				t.Errorf("%s: err = %v, want an invalid request", tool, err)
			}
		}
		if commands(e, appendCommand)+commands(e, expungeCommand) != 0 {
			t.Error("a draft was written")
		}
	})
	t.Run("sent", func(t *testing.T) {
		e := excludeEnvironment(t, nil, []string{"folder/Sent"})
		server := smtp(t)
		e.f.addRaw(t, "Drafts", header(mailbox, "d")+"\r\nbody\r\n", imap.FlagDraft)
		validity := validityOf(t, e, "Drafts")
		if _, err := e.confirmed("infomaniak.mail.messages.send", "ex", `{"to":["a@example.net"]}`); !isInvalidRequest(err) {
			t.Errorf("messages.send: err = %v", err)
		}
		if _, err := e.confirmed("infomaniak.mail.drafts.send", "ex", refArgs("Drafts", 1, validity, "")); !isInvalidRequest(err) {
			t.Errorf("drafts.send: err = %v", err)
		}
		if server.dials.Load() != 0 {
			t.Error("SMTP was contacted")
		}
	})
	t.Run("own address", func(t *testing.T) {
		e := excludeEnvironment(t, nil, []string{"sender/" + mailbox})
		server := smtp(t)
		uid := e.f.addRaw(t, "Drafts", header(mailbox, "d")+"\r\nbody\r\n", imap.FlagDraft)
		validity := validityOf(t, e, "Drafts")
		for tool, args := range map[string]string{"messages.send": `{"to":["a@example.net"]}`,
			"drafts.send":   refArgs("Drafts", uid, validity, ""),
			"drafts.create": `{"to":["a@example.net"]}`,
			"drafts.update": refArgs("Drafts", uid, validity, `,"to":["a@example.net"]`),
			"drafts.delete": refArgs("Drafts", uid, validity, "")} {
			if _, err := e.confirmed("infomaniak.mail."+tool, "ex", args); err == nil {
				t.Errorf("%s was admitted with the own address excluded", tool)
			}
		}
		if server.dials.Load() != 0 || commands(e, appendCommand)+commands(e, expungeCommand) != 0 {
			t.Error("something was sent or written")
		}
	})
}

// Exclusions never take a tool out of the offer, and a connection without them reads and writes as before.
var (
	requestLine = regexp.MustCompile(`(?m)^T\d+ (?:[A-Z]|UID [A-Z]).*$`)
	fetchItems  = regexp.MustCompile(`(FETCH \S+ \()([^)]*)\)`)
)

// requestsOf keeps the commands the client sent, with the items of a FETCH sorted, since the client lists them
// in no fixed order.
func requestsOf(wire string) string {
	var out []string
	for _, line := range requestLine.FindAllString(wire, -1) {
		if status := strings.Fields(line)[1]; status == "OK" || status == "NO" || status == "BAD" {
			continue
		}
		line = fetchItems.ReplaceAllStringFunc(line, func(m string) string {
			parts := fetchItems.FindStringSubmatch(m)
			items := strings.Fields(parts[2])
			sort.Strings(items)
			return parts[1] + strings.Join(items, " ") + ")"
		})
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func TestExcludeLeavesOfferAndWireUnchanged(t *testing.T) {
	// The package's dialer is shared, so the two environments run one after the other.
	excludes := []string{"folder/Nowhere", "folder/Nowhere/*", "sender/nobody@example.org"}
	run := func(e *environment) {
		uid := e.f.addRaw(t, "INBOX", header("alice@example.net", "one")+"Content-Type: text/plain\r\n\r\nbody\r\n")
		validity := validityOf(t, e, "INBOX")
		if _, err := e.invoke("infomaniak.mail.folders.list", "ex", `{}`); err != nil {
			t.Fatal(err)
		}
		list(t, e, "ex", `{"folder":"INBOX"}`)
		getMessage(t, e, "ex", getArgs("INBOX", uid, validity))
		if _, err := e.confirmed("infomaniak.mail.messages.flag", "ex", refArgs("INBOX", uid, validity, `,"flag":"seen","set":true`)); err != nil {
			t.Fatal(err)
		}
		if _, err := e.confirmed("infomaniak.mail.messages.move", "ex", refArgs("INBOX", uid, validity, `,"destination":"Archive"`)); err != nil {
			t.Fatal(err)
		}
	}
	var wires, offered []string
	for _, exclude := range [][]string{nil, excludes} {
		e := excludeEnvironment(t, nil, exclude)
		tools, err := e.core.Tools(application.SearchRequest{Connection: "ex"})
		if err != nil || len(tools.Tools) != len(allToolIDs) {
			t.Fatalf("tools = %d, %v, want all %d", len(tools.Tools), err, len(allToolIDs))
		}
		offered = append(offered, fmt.Sprint(tools.Tools))
		run(e)
		wires = append(wires, requestsOf(e.f.wire.String()))
	}
	if !strings.Contains(wires[0], "UID MOVE") || wires[0] != wires[1] || offered[0] != offered[1] {
		t.Errorf("a connection with unrelated exclusions differs:\n%s\n---\n%s", wires[0], wires[1])
	}
}

// capitalized upper-cases the first letter of the local part and of every domain label, so a test can spell an
// address in mixed case.
func capitalized(address string) string {
	out := []byte(address)
	for i := range out {
		if i == 0 || out[i-1] == '@' || out[i-1] == '.' {
			out[i] = strings.ToUpper(string(out[i]))[0]
		}
	}
	return string(out)
}
