package infomaniakdav

import (
	"context"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// ListTargets reads the calendars or address books the credential reaches. Both kinds sit at the top, the
// connection's current targets do not filter, and the provider has no paging, so there is never a cursor.
// The connection may still be a draft without targets.
func ListTargets(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	req config.TargetListRequest) (config.TargetPage, error) {
	var calendars bool
	var prefix string
	switch req.Kind {
	case "calendar":
		calendars, prefix = true, calendarPrefix
	case "addressbook":
		prefix = addressbookPrefix
	default:
		return config.TargetPage{}, &provider.InvalidRequestError{Message: "the kind is not one this provider offers"}
	}
	if req.Parent != "" || req.Cursor != "" {
		return config.TargetPage{}, &provider.InvalidRequestError{Message: "the kind has no parent and no further pages"}
	}
	if resolved == nil {
		return config.TargetPage{}, providerError("open", "no connection was selected")
	}
	client, err := openWith(ctx, resolved, secrets, red, scope{})
	if err != nil {
		return config.TargetPage{}, err
	}
	page, err := client.collect(ctx, calendars, func(id string) bool { return validCollectionID(id) })
	if err != nil {
		return config.TargetPage{}, err
	}
	entries := make([]config.TargetEntry, 0, len(page.Collections))
	for _, c := range page.Collections {
		name := c.Name
		if name == "" {
			name = c.ID
		}
		entries = append(entries, config.TargetEntry{Target: prefix + c.ID, Name: name, Kind: req.Kind})
	}
	return config.TargetPage{Entries: entries}, nil
}

// ResolveTargetURL resolves nothing: Infomaniak URLs carry no usable collection IDs.
func ResolveTargetURL(_ context.Context, _ *config.Resolved, _ *secret.Resolver, _ *redact.Redactor,
	_, _ string) ([]config.TargetEntry, error) {
	return nil, nil
}
