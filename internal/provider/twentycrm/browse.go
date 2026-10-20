package twentycrm

import (
	"context"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const browsePageSize = 100

// browseList offers the objects of the workspace from one page of the metadata list, the same read as
// meta-objects.list. The connection may be bound already; the read ignores its targets, so the choice
// reaches beyond them. Twenty's metadata API has no search, so the surface filters the loaded pages.
func browseList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	req config.TargetListRequest) (config.TargetPage, error) {
	if req.Kind != "object" || req.Parent != "" {
		return config.TargetPage{}, providerError("choose targets", "Twenty offers only its objects")
	}
	if resolved == nil {
		return config.TargetPage{}, providerError("choose targets", "no connection was selected")
	}
	unbound := *resolved
	unbound.Target, unbound.Targets = "", nil
	client, err := Open(ctx, &unbound, secrets, red)
	if err != nil {
		return config.TargetPage{}, err
	}
	list, err := client.ListMetaObjects(ctx, resolved.Name, browsePageSize, req.Cursor)
	if err != nil {
		return config.TargetPage{}, err
	}
	page := config.TargetPage{Next: list.NextCursor}
	for _, object := range list.Objects {
		target := objectPrefix + object.NameSingular
		if validateTarget(target) != nil {
			continue
		}
		name := object.LabelSingular
		if name == "" {
			name = object.NameSingular
		}
		page.Entries = append(page.Entries, config.TargetEntry{Target: target, Name: name, Kind: "object"})
	}
	return page, nil
}

// browseResolveURL resolves nothing: the web host of a workspace is not derivable from the API origin.
func browseResolveURL(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, string, string) ([]config.TargetEntry, error) {
	return nil, nil
}
