package infomaniakmail

import (
	"strings"
	"testing"
)

func TestTargetValidation(t *testing.T) {
	long := strings.Repeat("a", 65) + "@example.com"
	cases := []struct {
		name    string
		values  []string
		wantErr bool
	}{
		{"mailbox only", []string{"mailbox/box@example.com"}, false},
		{"mailbox, folders, senders", []string{"mailbox/box@example.com", "folder/INBOX", "folder/Archive/2026", "sender/a@example.net"}, false},
		{"no mailbox", []string{"folder/INBOX"}, true},
		{"two mailboxes", []string{"mailbox/a@example.com", "mailbox/b@example.com"}, true},
		{"empty", nil, true},
		{"unknown kind", []string{"mailbox/box@example.com", "label/x"}, true},
		{"bad mailbox", []string{"mailbox/not-an-address"}, true},
		{"mailbox with display name", []string{"mailbox/Box <box@example.com>"}, true},
		{"mailbox with two at signs", []string{"mailbox/a@b@example.com"}, true},
		{"mailbox with control character", []string{"mailbox/a\r\n@example.com"}, true},
		{"overlong local part", []string{"mailbox/" + long}, true},
		{"duplicate folder", []string{"mailbox/box@example.com", "folder/INBOX", "folder/inbox"}, true},
		{"duplicate sender", []string{"mailbox/box@example.com", "sender/a@example.net", "sender/A@Example.net"}, true},
		{"wildcard star", []string{"mailbox/box@example.com", "folder/*"}, true},
		{"wildcard percent", []string{"mailbox/box@example.com", "folder/Arch%"}, true},
		{"folder control character", []string{"mailbox/box@example.com", "folder/a\x00b"}, true},
		{"folder newline", []string{"mailbox/box@example.com", "folder/a\nb"}, true},
		{"empty folder", []string{"mailbox/box@example.com", "folder/"}, true},
		{"overlong folder", []string{"mailbox/box@example.com", "folder/" + strings.Repeat("f", 256)}, true},
		{"bad sender", []string{"mailbox/box@example.com", "sender/everyone"}, true},
	}
	for _, c := range cases {
		_, err := parseScope(c.values)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: parseScope(%q) = %v, wantErr %t", c.name, c.values, err, c.wantErr)
		}
		if err != nil {
			for _, value := range c.values {
				if _, rest, ok := strings.Cut(value, "/"); ok && len(rest) > 3 && strings.Contains(err.Error(), rest) {
					t.Errorf("%s: error %q quotes the configured value %q", c.name, err, rest)
				}
			}
		}
	}
}

func TestScopeAllowlists(t *testing.T) {
	bound, err := parseScope([]string{"mailbox/box@example.com", "folder/Allowed", "folder/inbox", "sender/Alice@Example.net"})
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{"Allowed": true, "INBOX": true, "Inbox": true, "allowed": false, "Secret": false} {
		if got := bound.allowsFolder(name); got != want {
			t.Errorf("allowsFolder(%q) = %t, want %t", name, got, want)
		}
	}
	for address, want := range map[string]bool{"alice@example.net": true, "ALICE@EXAMPLE.NET": true, "alice@example.net.evil.example": false, "": false} {
		if got := bound.allowsSender(address); got != want {
			t.Errorf("allowsSender(%q) = %t, want %t", address, got, want)
		}
	}
	open, _ := parseScope([]string{"mailbox/box@example.com"})
	if !open.allowsFolder("Anything") || !open.allowsSender("anyone@example.org") {
		t.Error("a scope without lists must admit every folder and sender")
	}
}
