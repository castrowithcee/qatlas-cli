package excalidrawplus

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Choosing targets reads what the key reaches: the collections of the workspace and the scenes of one
// collection. It only reads, with the key alone and never through the connection's targets, so a person finds
// an entry the targets do not name yet. The paging is the API's offset and limit; the cursor is the next offset.
// The API has no name search, so the surface filters the loaded pages.

const (
	browsePageSize = 50
	maxBrowseText  = 200
)

// browseText bounds a name Excalidraw+ sent: control characters become spaces and the text is cut on a rune boundary.
func browseText(value string) string {
	value = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return ' '
		}
		return r
	}, value))
	if len(value) > maxBrowseText {
		cut := maxBrowseText
		for !utf8.RuneStart(value[cut]) {
			cut--
		}
		value = strings.TrimSpace(value[:cut])
	}
	return value
}

func browseList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	req config.TargetListRequest) (config.TargetPage, error) {
	var parentID string
	switch req.Kind {
	case "collection":
	case "scene":
		id, ok := strings.CutPrefix(req.Parent, collectionPrefix)
		if !ok || !validID(id) {
			return config.TargetPage{}, invalidRequest("the parent must be collection/COLLECTION_ID")
		}
		parentID = id
	default:
		return config.TargetPage{}, invalidRequest("the kind is not one of this provider")
	}
	offset := 0
	if req.Cursor != "" {
		var err error
		if offset, err = strconv.Atoi(req.Cursor); err != nil || offset < 0 || offset > maxOffset {
			return config.TargetPage{}, invalidRequest("the cursor is not valid")
		}
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return config.TargetPage{}, err
	}
	query := url.Values{"offset": {strconv.Itoa(offset)}, "limit": {strconv.Itoa(browsePageSize)}}
	var page config.TargetPage
	var paging pageJSON
	if req.Kind == "collection" {
		var answer collectionsPageJSON
		if err := client.get(ctx, "choose targets", "/collections", query, &answer, maxResponseBytes); err != nil {
			return config.TargetPage{}, err
		}
		paging = answer.pageJSON
		for _, item := range answer.Data {
			if !item.IsDeleted && validID(item.ID) {
				page.Entries = append(page.Entries, config.TargetEntry{Target: collectionPrefix + item.ID,
					Name: browseText(item.Name), Kind: "collection", HasChildren: true})
			}
		}
	} else {
		query.Set("collectionId", parentID)
		var answer scenesPageJSON
		if err := client.get(ctx, "choose targets", "/scenes", query, &answer, maxResponseBytes); err != nil {
			return config.TargetPage{}, err
		}
		paging = answer.pageJSON
		for _, item := range answer.Data {
			meta := item.Metadata
			if !meta.IsDeleted && validID(meta.ID) && meta.Collection == parentID {
				page.Entries = append(page.Entries, config.TargetEntry{Target: scenePrefix + meta.ID,
					Name: browseText(meta.Name), Kind: "scene"})
			}
		}
	}
	if paging.HasNextPage && offset+browsePageSize <= maxOffset {
		page.Next = strconv.Itoa(offset + browsePageSize)
	}
	return page, nil
}

// browseResolveURL resolves nothing: the documentation of Excalidraw+ names the web interface's host and
// paths nowhere, so no URL is tied to a collection or scene without a guess. A person chooses from the lists.
func browseResolveURL(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, string, string) ([]config.TargetEntry, error) {
	return nil, nil
}
