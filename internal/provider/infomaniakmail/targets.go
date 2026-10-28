package infomaniakmail

import (
	"errors"
	"slices"
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
	childSuffix   = "/*"

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
// sender. A subfolder is a folder whose name begins with the parent's name and the server's hierarchy
// delimiter.
type scope struct {
	mailbox  string
	folders  []string // folder/PATH: the folder PATH and its subfolders
	children []string // folder/PATH/*: the subfolders of PATH, not PATH itself
	senders  []string // lower case

	// Exclusions of the connection, in the same forms. They are never targets: they only take folders and
	// senders out of what the targets reach, and an exclusion always wins.
	exFolders  []string
	exChildren []string
	exSenders  []string // lower case
}

// unrestricted reports whether no folder target is configured, so every folder of the mailbox is reachable.
func (s scope) unrestricted() bool { return len(s.folders) == 0 && len(s.children) == 0 }

// allowsExact reports whether name is the folder of a folder/PATH target itself. It needs no hierarchy
// delimiter.
func (s scope) allowsExact(name string) bool {
	return s.unrestricted() || slices.Contains(s.folders, normalizeFolder(name))
}

// allowsFolder is the check that needs no network access. It admits a folder that is exactly a folder/PATH
// target and, provisionally, one that begins with the name of a folder target and is longer: whether that is
// a subfolder depends on the server's hierarchy delimiter, so session.folderOK decides it after login.
func (s scope) allowsFolder(name string) bool {
	if s.excludesFolder(name, "") {
		return false
	}
	if s.allowsExact(name) {
		return true
	}
	name = normalizeFolder(name)
	return slices.ContainsFunc(s.folders, longerPrefix(name)) || slices.ContainsFunc(s.children, longerPrefix(name))
}

// allowsFolderDelim is the final check: the folder is a folder/PATH target itself, or a subfolder of a
// folder/PATH or folder/PATH/* target. An empty delimiter (a flat hierarchy) has no subfolders, so it admits
// only the folder/PATH targets themselves.
func (s scope) allowsFolderDelim(name, delim string) bool {
	if s.excludesFolder(name, delim) {
		return false
	}
	if s.allowsExact(name) {
		return true
	}
	name = normalizeFolder(name)
	return slices.ContainsFunc(s.folders, parentOf(name, delim)) || slices.ContainsFunc(s.children, parentOf(name, delim))
}

// excludesFolder reports whether an exclusion covers the folder: a folder/PATH naming it, or, with the
// hierarchy delimiter, a folder/PATH or folder/PATH/* naming one of its parents. Without a delimiter only the
// folder/PATH exclusions that name the folder itself apply. An exclusion matches a leading INBOX in any case,
// so it fails closed on a server that treats the parent INBOX case-insensitively.
func (s scope) excludesFolder(name, delim string) bool {
	name = inboxFolded(name)
	if slices.ContainsFunc(s.exFolders, func(ex string) bool { return inboxFolded(ex) == name }) {
		return true
	}
	under := func(ex string) bool { return parentOf(name, delim)(inboxFolded(ex)) }
	return slices.ContainsFunc(s.exFolders, under) || slices.ContainsFunc(s.exChildren, under)
}

// mayBeExcluded reports whether the folder is longer than and begins with the name of a folder exclusion,
// which makes it a subfolder of it depending on the server's delimiter.
func (s scope) mayBeExcluded(name string) bool {
	name = inboxFolded(name)
	under := func(ex string) bool { return longerPrefix(name)(inboxFolded(ex)) }
	return slices.ContainsFunc(s.exFolders, under) || slices.ContainsFunc(s.exChildren, under)
}

// hasFolderExcludes reports whether any folder exclusion is configured.
func (s scope) hasFolderExcludes() bool { return len(s.exFolders)+len(s.exChildren) > 0 }

// longerPrefix returns a test whether name is longer than and begins with a given folder name.
func longerPrefix(name string) func(string) bool {
	return func(parent string) bool { return len(name) > len(parent) && strings.HasPrefix(name, parent) }
}

// parentOf returns a test whether a given folder name is a parent of name under the hierarchy delimiter
// delim. An empty delimiter has no parents.
func parentOf(name, delim string) func(string) bool {
	return func(parent string) bool {
		return delim != "" && len(name) > len(parent)+len(delim) && strings.HasPrefix(name, parent+delim)
	}
}

// inboxFolded spells a leading INBOX in upper case, whatever follows it.
func inboxFolded(name string) string {
	if len(name) >= len("INBOX") && strings.EqualFold(name[:len("INBOX")], "INBOX") {
		return "INBOX" + name[len("INBOX"):]
	}
	return name
}

// allowsSender reports whether an address is admitted: not excluded, and on the sender list when there is one.
func (s scope) allowsSender(address string) bool {
	address = strings.ToLower(address)
	if slices.Contains(s.exSenders, address) {
		return false
	}
	if len(s.senders) == 0 {
		return true
	}
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
	return parseScopeExcluding(provider.TargetsOf(resolved), provider.ExcludesOf(resolved))
}

// parseScope reads the configured targets without exclusions.
func parseScope(values []string) (scope, error) { return parseScopeExcluding(values, nil) }

// parseScopeExcluding reads the configured targets: exactly one mailbox/ADDRESS, and zero or more folder/PATH
// and sender/ADDRESS entries, none named twice within its own kind. An exclusion is a folder/PATH,
// folder/PATH/*, or sender/ADDRESS, named once and never equal to a target. It does not rely on the core
// validation, so an invalid exclusion fails closed here too. No error ever quotes a configured value.
func parseScopeExcluding(values, excludes []string) (scope, error) {
	var bound scope
	seenFolders, seenChildren, seenSenders := map[string]bool{}, map[string]bool{}, map[string]bool{}
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
		case "children":
			if seenChildren[value] {
				return scope{}, errors.New("the folder target list names the subfolders of a folder more than once")
			}
			seenChildren[value] = true
			bound.children = append(bound.children, value)
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
	if len(bound.folders)+len(bound.children) > maxFolderTargets {
		return scope{}, errors.New("an Infomaniak Mail connection may list at most 50 folder targets")
	}
	if len(bound.senders) > maxSenderTargets {
		return scope{}, errors.New("an Infomaniak Mail connection may list at most 50 sender targets")
	}
	return bound.withExcludes(excludes)
}

// withExcludes reads the exclusions of the connection into the scope.
func (s scope) withExcludes(excludes []string) (scope, error) {
	errInvalid := errors.New("an Infomaniak Mail exclusion must be folder/PATH, folder/PATH/*, or sender/ADDRESS, " +
		"named once and different from every target")
	named := func(list []string, value string) bool { return slices.Contains(list, value) }
	for _, raw := range excludes {
		kind, value, err := parseTarget(strings.TrimSpace(raw))
		if err != nil {
			return scope{}, errInvalid
		}
		switch kind {
		case "folder":
			if named(s.exFolders, value) || named(s.folders, value) {
				return scope{}, errInvalid
			}
			s.exFolders = append(s.exFolders, value)
		case "children":
			if named(s.exChildren, value) || named(s.children, value) {
				return scope{}, errInvalid
			}
			s.exChildren = append(s.exChildren, value)
		case "sender":
			if named(s.exSenders, value) || named(s.senders, value) {
				return scope{}, errInvalid
			}
			s.exSenders = append(s.exSenders, value)
		default:
			return scope{}, errInvalid
		}
	}
	return s, nil
}

// validateTarget checks the form of one configured target in isolation.
func validateTarget(raw string) error {
	_, _, err := parseTarget(strings.TrimSpace(raw))
	return err
}

// parseTarget reads one target as mailbox/ADDRESS, folder/PATH, folder/PATH/* (kind children), or
// sender/ADDRESS. The mailbox keeps the spelling it was configured with, since it is the login name; a sender
// address is lower-cased for comparison; a folder name is normalised only for INBOX.
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
	case strings.HasPrefix(raw, folderPrefix) && strings.HasSuffix(raw, childSuffix):
		kind, raw = "children", strings.TrimSuffix(strings.TrimPrefix(raw, folderPrefix), childSuffix)
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
