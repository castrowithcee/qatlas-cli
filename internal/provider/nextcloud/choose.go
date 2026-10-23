package nextcloud

import (
	"context"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// choicePageSize is the number of entries of one page of choices. Nextcloud answers these lists as one
	// piece, so the provider pages them itself.
	choicePageSize = 50
	// maxChoicePage bounds a cursor well above any real listing.
	maxChoicePage = 100000
	// maxChoiceName keeps a name well inside the bound the caller applies to entry texts.
	maxChoiceName = 200
	// maxCategoryChunks bounds the note chunks read to find the categories.
	maxCategoryChunks = 10
	// noteChunkSize is the chunk size of that read.
	noteChunkSize = 200
)

// openUnbound returns a client for choosing targets: the connection may still be a draft without targets,
// so the client is bound to the whole Files root of the identity and every list reads what the credential
// reaches.
func openUnbound(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor) (*Client, error) {
	if resolved == nil {
		return nil, providerError("open", "no connection was selected")
	}
	unbound := *resolved
	unbound.Target, unbound.Targets = "", []string{kindFolder}
	return open(ctx, &unbound, secrets, red, true)
}

// choiceName makes a name from the provider fit for an entry: without control characters, cut to a bounded
// length, else the fallback. It stays text; nothing interprets it.
func choiceName(name, fallback string) string {
	clean := strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, name))
	if len(clean) > maxChoiceName {
		clean = clean[:maxChoiceName]
		for !utf8.ValidString(clean) {
			clean = clean[:len(clean)-1]
		}
	}
	if clean == "" {
		return fallback
	}
	return clean
}

// choiceWindow cuts the page-th window out of n items and says whether another page follows.
func choiceWindow(n, page int) (lo, hi int, next string) {
	lo = (page - 1) * choicePageSize
	if lo > n {
		lo = n
	}
	hi = lo + choicePageSize
	if hi >= n {
		return lo, n, ""
	}
	return lo, hi, strconv.Itoa(page + 1)
}

func invalidChoice(message string) error { return &provider.InvalidRequestError{Message: message} }

// allEntry is the entry of a kind alone, which binds everything of that kind the identity reaches.
func allEntry(kind, name string) config.TargetEntry {
	return config.TargetEntry{Target: kind, Name: name, Kind: kind}
}

// ListTargets reads one page of the folders, calendars, address books, Talk conversations, Deck boards, or
// note categories the credential reaches, never filtered by the connection's current targets. Every kind but
// folder and notes is one level; folders and note categories are browsed below a parent of their own kind.
// The first page of the top level also offers the kind alone. The account and admin targets have no list.
func ListTargets(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	req config.TargetListRequest) (config.TargetPage, error) {
	page := 1
	if req.Cursor != "" {
		n, err := strconv.Atoi(req.Cursor)
		if err != nil || n < 1 || n > maxChoicePage || strconv.Itoa(n) != req.Cursor {
			return config.TargetPage{}, invalidChoice("the target cursor is not a page of this provider")
		}
		page = n
	}
	var parent []string
	switch req.Kind {
	case kindFolder, kindNotes:
		var err error
		if parent, err = choiceParent(req.Kind, req.Parent); err != nil {
			return config.TargetPage{}, err
		}
	case kindCalendar, kindAddressbook, kindTalk, kindDeck:
		if req.Parent != "" {
			return config.TargetPage{}, invalidChoice("this kind has no parent")
		}
	default:
		return config.TargetPage{}, invalidChoice("the kind is not one this provider offers")
	}
	client, err := openUnbound(ctx, resolved, secrets, red)
	if err != nil {
		return config.TargetPage{}, err
	}
	var entries []config.TargetEntry
	switch req.Kind {
	case kindFolder:
		entries, err = client.folderChoices(ctx, parent)
	case kindNotes:
		entries, err = client.categoryChoices(ctx, parent)
	case kindCalendar:
		entries, err = client.calendarChoices(ctx)
	case kindAddressbook:
		entries, err = client.addressbookChoices(ctx)
	case kindTalk:
		entries, err = client.talkChoices(ctx)
	default:
		entries, err = client.deckChoices(ctx)
	}
	if err != nil {
		return config.TargetPage{}, err
	}
	lo, hi, next := choiceWindow(len(entries), page)
	result := config.TargetPage{Entries: entries[lo:hi], Next: next}
	if page == 1 && len(parent) == 0 {
		result.Entries = append([]config.TargetEntry{allChoice(req.Kind)}, result.Entries...)
	}
	return result, nil
}

func allChoice(kind string) config.TargetEntry {
	switch kind {
	case kindFolder:
		return allEntry(kind, "Whole Files root")
	case kindCalendar:
		return allEntry(kind, "All calendars")
	case kindAddressbook:
		return allEntry(kind, "All address books")
	case kindTalk:
		return allEntry(kind, "All conversations")
	case kindDeck:
		return allEntry(kind, "All boards")
	}
	return allEntry(kind, "All notes")
}

// choiceParent reads the parent of a folder or category listing: empty or the kind alone for the top level,
// else kind/PATH whose segments pass the same check as a configured path.
func choiceParent(kind, raw string) ([]string, error) {
	parent := strings.TrimSpace(raw)
	if parent == "" || parent == kind {
		return nil, nil
	}
	rest, ok := strings.CutPrefix(parent, kind+"/")
	if !ok || strings.Contains(rest, "*") {
		return nil, invalidChoice("the parent must be " + kind + "/PATH")
	}
	segments, err := splitRelative(rest)
	if err != nil || len(segments) == 0 {
		return nil, invalidChoice("the parent path is not usable")
	}
	return segments, nil
}

// folderChoices lists the folders directly inside one folder with the Files listing. An entry whose path
// has a segment a configured path would refuse is dropped.
func (c *Client) folderChoices(ctx context.Context, parent []string) ([]config.TargetEntry, error) {
	listed, err := c.ListFiles(ctx, strings.Join(parent, "/"))
	if err != nil {
		return nil, err
	}
	place := "/" + strings.Join(parent, "/")
	var entries []config.TargetEntry
	for _, e := range listed.Entries {
		segments, err := splitRelative(e.Path)
		if e.Type != typeFolder || err != nil || len(segments) == 0 {
			continue
		}
		entries = append(entries, config.TargetEntry{
			Target: kindFolder + "/" + e.Path, Name: choiceName(e.Name, segments[len(segments)-1]),
			Kind: kindFolder, Path: place, HasChildren: true,
		})
	}
	return entries, nil
}

func (c *Client) calendarChoices(ctx context.Context) ([]config.TargetEntry, error) {
	listed, err := c.listCalendars(ctx, selection{all: true})
	if err != nil {
		return nil, err
	}
	var entries []config.TargetEntry
	for _, e := range listed.Calendars {
		if checkSegment(e.ID) == nil {
			entries = append(entries, config.TargetEntry{Target: kindCalendar + "/" + e.ID,
				Name: choiceName(e.Name, e.ID), Kind: kindCalendar})
		}
	}
	return entries, nil
}

func (c *Client) addressbookChoices(ctx context.Context) ([]config.TargetEntry, error) {
	listed, err := c.listAddressbooks(ctx, selection{all: true})
	if err != nil {
		return nil, err
	}
	var entries []config.TargetEntry
	for _, e := range listed.Addressbooks {
		if checkSegment(e.ID) == nil {
			entries = append(entries, config.TargetEntry{Target: kindAddressbook + "/" + e.ID,
				Name: choiceName(e.Name, e.ID), Kind: kindAddressbook})
		}
	}
	return entries, nil
}

func (c *Client) talkChoices(ctx context.Context) ([]config.TargetEntry, error) {
	listed, err := c.ListRooms(ctx, selection{all: true}, nil, false)
	if err != nil {
		return nil, err
	}
	var entries []config.TargetEntry
	for _, e := range listed.Rooms {
		entries = append(entries, config.TargetEntry{Target: kindTalk + "/" + e.Token,
			Name: choiceName(e.DisplayName, choiceName(e.Name, e.Token)), Kind: kindTalk})
	}
	return entries, nil
}

func (c *Client) deckChoices(ctx context.Context) ([]config.TargetEntry, error) {
	listed, err := c.ListDeckBoards(ctx, selection{all: true})
	if err != nil {
		return nil, err
	}
	var entries []config.TargetEntry
	for _, e := range listed.Boards {
		if checkBoardID(e.ID) == nil {
			entries = append(entries, config.TargetEntry{Target: kindDeck + "/" + e.ID,
				Name: choiceName(e.Title, e.ID), Kind: kindDeck})
		}
	}
	return entries, nil
}

// categoryChoices derives the categories directly below parent from the notes of the identity, read in
// chunks without content, at most maxCategoryChunks of them. Notes has no list of categories of its own.
// A category with a segment a configured path would refuse is dropped as a whole.
func (c *Client) categoryChoices(ctx context.Context, parent []string) ([]config.TargetEntry, error) {
	base := strings.Join(parent, "/")
	children := map[string]bool{}
	cursor := ""
	for chunk := 0; chunk < maxCategoryChunks; chunk++ {
		list, err := c.ListNotes(ctx, selection{all: true}, "", noteChunkSize, cursor)
		if err != nil {
			return nil, err
		}
		for _, note := range list.Notes {
			segments, err := splitRelative(note.Category)
			if err != nil || len(segments) <= len(parent) || !equalSegments(segments[:len(parent)], parent) {
				continue
			}
			child := strings.Join(segments[:len(parent)+1], "/")
			children[child] = children[child] || len(segments) > len(parent)+1
		}
		if cursor = list.NextCursor; cursor == "" {
			break
		}
	}
	names := make([]string, 0, len(children))
	for name := range children {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]config.TargetEntry, 0, len(names))
	for _, name := range names {
		entries = append(entries, config.TargetEntry{
			Target: kindNotes + "/" + name, Name: choiceName(name[strings.LastIndexByte(name, '/')+1:], name),
			Kind: kindNotes, Path: "/" + base, HasChildren: children[name],
		})
	}
	return entries, nil
}

// ResolveTargetURL turns a web URL of the connection's own instance into targets, in the forms the
// Nextcloud sources name: a Files URL .../apps/files/files/FILE_ID?dir=/PATH names the folder PATH, a Deck
// URL .../apps/deck/board/ID the board, and a Talk URL .../call/TOKEN the conversation. The installation
// path of the service and an index.php after it are accepted. Another origin, another form, or a path with
// a segment a configured path would refuse gives nothing. It sends no request, so entries are named by what
// the URL holds.
func ResolveTargetURL(_ context.Context, resolved *config.Resolved, _ *secret.Resolver, _ *redact.Redactor,
	_, rawURL string) ([]config.TargetEntry, error) {
	if resolved == nil {
		return nil, nil
	}
	origin, install, err := parseInstance(resolved.BaseURL)
	if err != nil {
		return nil, nil
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" ||
		strings.ToLower(u.Scheme+"://"+u.Host) != strings.ToLower(origin) {
		return nil, nil
	}
	var segments []string
	for _, part := range strings.Split(strings.Trim(u.EscapedPath(), "/"), "/") {
		decoded, err := url.PathUnescape(part)
		if err != nil || strings.Contains(decoded, "/") {
			return nil, nil
		}
		segments = append(segments, decoded)
	}
	if len(segments) < len(install) || !equalSegments(segments[:len(install)], install) {
		return nil, nil
	}
	rest := segments[len(install):]
	if len(rest) > 0 && rest[0] == "index.php" {
		rest = rest[1:]
	}
	switch {
	case len(rest) == 4 && rest[0] == "apps" && rest[1] == "files" && rest[2] == "files" && digitsOnly(rest[3]):
		return folderOfURL(u.Query()["dir"]), nil
	case len(rest) >= 4 && rest[0] == "apps" && rest[1] == "deck" && rest[2] == "board" &&
		rest[3] != "" && len(rest[3]) <= maxBoardIDLength && digitsOnly(rest[3]):
		return []config.TargetEntry{{Target: kindDeck + "/" + rest[3], Name: rest[3], Kind: kindDeck}}, nil
	case len(rest) == 2 && rest[0] == "call" && validTalkToken(rest[1]):
		return []config.TargetEntry{{Target: kindTalk + "/" + rest[1], Name: rest[1], Kind: kindTalk}}, nil
	}
	return nil, nil
}

// folderOfURL reads the folder of a Files URL from its one dir value. The Files root and a missing or
// repeated dir name no folder, so a URL never widens a binding to the whole root.
func folderOfURL(dirs []string) []config.TargetEntry {
	if len(dirs) != 1 || !strings.HasPrefix(dirs[0], "/") {
		return nil
	}
	path := strings.TrimSuffix(strings.TrimPrefix(dirs[0], "/"), "/")
	segments, err := splitRelative(path)
	if err != nil || len(segments) == 0 {
		return nil
	}
	return []config.TargetEntry{{
		Target: kindFolder + "/" + path, Name: choiceName(segments[len(segments)-1], path), Kind: kindFolder,
		Path: "/" + strings.Join(segments[:len(segments)-1], "/"), HasChildren: true,
	}}
}
