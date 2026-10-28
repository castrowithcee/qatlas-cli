package nextcloud

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
)

// The account-wide feeds (activity, notifications, remote shares) name what an entry is about in many
// places. An entry is dropped when one identifier it names meets an exclusion, or when the read that would
// tell fails. An entry that names nothing attributable stays; the tool descriptions say so.

// feedRefs collects the identifiers one feed entry names, in the form the server reported them.
type feedRefs struct {
	paths        [][]string // relative to the Files root
	fileIDs      []string   // files known by ID only
	weakFileIDs  []string   // previews: used only when the entry names no other file
	noteIDs      []string
	cards        []string
	boards       []string
	calendars    []string
	addressbooks []string
	tokens       []string
	broken       bool // a path that cannot be placed
}

func (r *feedRefs) addPath(raw string) {
	raw = strings.Trim(strings.TrimSpace(raw), "/")
	if raw == "" {
		return
	}
	segments, err := splitRelative(raw)
	if err != nil || len(segments) == 0 {
		r.broken = true
		return
	}
	r.paths = append(r.paths, segments)
}

// addFile records a file by its path when the entry gives one, else by its ID.
func (r *feedRefs) addFile(path, id string) {
	if strings.TrimSpace(path) != "" {
		r.addPath(path)
		return
	}
	if id = strings.TrimSpace(id); id != "" {
		r.fileIDs = append(r.fileIDs, id)
	}
}

// addObject records an object by its type and ID; types without a mapping name nothing attributable.
func (r *feedRefs) addObject(kind, id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	switch strings.ReplaceAll(strings.ToLower(strings.TrimSpace(kind)), "_", "-") {
	case "file", "files":
		r.fileIDs = append(r.fileIDs, id)
	case "deck-board":
		r.boards = append(r.boards, id)
	case "deck-card":
		r.cards = append(r.cards, id)
	case "calendar":
		r.calendars = append(r.calendars, id)
	case "addressbook":
		r.addressbooks = append(r.addressbooks, id)
	case "chat", "call", "room":
		r.tokens = append(r.tokens, id)
	case "note", "notes":
		r.noteIDs = append(r.noteIDs, id)
	}
}

// addLink reads the identifiers an address carries: a folder, a file ID, a board or card, a conversation,
// a note, or a calendar or address book of the DAV tree.
func (r *feedRefs) addLink(raw string) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || raw == "" {
		return
	}
	query := parsed.Query()
	if dir := query.Get("dir"); dir != "" {
		r.addPath(dir)
	} else if id := query.Get("fileid"); id != "" {
		r.fileIDs = append(r.fileIDs, id)
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/")+"/"+strings.Trim(parsed.Fragment, "/"), "/")
	for i := 0; i+1 < len(segments); i++ {
		next := segments[i+1]
		switch segments[i] {
		case "board":
			r.boards = append(r.boards, next)
		case "card":
			r.cards = append(r.cards, next)
		case "call":
			r.tokens = append(r.tokens, next)
		case "note":
			r.noteIDs = append(r.noteIDs, next)
		case "f":
			r.fileIDs = append(r.fileIDs, next)
		case "files":
			if allDigits(next) {
				r.fileIDs = append(r.fileIDs, next)
			}
		case "calendars":
			if i+2 < len(segments) {
				r.calendars = append(r.calendars, segments[i+2])
			}
		case "addressbooks":
			if i+3 < len(segments) && next == "users" {
				r.addressbooks = append(r.addressbooks, segments[i+3])
			}
		}
	}
}

// addRich reads the objects behind a rich string, an object of placeholder keys.
func (r *feedRefs) addRich(raw json.RawMessage) {
	var parameters map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &parameters) != nil {
		return
	}
	for _, value := range parameters {
		var members map[string]json.RawMessage
		if json.Unmarshal(value, &members) != nil {
			continue
		}
		var kind, path, link string
		var id flexString
		_ = json.Unmarshal(members["type"], &kind)
		_ = json.Unmarshal(members["id"], &id)
		_ = json.Unmarshal(members["path"], &path)
		_ = json.Unmarshal(members["link"], &link)
		if kind == "file" {
			r.addFile(path, string(id))
		} else {
			r.addObject(kind, string(id))
		}
		r.addLink(link)
	}
}

// namedHidden reports whether an identifier the entry names meets an exclusion by name alone, without any
// read.
func (r feedRefs) namedHidden(s scope) bool {
	for _, p := range r.paths {
		if s.folderExcluded(p) {
			return true
		}
	}
	for _, id := range r.calendars {
		if s.calendars.excluding(s.excludes.calendars, false).hides(id) {
			return true
		}
	}
	for _, id := range r.addressbooks {
		if s.addressbooks.excluding(s.excludes.addressbooks, false).hides(id) {
			return true
		}
	}
	for _, id := range r.boards {
		if s.decks.excluding(s.excludes.decks, true).hides(id) {
			return true
		}
	}
	for _, token := range r.tokens {
		if s.talkHides(token) {
			return true
		}
	}
	return false
}

func (s scope) noteExcludesAny() bool { return len(s.excludes.notes) > 0 || len(s.cross.notes) > 0 }

// feedActive reports whether any exclusion can hide a feed entry.
func (s scope) feedActive() bool {
	e := s.excludes
	return len(e.folders)+len(e.notes)+len(e.calendars)+len(e.addressbooks)+len(e.talks)+len(e.decks) > 0 ||
		s.folderExcludesAny() || s.noteExcludesAny()
}

// feedFilter decides entries of one page. Each evidence read happens once per page; a read that fails
// counts as excluded.
type feedFilter struct {
	c     *Client
	ctx   context.Context
	op    string
	files map[string]bool
	notes map[string]bool
	rooms map[string]bool
}

// newFeedFilter returns nil when no exclusion applies, so that nothing is read.
func (c *Client) newFeedFilter(ctx context.Context, op string) *feedFilter {
	if !c.scope.feedActive() {
		return nil
	}
	return &feedFilter{c: c, ctx: ctx, op: op, files: map[string]bool{}, notes: map[string]bool{}, rooms: map[string]bool{}}
}

// hides reports whether the entry must be dropped. A nil filter hides nothing.
func (f *feedFilter) hides(r feedRefs) bool {
	if f == nil {
		return false
	}
	s := f.c.scope
	if len(r.paths) == 0 && len(r.fileIDs) == 0 {
		r.fileIDs = r.weakFileIDs
	}
	if r.namedHidden(s) || r.broken && s.folderExcludesAny() {
		return true
	}
	// No single read tells the board of a card, so a card without its board cannot be cleared.
	if len(r.cards) > 0 && len(r.boards) == 0 && len(s.excludes.decks) > 0 {
		return true
	}
	if s.folderExcludesAny() {
		for _, id := range r.fileIDs {
			if f.cached(f.files, id, f.fileHidden) {
				return true
			}
		}
	}
	if s.noteExcludesAny() {
		for _, id := range r.noteIDs {
			if f.cached(f.notes, id, f.noteHidden) {
				return true
			}
		}
	}
	if s.talkReadNeeded() {
		for _, token := range r.tokens {
			if f.cached(f.rooms, token, f.roomHidden) {
				return true
			}
		}
	}
	return false
}

func (f *feedFilter) cached(memo map[string]bool, key string, read func(string) bool) bool {
	if hidden, ok := memo[key]; ok {
		return hidden
	}
	hidden := read(key)
	memo[key] = hidden
	return hidden
}

func (f *feedFilter) fileHidden(id string) bool {
	hidden, err := f.c.fileExcluded(f.ctx, f.op, id)
	return err != nil || hidden
}

func (f *feedFilter) roomHidden(token string) bool {
	if !validTalkToken(token) {
		return true
	}
	state, err := f.c.readRoomState(f.ctx, f.op, token, messageTalkNoRoom)
	if err != nil {
		return true
	}
	hidden, err := f.c.roomExcluded(f.ctx, f.op, state.ObjectType, string(state.ObjectID))
	return err != nil || hidden
}

func (f *feedFilter) noteHidden(id string) bool {
	if len(id) > 20 || !allDigits(id) {
		return true
	}
	body, _, err := f.c.notesGetJSON(f.ctx, f.op, f.c.notesURL(url.Values{"exclude": {"content"}}, "notes", id))
	if err != nil {
		return true
	}
	var note rawNote
	if json.Unmarshal(body, &note) != nil || note.ID.String() != id {
		return true
	}
	return f.c.scope.categoryExcluded(note.Category)
}
