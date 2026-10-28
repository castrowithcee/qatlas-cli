package infomaniakmail

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// olderFile is a configuration of schema 3, where folder/PATH meant exactly that folder and folder/PATH/* the
// folder with its subfolders.
const olderFile = `version: 3
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
  exact:
    service: mail
    credential: mail-secret
    targets: [mailbox/user@example.net, folder/Kunde, sender/a@example.net]
    exclude: [sender/spam@example.org]
    target_labels:
      folder/Kunde: Customer
      sender/spam@example.org: Spam
  tree:
    service: mail
    credential: mail-secret
    targets: [mailbox/user@example.net, folder/Archive/*, folder/INBOX]
    target_labels:
      folder/Archive/*: Archive
  plain:
    service: mail
    credential: mail-secret
    targets: [mailbox/user@example.net, sender/a@example.net]
`

func TestMigrationRewritesFolderTargetsWithTheSameReach(t *testing.T) {
	cfg, err := config.Decode(strings.NewReader(olderFile), registry(t))
	if err != nil {
		t.Fatalf("Decode() = %v", err)
	}
	exact := cfg.Connections["exact"]
	if !slices.Equal(exact.Targets, []string{"mailbox/user@example.net", "folder/Kunde", "sender/a@example.net"}) ||
		!slices.Equal(exact.Exclude, []string{"sender/spam@example.org", "folder/Kunde/*"}) ||
		!reflect.DeepEqual(exact.TargetLabels, map[string]string{"folder/Kunde": "Customer", "sender/spam@example.org": "Spam"}) {
		t.Errorf("exact = %+v, want the folder with a new exclusion of its subfolders that has no label", exact)
	}
	tree := cfg.Connections["tree"]
	if !slices.Equal(tree.Targets, []string{"mailbox/user@example.net", "folder/Archive", "folder/INBOX"}) ||
		!slices.Equal(tree.Exclude, []string{"folder/INBOX/*"}) ||
		!reflect.DeepEqual(tree.TargetLabels, map[string]string{"folder/Archive": "Archive"}) {
		t.Errorf("tree = %+v, want the folder with its subfolders and the label moved along", tree)
	}
	if plain := cfg.Connections["plain"]; plain.Exclude != nil || plain.TargetsOutdated {
		t.Errorf("plain = %+v, want it unchanged", plain)
	}
	if got := cfg.RetargetedConnections(); !slices.Equal(got, []string{"exact", "tree"}) {
		t.Errorf("RetargetedConnections() = %v", got)
	}

	// The rewritten connections reach what they reached before.
	for name, folders := range map[string]map[string]bool{
		"exact": {"Kunde": true, "Kunde/A": false, "Kunden": false},
		"tree":  {"Archive": true, "Archive/2026": true, "INBOX": true, "INBOX/Sub": false},
	} {
		c := cfg.Connections[name]
		bound, err := parseScopeExcluding(c.Targets, c.Exclude)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for folder, want := range folders {
			if got := bound.allowsFolderDelim(folder, "/"); got != want {
				t.Errorf("%s: %q = %t, want %t", name, folder, got, want)
			}
		}
	}
}

// A connection whose former reach no current form keeps stays as it was, and its problem sits at its target
// line without naming a folder.
func TestMigrationRefusesAFolderBelowAnExactOne(t *testing.T) {
	file := olderFile + `  mixed:
    service: mail
    credential: mail-secret
    targets: [mailbox/user@example.net, folder/Kunde, folder/Kunde/A]
    target_labels:
      folder/Kunde: Customer
`
	cfg, err := config.Decode(strings.NewReader(file), registry(t))
	if err != nil {
		t.Fatalf("Decode() = %v, want the file to load", err)
	}
	err = cfg.Validate()
	problems := config.Problems(err)
	if len(problems) != 1 || problems[0].Code != config.CodeTargetsOutdated ||
		problems[0].Location() != "connections.mixed.targets" {
		t.Fatalf("problems = %+v, want the outdated targets of mixed alone", problems)
	}
	if text := err.Error() + problems[0].Hint(); strings.Contains(text, "Kunde") {
		t.Errorf("the problem names a folder: %s", text)
	}
}

func TestMigrateTargetsRefusals(t *testing.T) {
	const box = "mailbox/user@example.net"
	for name, tc := range map[string]struct {
		targets, exclude []string
		ok               bool
	}{
		"subfolder of an exact folder":       {[]string{box, "folder/Kunde", "folder/Kunde/A"}, nil, false},
		"subfolders of an exact folder":      {[]string{box, "folder/Kunde", "folder/Kunde/A/*"}, nil, false},
		"exact folder below a former tree":   {[]string{box, "folder/Kunde/*", "folder/Kunde/A"}, nil, false},
		"one folder both ways":               {[]string{box, "folder/Kunde", "folder/Kunde/*"}, nil, false},
		"name that begins with an exact one": {[]string{box, "folder/Kunde", "folder/Kunden"}, nil, false},
		"INBOX in another case":              {[]string{box, "folder/INBOX", "folder/inbox/Sub"}, nil, false},
		"folder exclusion":                   {[]string{box}, []string{"folder/Spam"}, false},
		"folder exclusion of subfolders":     {[]string{box, "folder/Kunde/*"}, []string{"folder/Kunde/A/*"}, false},
		"two former trees":                   {[]string{box, "folder/Kunde/*", "folder/Kunde/A/*"}, nil, true},
		"exact folders side by side":         {[]string{box, "folder/Kunde", "folder/Archive"}, nil, true},
		"exact name that begins with a tree": {[]string{box, "folder/Kunden", "folder/Kunde/*"}, nil, false},
		"tree and exact folder apart":        {[]string{box, "folder/Kunde/*", "folder/Archive"}, nil, true},
		"sender exclusion":                   {[]string{box, "folder/Kunde"}, []string{"sender/a@example.net"}, true},
	} {
		got, ok := migrateTargets(tc.targets, tc.exclude)
		if ok != tc.ok {
			t.Errorf("%s: ok = %t, want %t (%+v)", name, ok, tc.ok, got)
		}
		if ok && (len(got.Targets) != len(tc.targets) || !slices.Equal(got.Exclude[:len(tc.exclude)], tc.exclude)) {
			t.Errorf("%s: %+v does not line up with the former entries", name, got)
		}
	}
}
