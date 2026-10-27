package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// Matomo stores the name in a column of 255 characters and the definition as text; Qatlas keeps both to the
	// size it accepts for a segment expression elsewhere.
	maxSegmentName      = 255
	maxStoredDefinition = 4096
	segmentsPlugin      = "SegmentEditor"
)

func segmentsRequirement(tool string) requirement {
	return requirement{tool: tool, plugin: segmentsPlugin}
}

var segmentSentOnce = sentOnceFor("segment", "matomo.segments.get")

// segmentChangeRisk is the risk of every segment change: confirmed, open to the instance, and about the saved
// segments of the instance.
func segmentChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: segmentsSensitive}
}

// readsSegment reports whether a segment bound to the website may be read: a segment of one website needs that
// website among the targets, and a segment of all websites (0) is shown to any connection that reaches a
// website, as SegmentEditor does for every website the token views.
func (s scope) readsSegment(site int) bool {
	if site == 0 {
		return s.reachesSites()
	}
	return s.allows(site)
}

// writesSegment reports whether a segment bound to the website may be changed: a segment of all websites (0)
// needs a binding to all websites, as Matomo allows it only to a Super User.
func (s scope) writesSegment(site int) bool {
	if site == 0 {
		return s.allSites
	}
	return s.allows(site)
}

// segmentSite settles the website a segment is written for, before any secret is read. 0 stands for all
// websites, which only a connection bound to site without an ID may write; an omitted idSite then means all
// websites, or the one website a connection binds.
func segmentSite(resolved *config.Resolved, given *int) (int, error) {
	bound, err := boundScope(resolved)
	if err != nil {
		return 0, err
	}
	if !bound.reachesSites() {
		return 0, invalidArgument("this connection is bound to no website")
	}
	if given != nil && (*given < 0 || len(strconv.Itoa(*given)) > maxIDDigits) {
		return 0, invalidArgument("idSite must be a website ID, or 0 for all websites")
	}
	switch {
	case given != nil && *given > 0:
		if !bound.allows(*given) {
			return 0, invalidArgument(outsideTargets)
		}
		return *given, nil
	case bound.allSites:
		return 0, nil
	case given == nil && len(bound.sites) == 1:
		return bound.sites[0], nil
	}
	return 0, invalidArgument("a segment for all websites needs a connection bound to site without an ID; " +
		"name the website with idSite")
}

// storedSegmentJSON is a segment as SegmentEditor reports it. Every member is read loosely and none is trusted.
type storedSegmentJSON struct {
	ID               *loose `json:"idsegment"`
	Name             *loose `json:"name"`
	Definition       *loose `json:"definition"`
	Hash             *loose `json:"hash"`
	Login            *loose `json:"login"`
	EnableAllUsers   *loose `json:"enable_all_users"`
	EnableOnlyIDSite *loose `json:"enable_only_idsite"`
	AutoArchive      *loose `json:"auto_archive"`
	Created          *loose `json:"ts_created"`
	LastEdit         *loose `json:"ts_last_edit"`
	Deleted          *loose `json:"deleted"`
	Starred          *loose `json:"starred"`
	StarredBy        *loose `json:"starred_by"`
}

// Segment is a saved segment. ID counts for the whole instance; EnableOnlyIDSite is the website the segment
// belongs to, 0 for all websites. All text is untrusted provider data.
type Segment struct {
	ID               int    `json:"id"`
	Name             string `json:"name"`
	Definition       string `json:"definition"`
	Hash             string `json:"hash,omitempty"`
	Login            string `json:"login,omitempty"`
	EnableAllUsers   *bool  `json:"enable_all_users,omitempty"`
	EnableOnlyIDSite int    `json:"enable_only_idsite"`
	AutoArchive      *bool  `json:"auto_archive,omitempty"`
	Created          string `json:"ts_created,omitempty"`
	LastEdit         string `json:"ts_last_edit,omitempty"`
	Deleted          *bool  `json:"deleted,omitempty"`
	Starred          *bool  `json:"starred,omitempty"`
	StarredBy        string `json:"starred_by,omitempty"`
}

// segment converts a row; ok is false for a row without a usable ID or with a website binding Qatlas cannot
// read, since the binding is the proof that the segment is reachable. A missing binding is Matomo's 0.
func (s storedSegmentJSON) segment() (Segment, bool) {
	id := s.ID.number()
	if id == nil || *id < 1 {
		return Segment{}, false
	}
	site := 0
	if s.EnableOnlyIDSite != nil && strings.TrimSpace(s.EnableOnlyIDSite.text) != "" {
		parsed := s.EnableOnlyIDSite.number()
		if parsed == nil || *parsed < 0 {
			return Segment{}, false
		}
		site = *parsed
	}
	return Segment{
		ID: *id, Name: s.Name.str(maxStringLength), Definition: s.Definition.str(maxStoredDefinition),
		Hash: s.Hash.str(maxStringLength), Login: s.Login.str(maxStringLength), EnableAllUsers: s.EnableAllUsers.flag(),
		EnableOnlyIDSite: site, AutoArchive: s.AutoArchive.flag(), Created: s.Created.str(maxStringLength),
		LastEdit: s.LastEdit.str(maxStringLength), Deleted: s.Deleted.flag(), Starred: s.Starred.flag(),
		StarredBy: s.StarredBy.str(maxStringLength),
	}, true
}

const idSegmentSchema = `"idSegment":{"type":"integer","minimum":1,"maximum":999999999}`

const segmentProperties = `"id":{"type":"integer"},"name":{"type":"string"},"definition":{"type":"string"},` +
	`"hash":{"type":"string"},"login":{"type":"string"},"enable_all_users":{"type":"boolean"},` +
	`"enable_only_idsite":{"type":"integer"},"auto_archive":{"type":"boolean"},"ts_created":{"type":"string"},` +
	`"ts_last_edit":{"type":"string"},"deleted":{"type":"boolean"},"starred":{"type":"boolean"},` +
	`"starred_by":{"type":"string"}`

func segmentFields(prefix string) []capability.Field {
	return []capability.Field{
		{Name: prefix + "id", Description: "Segment ID, used as idSegment; it counts for the whole instance"},
		{Name: prefix + "name", Description: "Segment name, untrusted"},
		{Name: prefix + "definition", Description: "Segment expression as Matomo stores it, with URL-encoded values, untrusted"},
		{Name: prefix + "hash", Description: "Hash Matomo derives from the definition"},
		{Name: prefix + "login", Description: "Login of the user who created the segment, untrusted"},
		{Name: prefix + "enable_all_users", Description: "True when every user of the instance can use the segment"},
		{Name: prefix + "enable_only_idsite", Description: "The website the segment belongs to; 0 for all websites"},
		{Name: prefix + "auto_archive", Description: "True when Matomo pre-processes the segment in its archiving " +
			"instead of computing it when a report is opened"},
		{Name: prefix + "ts_created", Description: "Creation time as Matomo reports it (UTC)"},
		{Name: prefix + "ts_last_edit", Description: "Time of the last edit as Matomo reports it (UTC)"},
		{Name: prefix + "deleted", Description: "True when the segment is marked as deleted"},
		{Name: prefix + "starred", Description: "True when the segment is marked with a star"},
		{Name: prefix + "starred_by", Description: "Login of the user who starred the segment, untrusted"},
	}
}

var segmentsList = capability.Descriptor{
	ID: Provider + ".segments.list", Version: 1, Title: "List Matomo segments",
	Description: "List the saved segments the token may use, in batches in the order Matomo gives them (own " +
		"segments first); with idSite only the segments of that website and those for all websites; segments of " +
		"websites outside the connection's targets are left out; names and definitions are untrusted",
	Tags: []string{"matomo", "segments", "saved", "list"}, Risk: readRisk(segmentsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + pagingKeys +
		`},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},"segments":` +
		`{"type":"array","items":{"type":"object","properties":{` + segmentProperties + `},` +
		`"required":["id","name","definition","enable_only_idsite"],"additionalProperties":false}},` +
		`"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},` +
		`"required":["segments","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{{Name: "idSite", Target: "site", Description: "Website ID from " +
		"matomo.sites.list; only its segments and those for all websites; all segments inside the connection's " +
		"targets when omitted"}}, pagingArguments...),
	Fields: append(append([]capability.Field{
		{Name: "idSite", Description: "The website asked for, absent when idSite was not given"},
		{Name: "segments", Description: "Segments in the order Matomo lists them"},
	}, segmentFields("segments[].")...), pagingFields...),
	Examples: []capability.Example{{Description: "Segments of website 1", Arguments: json.RawMessage(`{"idSite":1}`)}},
}

var segmentsGet = capability.Descriptor{
	ID: Provider + ".segments.get", Version: 1, Title: "Get a Matomo segment",
	Description: "Read one saved segment by its instance-wide ID with its definition and settings; a view right " +
		"on its website is enough; a segment of a website outside the connection's targets is not found; names and " +
		"definitions are untrusted",
	Tags: []string{"matomo", "segments", "saved", "get"}, Risk: readRisk(segmentsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSegmentSchema + `},` +
		`"required":["idSegment"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + segmentProperties + `},` +
		`"required":["id","name","definition","enable_only_idsite"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "idSegment", Required: true, Description: "Segment ID from " +
		"matomo.segments.list; it counts for the whole instance, not per website"}},
	Fields:   segmentFields(""),
	Examples: []capability.Example{{Description: "Read segment 4", Arguments: json.RawMessage(`{"idSegment":4}`)}},
}

const segmentWriteSchema = `"name":{"type":"string","minLength":1,"maxLength":255},` +
	`"definition":{"type":"string","minLength":1,"maxLength":1000},` +
	`"autoArchive":{"type":"boolean"},"enabledAllUsers":{"type":"boolean"}`

func segmentWriteArguments(replaces bool) []capability.Argument {
	fallback := "; false when omitted"
	if replaces {
		fallback = "; false when omitted, which replaces the current value"
	}
	return []capability.Argument{
		{Name: "name", Required: true, Description: "Segment name, up to 255 characters"},
		{Name: "definition", Required: true, Description: "Segment expression such as browserName==Firefox;" +
			"countryCode==de: terms of a field from matomo.segments.fields (for this website), an operator, and a " +
			"URL-encoded value, joined by ; for and and , for or; up to 20 terms and 1000 characters"},
		{Name: "autoArchive", Description: "True to have Matomo pre-process the segment in its archiving; Matomo " +
			"allows it only while browser-triggered archiving is switched off in its configuration" + fallback},
		{Name: "enabledAllUsers", Description: "True to share the segment with every user; only a Super User " +
			"may set it" + fallback},
	}
}

var segmentsAdd = capability.Descriptor{
	ID: Provider + ".segments.add", Version: 1, Title: "Add a Matomo segment",
	Description: "Save a segment for one website, or with idSite 0 for all websites; the right to create it " +
		"depends on the instance setting adding_segment_requires_access (view by default) and, for all " +
		"websites, the Super User right; the definition is checked against the segment fields of the website; " +
		"not idempotent, since a repeated call saves a second segment; it changes no recorded data, but " +
		"autoArchive makes Matomo pre-process the segment; sent once and never repeated",
	Tags: []string{"matomo", "segments", "saved", "create"}, Risk: segmentChangeRisk(capability.EffectCreate,
		capability.IdempotencyNonIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer","minimum":0,` +
		`"maximum":999999999},` + segmentWriteSchema + `},"required":["name","definition"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSegment":{"type":"integer"},` +
		`"idSite":{"type":"integer"},"created":{"type":"boolean"}},"required":["idSegment","idSite","created"],` +
		`"additionalProperties":false}`),
	Arguments: append([]capability.Argument{{Name: "idSite", Target: "site", Description: "Website the segment " +
		"belongs to; 0 for all websites, which needs a connection bound to site without an ID and the Super User " +
		"right; omitted, it is the one website of a connection that binds exactly one, else all websites"}},
		segmentWriteArguments(false)...),
	Fields: []capability.Field{
		{Name: "idSegment", Description: "ID of the new segment"},
		{Name: "idSite", Description: "The website the segment belongs to; 0 for all websites"},
		{Name: "created", Description: "True once Matomo saved the segment"},
	},
	Examples: []capability.Example{{Description: "Visitors from Germany on website 1", Arguments: json.RawMessage(
		`{"idSite":1,"name":"Germany","definition":"countryCode==de"}`)}},
}

var segmentsUpdate = capability.Descriptor{
	ID: Provider + ".segments.update", Version: 1, Title: "Update a Matomo segment",
	Description: "Replace name, definition, website, and settings of one saved segment as a whole: every value " +
		"not given falls back to its default (not pre-processed, not shared), so read the segment with " +
		"matomo.segments.get first; the segment is read once before the change and must lie inside the " +
		"connection's targets; only its creator or a Super User may edit it, and only a Super User a segment " +
		"for all websites; a changed definition of a pre-processed segment makes Matomo process it again; " +
		"idempotent, since the same values leave the same state; sent once and never repeated",
	Tags: []string{"matomo", "segments", "saved", "update"}, Risk: segmentChangeRisk(capability.EffectUpdate,
		capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSegmentSchema + `,` +
		`"idSite":{"type":"integer","minimum":0,"maximum":999999999},` + segmentWriteSchema + `},` +
		`"required":["idSegment","name","definition"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSegment":{"type":"integer"},` +
		`"idSite":{"type":"integer"},"updated":{"type":"boolean"}},"required":["idSegment","idSite","updated"],` +
		`"additionalProperties":false}`),
	Arguments: append([]capability.Argument{
		{Name: "idSegment", Required: true, Description: "ID of the segment to replace, from matomo.segments.list"},
		{Name: "idSite", Target: "site", Description: "Website the segment belongs to afterwards; 0 for all " +
			"websites, which needs a connection bound to site without an ID and the Super User right; the " +
			"current website of the segment when omitted"},
	}, segmentWriteArguments(true)...),
	Fields: []capability.Field{
		{Name: "idSegment", Description: "ID of the updated segment"},
		{Name: "idSite", Description: "The website the segment belongs to now; 0 for all websites"},
		{Name: "updated", Description: "True once Matomo replaced the segment"},
	},
	Examples: []capability.Example{{Description: "Rename segment 4 and keep it on website 1", Arguments: json.RawMessage(
		`{"idSegment":4,"idSite":1,"name":"Germany","definition":"countryCode==de"}`)}},
}

// SegmentsResult is the answer of segments.list.
type SegmentsResult struct {
	IDSite     int       `json:"idSite,omitempty"`
	Segments   []Segment `json:"segments"`
	HasMore    bool      `json:"has_more"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

// SegmentAdded is the answer of segments.add.
type SegmentAdded struct {
	IDSegment int  `json:"idSegment"`
	IDSite    int  `json:"idSite"`
	Created   bool `json:"created"`
}

// SegmentUpdated is the answer of segments.update.
type SegmentUpdated struct {
	IDSegment int  `json:"idSegment"`
	IDSite    int  `json:"idSite"`
	Updated   bool `json:"updated"`
}

func invokeSegmentsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list segments"
	var args struct {
		IDSite *int   `json:"idSite"`
		Limit  int    `json:"limit"`
		Cursor string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	site := 0
	if args.IDSite != nil {
		if site, err = selectSite(resolved, args.IDSite); err != nil {
			return nil, err
		}
	} else if !bound.reachesSites() {
		return nil, invalidArgument("this connection is bound to no website")
	}
	paging, err := startPage(provider.CursorBinding(segmentsList.ID, scopeBinding(resolved), site), args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{}
	if site > 0 {
		params.Set("idSite", strconv.Itoa(site))
	}
	var rows []json.RawMessage
	if err := client.call(ctx, op, segmentsRequirement(segmentsList.ID), "SegmentEditor.getAll", params, &rows); err != nil {
		return nil, err
	}
	// Every row is proven against the targets: Matomo may answer more than the connection reaches, such as the
	// segments of all websites for a token that sees them.
	var segments []Segment
	for _, row := range rows {
		var stored storedSegmentJSON
		if json.Unmarshal(row, &stored) != nil {
			continue
		}
		segment, ok := stored.segment()
		if !ok || !bound.readsSegment(segment.EnableOnlyIDSite) ||
			site > 0 && segment.EnableOnlyIDSite != 0 && segment.EnableOnlyIDSite != site {
			continue
		}
		segments = append(segments, segment)
	}
	batch, next := slice(paging, segments)
	if batch == nil {
		batch = []Segment{}
	}
	return &SegmentsResult{IDSite: site, Segments: batch, HasMore: next != "", NextCursor: next}, nil
}

// fetchSegment reads one segment by its instance-wide ID and proves it against the targets, for reading or, with
// write, for changing it. A segment outside them is not found, so the answer does not tell whether it exists.
func (c *Client) fetchSegment(ctx context.Context, op string, req requirement, id int, write bool) (Segment, error) {
	var answer json.RawMessage
	params := url.Values{"idSegment": {strconv.Itoa(id)}}
	if err := c.call(ctx, op, req, "SegmentEditor.get", params, &answer); err != nil {
		return Segment{}, err
	}
	notFound := &provider.Error{Class: provider.ClassNotFound, Op: op,
		Message: "Matomo has no segment with this ID inside the targets of this connection"}
	answer = bytes.TrimSpace(answer)
	if bytes.Equal(answer, []byte("null")) {
		return Segment{}, notFound
	}
	var row storedSegmentJSON
	if !bytes.HasPrefix(answer, []byte("{")) || json.Unmarshal(answer, &row) != nil {
		return Segment{}, provider.InvalidResponse(op, "Matomo returned an invalid segment")
	}
	segment, ok := row.segment()
	if !ok || segment.ID != id {
		return Segment{}, provider.InvalidResponse(op, "Matomo returned another segment than asked for")
	}
	reach := c.scope.readsSegment
	if write {
		reach = c.scope.writesSegment
	}
	if !reach(segment.EnableOnlyIDSite) {
		return Segment{}, notFound
	}
	return segment, nil
}

func invokeSegmentsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get segment"
	var args struct {
		IDSegment *int `json:"idSegment"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := segmentIDOf(args.IDSegment)
	if err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if !bound.reachesSites() {
		return nil, invalidArgument("this connection is bound to no website")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	segment, err := client.fetchSegment(ctx, op, segmentsRequirement(segmentsGet.ID), id, false)
	if err != nil {
		return nil, err
	}
	return &segment, nil
}

func segmentIDOf(given *int) (int, error) {
	if given == nil || *given < 1 || len(strconv.Itoa(*given)) > maxIDDigits {
		return 0, invalidArgument("idSegment must be a positive integer segment ID from matomo.segments.list")
	}
	return *given, nil
}

type segmentDefinition struct {
	Name            *string `json:"name"`
	Definition      string  `json:"definition"`
	AutoArchive     *bool   `json:"autoArchive"`
	EnabledAllUsers *bool   `json:"enabledAllUsers"`
}

// check settles everything that needs no instance: the name, the shape of the expression, and the fields it
// names. It returns those fields for the check against the website's segment fields.
func (d segmentDefinition) check() ([]string, error) {
	if d.Name == nil || strings.TrimSpace(*d.Name) == "" || utf8.RuneCountInString(*d.Name) > maxSegmentName {
		return nil, invalidArgument("name must hold 1 to 255 characters")
	}
	if !validSegmentExpression(d.Definition) {
		return nil, invalidArgument("definition must be terms of field, operator, and URL-encoded value " +
			"joined by ; or , (fields: matomo.segments.fields)")
	}
	var fields []string
	for _, term := range strings.FieldsFunc(d.Definition, func(r rune) bool { return r == ';' || r == ',' }) {
		end := strings.IndexFunc(term, func(r rune) bool { return !validSegmentNameRune(r) })
		fields = append(fields, term[:end])
	}
	return fields, nil
}

func validSegmentNameRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '.'
}

// checkFields proves that every field of the definition is a segment field of the website, or of the instance
// for a segment of all websites. Matomo checks the whole expression again and its answer decides.
func (c *Client) checkFields(ctx context.Context, op, tool string, site int, fields []string) error {
	params := url.Values{}
	if site > 0 {
		params.Set("idSites", strconv.Itoa(site))
	}
	var rows []segmentJSON
	if err := c.call(ctx, op, segmentsRequirement(tool), "API.getSegmentsMetadata", params, &rows); err != nil {
		return err
	}
	known := map[string]bool{}
	for _, row := range rows {
		if field, ok := row.field(); ok {
			known[field.Segment] = true
		}
	}
	for _, field := range fields {
		if !known[field] {
			return invalidArgument("definition names a field that is no segment field of this website; " +
				"see matomo.segments.fields")
		}
	}
	return nil
}

func (d segmentDefinition) params(site int) url.Values {
	params := url.Values{
		"name": {*d.Name}, "definition": {d.Definition},
		"autoArchive": {flagValue(d.AutoArchive)}, "enabledAllUsers": {flagValue(d.EnabledAllUsers)},
	}
	if site > 0 {
		params.Set("idSite", strconv.Itoa(site))
	}
	return params
}

func invokeSegmentsAdd(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "add segment"
	var args struct {
		IDSite *int `json:"idSite"`
		segmentDefinition
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	site, err := segmentSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	fields, err := args.check()
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.checkFields(ctx, op, segmentsAdd.ID, site, fields); err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, segmentsRequirement(segmentsAdd.ID), "SegmentEditor.add", args.params(site),
		segmentSentOnce, &answer); err != nil {
		return nil, err
	}
	var created int
	if scalar(answer, &created) != nil || created < 1 {
		return nil, unclearChange(op, "Matomo returned no usable ID for the new segment", segmentSentOnce)
	}
	return &SegmentAdded{IDSegment: created, IDSite: site, Created: true}, nil
}

func invokeSegmentsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update segment"
	var args struct {
		IDSegment *int `json:"idSegment"`
		IDSite    *int `json:"idSite"`
		segmentDefinition
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := segmentIDOf(args.IDSegment)
	if err != nil {
		return nil, err
	}
	// An omitted idSite keeps the binding the segment has; it is settled after the proving read.
	site := 0
	if args.IDSite != nil {
		if site, err = segmentSite(resolved, args.IDSite); err != nil {
			return nil, err
		}
	} else if bound, err := boundScope(resolved); err != nil {
		return nil, err
	} else if !bound.reachesSites() {
		return nil, invalidArgument("this connection is bound to no website")
	}
	fields, err := args.check()
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	// Segment IDs count for the whole instance, so one read proves that the segment lies inside the targets
	// before anything is changed.
	req := segmentsRequirement(segmentsUpdate.ID)
	current, err := client.fetchSegment(ctx, op, req, id, true)
	if err != nil {
		return nil, err
	}
	if args.IDSite == nil {
		site = current.EnableOnlyIDSite
	}
	if err := client.checkFields(ctx, op, segmentsUpdate.ID, site, fields); err != nil {
		return nil, err
	}
	params := args.params(site)
	params.Set("idSegment", strconv.Itoa(id))
	var answer json.RawMessage
	if err := client.change(ctx, op, req, "SegmentEditor.update", params, segmentSentOnce, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the update", segmentSentOnce)
	}
	return &SegmentUpdated{IDSegment: id, IDSite: site, Updated: true}, nil
}
