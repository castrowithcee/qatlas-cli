package seatableaccount

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	baseKind = "base"
	// maxBrowseEntries bounds one list; the workspace list has no paging.
	maxBrowseEntries = 500
)

func targetBrowser() capability.TargetBrowser {
	return capability.TargetBrowser{List: listTargets, ResolveURL: resolveTargetURL}
}

type workspacesAnswer struct {
	Workspaces []struct {
		Bases []struct {
			WorkspaceID int64  `json:"workspace_id"`
			Name        string `json:"name"`
		} `json:"table_list"`
	} `json:"workspace_list"`
}

// listTargets lists the bases of all workspaces of the account (SeaTable API reference, "List Workspaces",
// GET /api/v2.1/workspaces/, account token; no paging), never filtered by the connection's target.
func listTargets(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	req config.TargetListRequest) (config.TargetPage, error) {
	const op = "choose targets"
	if req.Kind != baseKind {
		return config.TargetPage{}, invalidRequest("the kind is not one this provider offers")
	}
	if req.Cursor != "" {
		return config.TargetPage{}, invalidRequest("the target cursor is not a page of this provider")
	}
	client, err := openAccount(ctx, resolved, secrets, red, nil)
	if err != nil {
		return config.TargetPage{}, err
	}
	var answer workspacesAnswer
	if err := client.do(ctx, op, http.MethodGet, "/api/v2.1/workspaces/", nil, &answer, ""); err != nil {
		return config.TargetPage{}, err
	}
	var page config.TargetPage
	for _, workspace := range answer.Workspaces {
		for _, base := range workspace.Bases {
			if len(page.Entries) >= maxBrowseEntries {
				return page, nil
			}
			id := strconv.FormatInt(base.WorkspaceID, 10)
			if base.WorkspaceID <= 0 || !validBaseName(base.Name) {
				continue
			}
			target := id + "/" + base.Name
			page.Entries = append(page.Entries, config.TargetEntry{Target: target, Name: browseName(base.Name, target), Kind: baseKind})
		}
	}
	return page, nil
}

// browseName makes a name plain text for the picker: control characters become spaces, an empty name falls
// back to the target.
func browseName(name, fallback string) string {
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return ' '
		}
		return r
	}, name))
	if name == "" {
		return fallback
	}
	return bounded(name)
}

// resolveTargetURL reads the base from its address in the web interface,
// <origin>/workspace/<workspace id>/dtable/<base name>/ (SeaTable help, "Find the workspace ID of a group").
// It counts only on the host of the connection over https and sends no request.
func resolveTargetURL(_ context.Context, _ *config.Resolved, _ *secret.Resolver, _ *redact.Redactor,
	origin, rawURL string) ([]config.TargetEntry, error) {
	if origin == "" {
		origin = cloudOrigin
	}
	service, err := url.Parse(origin)
	parsed, perr := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || perr != nil || service.Host == "" || parsed.Scheme != "https" ||
		!strings.EqualFold(parsed.Host, service.Host) {
		return nil, nil
	}
	segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(segments) != 4 || segments[0] != "workspace" || segments[2] != "dtable" {
		return nil, nil
	}
	name, err := url.PathUnescape(segments[3])
	if err != nil || !validWorkspaceID(segments[1]) || !validBaseName(name) {
		return nil, nil
	}
	target := segments[1] + "/" + name
	return []config.TargetEntry{{Target: target, Name: browseName(name, target), Kind: baseKind}}, nil
}
