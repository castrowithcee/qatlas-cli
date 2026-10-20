package infomaniakmail

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The three target kinds a connection may combine: exactly one mailbox, and optionally allow-lists of
// folders and sender addresses within it.
const (
	mailboxPrefix = "mailbox/"
	folderPrefix  = "folder/"
	senderPrefix  = "sender/"
	treeSuffix    = "/*"

	maxAddressLength    = 254
	maxLocalPartLength  = 64
	maxDomainLength     = 253
	maxLabelLength      = 63
	maxFolderNameLength = 255
	maxFolderTargets    = 50
	maxSenderTargets    = 50
)

// scope is the mailbox, folder, and sender boundary of one connection. The mailbox address is the IMAP
// login name. An empty folder list admits every folder of the mailbox; an empty sender list admits every
// sender.
type scope struct {
	mailbox string
	folders []string // exact names: folder/PATH
	trees   []string // folder/PATH/*: the folder NAME and its subfolders
	senders []string // lower case
}

// unrestricted reports whether no folder target is configured, so every folder of the mailbox is reachable.
func (s scope) unrestricted() bool { return len(s.folders) == 0 && len(s.trees) == 0 }

// allowsExact reports whether name is the folder of a folder/PATH or folder/PATH/* target itself. It needs no
// hierarchy delimiter.
func (s scope) allowsExact(name string) bool {
	if s.unrestricted() {
		return true
	}
	name = normalizeFolder(name)
	for _, allowed := range s.folders {
		if allowed == name {
			return true
		}
	}
	for _, allowed := range s.trees {
		if allowed == name {
			return true
		}
	}
	return false
}

// allowsFolder is the check that needs no network access. It admits a folder that is exactly a target and,
// provisionally, one that begins with the name of a folder/PATH/* target and is longer: whether that is a
// subfolder depends on the server's hierarchy delimiter, so session.folderOK decides it after login. Without a
// folder/PATH/* target it is exact.
func (s scope) allowsFolder(name string) bool {
	if s.allowsExact(name) {
		return true
	}
	name = normalizeFolder(name)
	for _, tree := range s.trees {
		if len(name) > len(tree) && strings.HasPrefix(name, tree) {
			return true
		}
	}
	return false
}

// allowsFolderDelim is the final check: the folder is a target itself, or it begins with the name of a
// folder/PATH/* target followed by the hierarchy delimiter. An empty delimiter (a flat hierarchy) admits only
// the targets themselves.
func (s scope) allowsFolderDelim(name, delim string) bool {
	if s.allowsExact(name) {
		return true
	}
	if delim == "" {
		return false
	}
	name = normalizeFolder(name)
	for _, tree := range s.trees {
		if strings.HasPrefix(name, tree+delim) && len(name) > len(tree)+len(delim) {
			return true
		}
	}
	return false
}

func (s scope) allowsSender(address string) bool {
	if len(s.senders) == 0 {
		return true
	}
	address = strings.ToLower(address)
	for _, allowed := range s.senders {
		if allowed == address {
			return true
		}
	}
	return false
}

// normalizeFolder applies the one case rule IMAP defines: INBOX is case-insensitive, every other name is
// compared exactly.
func normalizeFolder(name string) string {
	if strings.EqualFold(name, "INBOX") {
		return "INBOX"
	}
	return name
}

// scopeOf reads the mailbox, folder, and sender targets of one connection.
func scopeOf(resolved *config.Resolved) (scope, error) {
	values := provider.TargetsOf(resolved)
	return parseScope(values)
}

// parseScope reads the configured targets: exactly one mailbox/ADDRESS, and zero or more folder/PATH and
// sender/ADDRESS entries, none named twice within its own kind. No error ever quotes a configured value.
func parseScope(values []string) (scope, error) {
	var bound scope
	seenFolders, seenTrees, seenSenders := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, raw := range values {
		kind, value, err := parseTarget(strings.TrimSpace(raw))
		if err != nil {
			return scope{}, err
		}
		switch kind {
		case "mailbox":
			if bound.mailbox != "" {
				return scope{}, errors.New("an Infomaniak Mail connection may bind exactly one mailbox/ADDRESS target")
			}
			bound.mailbox = value
		case "folder":
			if seenFolders[value] {
				return scope{}, errors.New("the folder target list names a folder more than once")
			}
			seenFolders[value] = true
			bound.folders = append(bound.folders, value)
		case "tree":
			if seenTrees[value] {
				return scope{}, errors.New("the folder target list names a folder tree more than once")
			}
			seenTrees[value] = true
			bound.trees = append(bound.trees, value)
		case "sender":
			if seenSenders[value] {
				return scope{}, errors.New("the sender target list names an address more than once")
			}
			seenSenders[value] = true
			bound.senders = append(bound.senders, value)
		}
	}
	if bound.mailbox == "" {
		return scope{}, errors.New("an Infomaniak Mail connection needs exactly one mailbox/ADDRESS target")
	}
	if len(bound.folders)+len(bound.trees) > maxFolderTargets {
		return scope{}, errors.New("an Infomaniak Mail connection may list at most 50 folder targets")
	}
	if len(bound.senders) > maxSenderTargets {
		return scope{}, errors.New("an Infomaniak Mail connection may list at most 50 sender targets")
	}
	return bound, nil
}

// validateTarget checks the form of one configured target in isolation.
func validateTarget(raw string) error {
	_, _, err := parseTarget(strings.TrimSpace(raw))
	return err
}

// parseTarget reads one target as mailbox/ADDRESS, folder/PATH, folder/PATH/* (kind tree), or sender/ADDRESS. The mailbox keeps the
// spelling it was configured with, since it is the login name; a sender address is lower-cased for
// comparison; a folder name is normalised only for INBOX.
func parseTarget(raw string) (kind, value string, err error) {
	switch {
	case strings.HasPrefix(raw, mailboxPrefix):
		kind, raw = "mailbox", strings.TrimPrefix(raw, mailboxPrefix)
		if !validAddress(raw) {
			return "", "", errors.New("an Infomaniak Mail mailbox target must be a plain email address")
		}
		return kind, raw, nil
	case strings.HasPrefix(raw, senderPrefix):
		kind, raw = "sender", strings.TrimPrefix(raw, senderPrefix)
		if !validAddress(raw) {
			return "", "", errors.New("an Infomaniak Mail sender target must be a plain email address")
		}
		return kind, strings.ToLower(raw), nil
	case strings.HasPrefix(raw, folderPrefix) && strings.HasSuffix(raw, treeSuffix):
		kind, raw = "tree", strings.TrimSuffix(strings.TrimPrefix(raw, folderPrefix), treeSuffix)
		if !validFolderName(raw) {
			return "", "", errors.New("an Infomaniak Mail folder/PATH/* target must name one folder without " +
				"control characters or the IMAP wildcards * and %")
		}
		return kind, normalizeFolder(raw), nil
	case strings.HasPrefix(raw, folderPrefix):
		kind, raw = "folder", strings.TrimPrefix(raw, folderPrefix)
		if !validFolderName(raw) {
			return "", "", errors.New("an Infomaniak Mail folder target must be a folder name of at most 255 " +
				"bytes without control characters or the IMAP wildcards * and %")
		}
		return kind, normalizeFolder(raw), nil
	}
	return "", "", errors.New("an Infomaniak Mail target must be mailbox/ADDRESS, folder/PATH, folder/PATH/*, or sender/ADDRESS")
}

// validFolderName keeps a folder name to one literal mailbox name: valid UTF-8 of bounded length, no control
// character, and never an IMAP wildcard, so a name can never widen into a pattern.
func validFolderName(name string) bool {
	if name == "" || len(name) > maxFolderNameLength || !utf8.ValidString(name) || strings.TrimSpace(name) != name {
		return false
	}
	for _, r := range name {
		if r == '*' || r == '%' || unicode.IsControl(r) || r == ' ' || r == ' ' || r == utf8.RuneError {
			return false
		}
	}
	return true
}

// validAddress accepts a plain ASCII email address: one at sign, a bounded local part of letters, digits,
// and a few safe punctuation characters without leading, trailing, or doubled dots, and a dotted domain of
// letters, digits, and inner hyphens. Display names, comments, quoting, and internationalised domains are
// not accepted.
func validAddress(address string) bool {
	if len(address) > maxAddressLength {
		return false
	}
	local, domain, ok := strings.Cut(address, "@")
	if !ok || local == "" || len(local) > maxLocalPartLength || domain == "" || len(domain) > maxDomainLength ||
		strings.Contains(domain, "@") {
		return false
	}
	if local[0] == '.' || local[len(local)-1] == '.' || strings.Contains(local, "..") {
		return false
	}
	for i := 0; i < len(local); i++ {
		c := local[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '.', c == '_', c == '%', c == '+', c == '-', c == '=':
		default:
			return false
		}
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if label == "" || len(label) > maxLabelLength || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
