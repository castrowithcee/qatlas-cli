package infomaniakdrive

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
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

// ListTargets reads one page of accounts or of the drives of one account. Every read counts against the
// token's request budget like any other. Neither level filters by the connection's current targets.
func ListTargets(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	req config.TargetListRequest) (config.TargetPage, error) {
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

// listAccountTargets groups one page of the token's drives by account. Infomaniak has no accounts listing
// that is bound to kDrive, so an account appears once per page on which it holds a drive. Names come from
// the accounts endpoint; a token that may not read it, or does not show it, still gets the accounts by ID. Any
// other failure of that read is a failure of the list, not something to guess around.
func (c *Client) listAccountTargets(ctx context.Context, page int) (config.TargetPage, error) {
	drives, meta, err := c.readDrives(ctx, "list account targets", 0, page)
	if err != nil {
		return config.TargetPage{}, err
	}
	var ids []int64
	seen := map[int64]bool{}
	for _, d := range drives {
		if d.AccountID <= 0 || seen[d.AccountID] {
			continue
		}
		seen[d.AccountID] = true
		ids = append(ids, d.AccountID)
	}
	names := map[int64]string{}
	if len(ids) > 0 {
		var accounts []accountJSON
		err := c.do(ctx, "list account names", "/1/accounts", nil, &accounts)
		var providerErr *provider.Error
		switch {
		case err == nil:
			for _, a := range accounts {
				names[a.ID] = bounded(a.Name)
			}
		case errors.As(err, &providerErr) &&
			(providerErr.Class == provider.ClassPermission || providerErr.Class == provider.ClassNotFound):
		default:
			return config.TargetPage{}, err
		}
	}
	entries := make([]config.TargetEntry, 0, len(ids))
	for _, id := range ids {
		text := strconv.FormatInt(id, 10)
		name := names[id]
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
// of an entry is its ID. Files and folders in the URL are ignored.
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
		return append(entries, config.TargetEntry{
			Target: drivePrefix + segments[i+1], Name: segments[i+1], Kind: "drive",
		}), nil
	}
	return nil, nil
}
