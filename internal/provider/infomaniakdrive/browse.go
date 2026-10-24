package infomaniakdrive

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Hosts of the web interface whose URLs name a drive, and optionally an account.
const (
	webHostDrive  = "kdrive.infomaniak.com"
	webHostKSuite = "ksuite.infomaniak.com"
)

// openUnbound returns a client for choosing targets: the connection may still be a draft without an account.
func openUnbound(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor) (*Client, error) {
	if resolved == nil {
		return nil, providerError("open", "no connection was selected")
	}
	return build(ctx, resolved, secrets, red, nil, scope{})
}

// ListTargets reads one page of accounts or of the drives of one account. A drive without a parent is read
// from the connection's single account target. Every read counts against the token's request budget like any
// other. Neither level filters by the connection's current targets.
func ListTargets(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	req config.TargetListRequest) (config.TargetPage, error) {
	if req.Kind == "files" {
		return listFileTargets(ctx, resolved, secrets, red, req)
	}
	page := 1
	if req.Cursor != "" {
		if !validPositiveID(req.Cursor) {
			return config.TargetPage{}, invalidRequest("the target cursor is not a page of this provider")
		}
		page, _ = strconv.Atoi(req.Cursor)
	}
	var parent int64
	switch req.Kind {
	case "account":
	case "drive":
		if strings.TrimSpace(req.Parent) == "" {
			id, err := boundAccount(resolved)
			if err != nil {
				return config.TargetPage{}, err
			}
			parent = id
			break
		}
		kind, id, err := parseTarget(strings.TrimSpace(req.Parent))
		if err != nil || kind != "account" {
			return config.TargetPage{}, invalidRequest("the parent of a drive must be account/ACCOUNT_ID")
		}
		parent = id
	default:
		return config.TargetPage{}, invalidRequest("the kind is not one this provider offers")
	}
	client, err := openUnbound(ctx, resolved, secrets, red)
	if err != nil {
		return config.TargetPage{}, err
	}
	if parent != 0 {
		return client.listDriveTargets(ctx, parent, page)
	}
	return client.listAccountTargets(ctx, page)
}

// boundAccount is the one account target of the connection, the start of a drive pick without a parent.
func boundAccount(resolved *config.Resolved) (int64, error) {
	const need = "listing drives without an account needs exactly one account/ACCOUNT_ID target on the connection"
	if resolved == nil {
		return 0, invalidRequest(need)
	}
	var found int64
	count := 0
	for _, target := range resolved.Targets {
		kind, id, err := parseTarget(strings.TrimSpace(target))
		if err != nil || kind != "account" {
			continue
		}
		found = id
		count++
	}
	if count != 1 {
		return 0, invalidRequest(need)
	}
	return found, nil
}

// fileTargetsPerPage is the page size of one folder read while choosing a file or folder.
const fileTargetsPerPage = 50

// maxFileCursorLength bounds the opaque folder cursor taken from a request before it is sent on.
const maxFileCursorLength = 1024

// listFileTargets reads one page of the folders and files directly inside the drive's root (parent
// drive/DRIVE_ID) or inside one folder (parent drive/DRIVE_ID/files/FILE_ID). The cursor is Infomaniak's own
// folder cursor. The path of an entry is the folder's own path, read once per page below a folder.
func listFileTargets(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	req config.TargetListRequest) (config.TargetPage, error) {
	const badParent = "the parent of a file or folder must be drive/DRIVE_ID or drive/DRIVE_ID/files/FILE_ID"
	parent, err := parseAnyTarget(strings.TrimSpace(req.Parent))
	if err != nil || (parent.kind != "drive" && parent.kind != "files") {
		return config.TargetPage{}, invalidRequest(badParent)
	}
	folderID := int64(rootFileID)
	if parent.kind == "files" {
		folderID = parent.fileID
	}
	if len(req.Cursor) > maxFileCursorLength || strings.ContainsFunc(req.Cursor, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return config.TargetPage{}, invalidRequest("the target cursor is not a page of this provider")
	}
	client, err := openUnbound(ctx, resolved, secrets, red)
	if err != nil {
		return config.TargetPage{}, err
	}
	place := "/"
	if folderID != rootFileID {
		item, err := client.readWithParents(ctx, "list file targets", parent.id, folderID)
		if err != nil {
			return config.TargetPage{}, err
		}
		place = folderPath(folderID, item)
	}
	folder, err := client.ListFolder(ctx, parent.id, folderID, req.Cursor, fileTargetsPerPage, nil)
	if err != nil {
		return config.TargetPage{}, err
	}
	entries := make([]config.TargetEntry, 0, len(folder.Entries))
	for _, e := range folder.Entries {
		if e.ID <= 0 {
			continue
		}
		entries = append(entries, config.TargetEntry{
			Target: drivePrefix + strconv.FormatInt(parent.id, 10) + "/files/" + strconv.FormatInt(e.ID, 10),
			Name:   e.Name, Kind: "files", Path: place, HasChildren: e.Type == "folder",
		})
	}
	page := config.TargetPage{Entries: entries}
	if folder.HasMore {
		page.Next = folder.Cursor
	}
	return page, nil
}

// nextCursor is the page after the one read, empty on the last page.
func nextCursor(meta paginationJSON, page int) string {
	if meta.Pages > page {
		return strconv.Itoa(page + 1)
	}
	return ""
}

func (c *Client) listDriveTargets(ctx context.Context, accountID int64, page int) (config.TargetPage, error) {
	drives, meta, err := c.readDrives(ctx, "list drive targets", accountID, page)
	if err != nil {
		return config.TargetPage{}, err
	}
	entries := make([]config.TargetEntry, 0, len(drives))
	for _, d := range drives {
		if d.AccountID != accountID || d.ID <= 0 {
			continue
		}
		entries = append(entries, config.TargetEntry{
			Target: drivePrefix + strconv.FormatInt(d.ID, 10), Name: bounded(d.Name), Kind: "drive",
		})
	}
	return config.TargetPage{Entries: entries, Next: nextCursor(meta, page)}, nil
}

// accountJSON is the subset of an Infomaniak account this provider reads.
type accountJSON struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// listAccountTargets reads one page of the accounts the token may see. The drives listing needs an account_id
// and cannot name the accounts, so a token that may not read the accounts endpoint fails with the classified
// error: the person types the ID or a kDrive URL instead. The endpoint's own pagination is followed only when
// its answer reports it; page 1 sends no query because the endpoint's parameters are not relied on.
func (c *Client) listAccountTargets(ctx context.Context, page int) (config.TargetPage, error) {
	var query url.Values
	if page > 1 {
		query = url.Values{"page": {strconv.Itoa(page)}}
	}
	var accounts []accountJSON
	var meta paginationJSON
	if err := c.doInto(ctx, "list account targets", "/1/accounts", query, &accounts, &meta); err != nil {
		return config.TargetPage{}, err
	}
	entries := make([]config.TargetEntry, 0, len(accounts))
	for _, a := range accounts {
		if a.ID <= 0 {
			continue
		}
		text := strconv.FormatInt(a.ID, 10)
		name := bounded(a.Name)
		if name == "" {
			name = text
		}
		entries = append(entries, config.TargetEntry{
			Target: accountPrefix + text, Name: name, Kind: "account", HasChildren: true,
		})
	}
	return config.TargetPage{Entries: entries, Next: nextCursor(meta, page)}, nil
}

// ResolveTargetURL reads the drive, and on a K Suite URL the account before it, from a URL of the web
// interface. It never sends a request, so a person's URL names no more than the IDs it holds, and the name
// of an entry is its ID. A file or folder in the URL (…/files/FILE_ID, or the file of …/files/FOLDER_ID/preview/…)
// is added when its form is unambiguous.
func ResolveTargetURL(_ context.Context, _ *config.Resolved, _ *secret.Resolver, _ *redact.Redactor,
	_, rawURL string) ([]config.TargetEntry, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, nil
	}
	host := strings.ToLower(u.Hostname())
	if host != webHostDrive && host != webHostKSuite {
		return nil, nil
	}
	segments := strings.Split(u.Path, "/")
	for i, segment := range segments {
		if segment != "drive" || i+1 >= len(segments) || !validPositiveID(segments[i+1]) {
			continue
		}
		var entries []config.TargetEntry
		if host == webHostKSuite {
			for _, before := range segments[:i] {
				if validPositiveID(before) {
					entries = append(entries, config.TargetEntry{
						Target: accountPrefix + before, Name: before, Kind: "account", HasChildren: true,
					})
					break
				}
			}
		}
		entries = append(entries, config.TargetEntry{
			Target: drivePrefix + segments[i+1], Name: segments[i+1], Kind: "drive",
		})
		if file := fileOfURL(segments[i+2:]); file != "" {
			entries = append(entries, config.TargetEntry{
				Target: drivePrefix + segments[i+1] + "/files/" + file, Name: file, Kind: "files",
			})
		}
		return entries, nil
	}
	return nil, nil
}

// fileOfURL reads the file or folder from the path after the drive: files/ID names it, and
// files/FOLDER/preview/…/FILE names the previewed file when exactly one numeric segment follows preview.
// Anything else names none.
func fileOfURL(rest []string) string {
	if len(rest) < 2 || rest[0] != "files" || !validPositiveID(rest[1]) {
		return ""
	}
	if len(rest) == 2 || (len(rest) == 3 && rest[2] == "") {
		return rest[1]
	}
	if rest[2] != "preview" {
		return ""
	}
	file := ""
	for _, segment := range rest[3:] {
		if segment == "" {
			continue
		}
		if !validPositiveID(segment) {
			if segment[0] >= '0' && segment[0] <= '9' {
				return ""
			}
			continue
		}
		if file != "" {
			return ""
		}
		file = segment
	}
	return file
}

// maxPathLength bounds the path of an entry.
const maxPathLength = 1024

// folderPath builds /Name/Name from the names of a folder read with its parents, root left out. The order of
// the parents is not relied on: the chain is followed through parent_id from the folder up to the root. A
// chain that does not reach the root, or loops, gives no path.
func folderPath(folderID int64, item *fileJSON) string {
	if item.ID != folderID || item.Parents == nil {
		return ""
	}
	byID := make(map[int64]fileJSON, len(*item.Parents))
	for _, p := range *item.Parents {
		byID[p.ID] = p
	}
	names := []string{boundedText(item.Name, maxPathLength)}
	for cur := item.ParentID; cur != rootFileID; {
		p, ok := byID[cur]
		if !ok || len(names) > len(byID) {
			return ""
		}
		names = append(names, boundedText(p.Name, maxPathLength))
		cur = p.ParentID
	}
	var path strings.Builder
	for i := len(names) - 1; i >= 0; i-- {
		path.WriteString("/" + names[i])
	}
	return boundedText(path.String(), maxPathLength)
}
