package infomaniakmail

import (
	"context"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// browsePageSize is the number of entries of one page of choices. IMAP answers the folder list as a whole,
	// so the provider pages it itself.
	browsePageSize = 50
	// maxBrowsePage bounds a cursor well above any real listing.
	maxBrowsePage = 100000
	// maxChoiceName and maxChoicePath keep an entry well inside the bounds the caller applies to its texts.
	maxChoiceName = 200
	maxChoicePath = 250

	treeLabel = " (with subfolders)"
)

// ListTargets reads one page of choices. The mailbox is the credential's own and cannot be listed: its level
// offers only the connection's mailbox/ADDRESS target, without any request. The folders are the whole IMAP
// folder list of that mailbox, not filtered by the connection's folder or sender targets, behind a login with
// the mailbox of the connection and no other address. The connection may still be a draft without folders.
func ListTargets(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	req config.TargetListRequest) (config.TargetPage, error) {
	if resolved == nil {
		return config.TargetPage{}, providerError("open", "no connection was selected")
	}
	bound, hasBound := boundMailbox(resolved)
	switch req.Kind {
	case "mailbox":
		if req.Parent != "" || req.Cursor != "" {
			return config.TargetPage{}, invalidRequest("the kind has no parent and no further pages")
		}
		if !hasBound {
			return config.TargetPage{Entries: []config.TargetEntry{}}, nil
		}
		return config.TargetPage{Entries: []config.TargetEntry{{
			Target: mailboxPrefix + bound, Name: bound, Kind: "mailbox", HasChildren: true,
		}}}, nil
	case "folder":
	default:
		return config.TargetPage{}, invalidRequest("the kind is not one this provider offers")
	}
	page := 1
	if req.Cursor != "" {
		n, err := strconv.Atoi(req.Cursor)
		if err != nil || n < 1 || n > maxBrowsePage || strconv.Itoa(n) != req.Cursor {
			return config.TargetPage{}, invalidRequest("the target cursor is not a page of this provider")
		}
		page = n
	}
	kind, address, err := parseTarget(strings.TrimSpace(req.Parent))
	if err != nil || kind != "mailbox" {
		return config.TargetPage{}, invalidRequest("the parent of a folder must be mailbox/ADDRESS")
	}
	// The password is never checked against another address than the one the connection holds.
	if !hasBound || address != bound {
		return config.TargetPage{}, invalidRequest("the parent mailbox is not the mailbox of this connection")
	}
	client, err := newClient(ctx, scope{mailbox: bound}, resolved, secrets, red)
	if err != nil {
		return config.TargetPage{}, err
	}
	folders, err := client.ListFolders(ctx)
	if err != nil {
		return config.TargetPage{}, err
	}
	entries := folderEntries(folders.Folders)
	start := (page - 1) * browsePageSize
	if start >= len(entries) {
		return config.TargetPage{Entries: []config.TargetEntry{}}, nil
	}
	end := min(start+browsePageSize, len(entries))
	result := config.TargetPage{Entries: entries[start:end]}
	if end < len(entries) {
		result.Next = strconv.Itoa(page + 1)
	}
	return result, nil
}

// ResolveTargetURL resolves nothing: a mailbox has no web address that names a folder.
func ResolveTargetURL(_ context.Context, _ *config.Resolved, _ *secret.Resolver, _ *redact.Redactor,
	_, _ string) ([]config.TargetEntry, error) {
	return nil, nil
}

// boundMailbox returns the one mailbox/ADDRESS target of the connection. A connection with none, with
// several, or with a malformed one has no usable mailbox here; the other targets are not looked at.
func boundMailbox(resolved *config.Resolved) (string, bool) {
	var found string
	for _, raw := range provider.TargetsOf(resolved) {
		kind, value, err := parseTarget(strings.TrimSpace(raw))
		if err != nil || kind != "mailbox" {
			continue
		}
		if found != "" {
			return "", false
		}
		found = value
	}
	return found, found != ""
}

// folderEntries turns the sorted folder list into choices: the exact folder/PATH of every folder, directly
// followed by folder/PATH/* for a folder with subfolders. A folder has subfolders when its LIST answer says
// \HasChildren or another listed folder begins with its name and the hierarchy delimiter; a flat hierarchy
// (no delimiter) has none. The name is the last element below the delimiter and the path the elements above
// it, as /A/B, or / at the top.
func folderEntries(folders []Folder) []config.TargetEntry {
	entries := make([]config.TargetEntry, 0, len(folders))
	for _, f := range folders {
		if !validFolderName(f.Name) {
			continue
		}
		name, path := f.Name, "/"
		if f.Delimiter != "" {
			if elements := strings.Split(f.Name, f.Delimiter); len(elements) > 1 && elements[len(elements)-1] != "" {
				name = elements[len(elements)-1]
				path = "/" + strings.Join(elements[:len(elements)-1], "/")
			}
		}
		name, path = cutBytes(name, maxChoiceName), cutBytes(path, maxChoicePath)
		entries = append(entries, config.TargetEntry{Target: folderPrefix + f.Name, Name: name, Kind: "folder", Path: path})
		if hasSubfolders(f, folders) {
			entries = append(entries, config.TargetEntry{
				Target: folderPrefix + f.Name + treeSuffix, Name: name + treeLabel, Kind: "folder", Path: path,
			})
		}
	}
	return entries
}

func hasSubfolders(f Folder, all []Folder) bool {
	if f.Delimiter == "" {
		return false
	}
	for _, attr := range f.Attributes {
		if strings.EqualFold(attr, `\HasChildren`) {
			return true
		}
	}
	for _, other := range all {
		if len(other.Name) > len(f.Name)+len(f.Delimiter) && strings.HasPrefix(other.Name, f.Name+f.Delimiter) {
			return true
		}
	}
	return false
}

// cutBytes keeps at most n bytes of s without splitting a character.
func cutBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
