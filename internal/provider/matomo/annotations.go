package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
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
	annotationsSensitive = "matomo-annotations"
	// Matomo cuts a longer note silently; Qatlas refuses it instead. Matomo HTML-escapes a stored note, so an
	// answer may be longer than the note was.
	maxNoteLength       = 255
	maxNoteAnswerLength = 2048
	maxLastN            = 1000
)

var (
	// A single day, a range, or today and yesterday; period and lastN shape the rest.
	annotationListDate = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}(,\d{4}-\d{2}-\d{2})?|today|yesterday)$`)
	annotationDate     = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}|today|yesterday)$`)
	annotationPeriods  = []string{"day", "week", "month", "year", "range"}
)

var annotationSentOnce = sentOnceFor("annotation", "matomo.annotations.list")

func annotationsRequirement(tool string) requirement {
	return requirement{tool: tool, plugin: "Annotations"}
}

func annotationChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: annotationsSensitive}
}

const annotationProperties = `"idNote":{"type":"integer"},"date":{"type":"string"},"note":{"type":"string"},` +
	`"starred":{"type":"boolean"},"user":{"type":"string"},"canEditOrDelete":{"type":"boolean"}`

const (
	idNoteSchema   = `"idNote":{"type":"integer","minimum":1,"maximum":999999999}`
	noteSchema     = `"note":{"type":"string","minLength":1,"maxLength":255}`
	noteDateSchema = `"date":{"type":"string","minLength":1,"maxLength":16}`
)

func annotationFields(prefix string) []capability.Field {
	return []capability.Field{
		{Name: prefix + "idNote", Description: "Annotation ID, used as idNote by the other tools; it counts per website"},
		{Name: prefix + "date", Description: "Day the annotation is attached to, YYYY-MM-DD"},
		{Name: prefix + "note", Description: "Annotation text, at most 255 characters as written, untrusted; Matomo " +
			"returns it HTML-escaped"},
		{Name: prefix + "starred", Description: "True when the annotation is starred"},
		{Name: prefix + "user", Description: "Login of the user who wrote the annotation, untrusted"},
		{Name: prefix + "canEditOrDelete", Description: "True when the token's user may change the annotation: write " +
			"access to the website or the author"},
	}
}

func idNoteArgument(verb string) capability.Argument {
	return capability.Argument{Name: "idNote", Required: true, Description: "ID of the annotation to " + verb +
		", from matomo.annotations.list; it counts per website"}
}

var annotationsList = capability.Descriptor{
	ID: Provider + ".annotations.list", Version: 1, Title: "List Matomo annotations",
	Description: "List the annotations of one website in a date range, ordered by date and ID, in batches; without " +
		"date every annotation of the website; notes and logins are untrusted",
	Tags: []string{"matomo", "annotations", "notes", "list"}, Risk: readRisk(annotationsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` +
		`"date":{"type":"string","minLength":1,"maxLength":32},"period":{"type":"string","enum":` +
		enumJSON(annotationPeriods) + `},"lastN":{"type":"integer","minimum":1,"maximum":1000},` + pagingKeys +
		`},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},"annotations":` +
		`{"type":"array","items":{"type":"object","properties":{` + annotationProperties + `},` +
		`"required":["idNote","date"],"additionalProperties":false}},"has_more":{"type":"boolean"},` +
		`"next_cursor":{"type":"string"}},"required":["idSite","annotations","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument(),
		{Name: "date", Description: "Date of the period: YYYY-MM-DD, today, yesterday, or for period range " +
			"YYYY-MM-DD,YYYY-MM-DD; all annotations when omitted"},
		{Name: "period", Description: "Period type of date: day, week, month, year, or range; day when omitted"},
		{Name: "lastN", Description: "Include the last N periods up to date, 1 to 1000; ignored for period range"},
	}, pagingArguments...),
	Fields: append(append([]capability.Field{{Name: "idSite", Description: "The website the annotations belong to"},
		{Name: "annotations", Description: "Annotations ordered by date and idNote"}},
		annotationFields("annotations[].")...), pagingFields...),
	Examples: []capability.Example{{Description: "List the annotations of the last 3 months of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"date":"today","period":"month","lastN":3}`)}},
}

var annotationsGet = capability.Descriptor{
	ID: Provider + ".annotations.get", Version: 1, Title: "Get a Matomo annotation",
	Description: "Read one annotation of a website; the ID counts per website, and an ID the website does not have " +
		"is not found; note and login are untrusted",
	Tags: []string{"matomo", "annotations", "notes", "get"}, Risk: readRisk(annotationsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idNoteSchema + `},` +
		`"required":["idNote"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` + annotationProperties +
		`},"required":["idSite","idNote","date"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(), idNoteArgument("read")},
	Fields:    append([]capability.Field{{Name: "idSite", Description: "The website the annotation belongs to"}}, annotationFields("")...),
	Examples:  []capability.Example{{Description: "Read annotation 4 of website 1", Arguments: json.RawMessage(`{"idSite":1,"idNote":4}`)}},
}

var annotationsAdd = capability.Descriptor{
	ID: Provider + ".annotations.add", Version: 1, Title: "Add a Matomo annotation",
	Description: "Create one annotation on a day of a website; Matomo 5 needs a view right on it for a named user, " +
		"Matomo 6 needs a write right; the note is at most 255 characters; not idempotent, since a repeated call " +
		"creates a second annotation; sent once and never repeated",
	Tags: []string{"matomo", "annotations", "notes", "create"}, Risk: annotationChangeRisk(capability.EffectCreate,
		capability.IdempotencyNonIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + noteDateSchema + `,` + noteSchema +
		`,"starred":{"type":"boolean"}},"required":["date","note"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` + annotationProperties +
		`,"created":{"type":"boolean"}},"required":["idSite","idNote","created"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(),
		{Name: "date", Required: true, Description: "Day the annotation is attached to: YYYY-MM-DD, today, or yesterday"},
		{Name: "note", Required: true, Description: "Annotation text, 1 to 255 characters; a longer text is refused"},
		{Name: "starred", Description: "True to star the annotation; false when omitted"}},
	Fields: append([]capability.Field{{Name: "idSite", Description: "The website the annotation was created for"},
		{Name: "created", Description: "True once Matomo created the annotation"}}, annotationFields("")...),
	Examples: []capability.Example{{Description: "Mark a campaign start",
		Arguments: json.RawMessage(`{"idSite":1,"date":"2026-10-01","note":"Autumn campaign started","starred":true}`)}},
}

var annotationsUpdate = capability.Descriptor{
	ID: Provider + ".annotations.update", Version: 1, Title: "Update a Matomo annotation",
	Description: "Change the date, note, or star of one annotation of a website; a value not given stays as it is, " +
		"and at least one is required; it needs a write right on the website or being the author; idempotent, " +
		"since the same values leave the same state; sent once and never repeated",
	Tags: []string{"matomo", "annotations", "notes", "update"}, Risk: annotationChangeRisk(capability.EffectUpdate,
		capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idNoteSchema + `,` +
		noteDateSchema + `,` + noteSchema + `,"starred":{"type":"boolean"}},"required":["idNote"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` + annotationProperties +
		`,"updated":{"type":"boolean"}},"required":["idSite","idNote","updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(), idNoteArgument("change"),
		{Name: "date", Description: "New day: YYYY-MM-DD, today, or yesterday; unchanged when omitted"},
		{Name: "note", Description: "New text, 1 to 255 characters; unchanged when omitted"},
		{Name: "starred", Description: "True to star, false to unstar; unchanged when omitted"}},
	Fields: append([]capability.Field{{Name: "idSite", Description: "The website the annotation belongs to"},
		{Name: "updated", Description: "True once Matomo answered the changed annotation"}}, annotationFields("")...),
	Examples: []capability.Example{{Description: "Unstar annotation 4 of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"idNote":4,"starred":false}`)}},
}

// annotationJSON is an annotation as Annotations.get reports it. Every member is read loosely.
type annotationJSON struct {
	ID              *loose `json:"idNote"`
	RawID           *loose `json:"id"`
	IDSite          *loose `json:"idsite"`
	Date            *loose `json:"date"`
	Note            *loose `json:"note"`
	Starred         *loose `json:"starred"`
	User            *loose `json:"user"`
	CanEditOrDelete *loose `json:"canEditOrDelete"`
}

// Annotation is an annotation of a website. Note and user are untrusted provider data.
type Annotation struct {
	IDNote          int    `json:"idNote"`
	Date            string `json:"date"`
	Note            string `json:"note,omitempty"`
	Starred         *bool  `json:"starred,omitempty"`
	User            string `json:"user,omitempty"`
	CanEditOrDelete *bool  `json:"canEditOrDelete,omitempty"`
}

// annotation converts a row; ok is false for a row without a usable ID or from another website than site.
func (a annotationJSON) annotation(site int) (Annotation, bool) {
	id := a.ID.number()
	if id == nil {
		id = a.RawID.number()
	}
	if id == nil || *id < 1 {
		return Annotation{}, false
	}
	if row := a.IDSite.number(); row != nil && *row != site {
		return Annotation{}, false
	}
	date := a.Date.str(maxStringLength)
	if len(date) > 10 {
		date = date[:10]
	}
	return Annotation{IDNote: *id, Date: date, Note: a.Note.str(maxNoteAnswerLength), Starred: a.Starred.flag(),
		User: a.User.str(maxStringLength), CanEditOrDelete: a.CanEditOrDelete.flag()}, true
}

// AnnotationsResult is the answer of annotations.list.
type AnnotationsResult struct {
	IDSite      int          `json:"idSite"`
	Annotations []Annotation `json:"annotations"`
	HasMore     bool         `json:"has_more"`
	NextCursor  string       `json:"next_cursor,omitempty"`
}

// AnnotationResult is the answer of annotations.get.
type AnnotationResult struct {
	IDSite int `json:"idSite"`
	Annotation
}

// AnnotationAdded is the answer of annotations.add.
type AnnotationAdded struct {
	IDSite int `json:"idSite"`
	Annotation
	Created bool `json:"created"`
}

// AnnotationUpdated is the answer of annotations.update.
type AnnotationUpdated struct {
	IDSite int `json:"idSite"`
	Annotation
	Updated bool `json:"updated"`
}

// readAnnotation reads one annotation object for the website; ok is false for any other shape.
func readAnnotation(raw json.RawMessage, site int) (Annotation, bool) {
	raw = bytes.TrimSpace(raw)
	var row annotationJSON
	if !bytes.HasPrefix(raw, []byte("{")) || json.Unmarshal(raw, &row) != nil {
		return Annotation{}, false
	}
	return row.annotation(site)
}

func invokeAnnotationsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list annotations"
	var args struct {
		IDSite *int   `json:"idSite"`
		Date   string `json:"date"`
		Period string `json:"period"`
		LastN  *int   `json:"lastN"`
		Limit  int    `json:"limit"`
		Cursor string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	if args.Date != "" && !annotationListDate.MatchString(args.Date) {
		return nil, invalidArgument("date must be YYYY-MM-DD, today, yesterday, or YYYY-MM-DD,YYYY-MM-DD")
	}
	if args.Period != "" && !slices.Contains(annotationPeriods, args.Period) {
		return nil, invalidArgument("period must be one of " + strings.Join(annotationPeriods, ", "))
	}
	if args.LastN != nil && (*args.LastN < 1 || *args.LastN > maxLastN) {
		return nil, invalidArgument("lastN must be between 1 and " + strconv.Itoa(maxLastN))
	}
	if args.Date == "" && (args.Period != "" || args.LastN != nil) {
		return nil, invalidArgument("period and lastN need a date")
	}
	if strings.Contains(args.Date, ",") != (args.Period == "range") {
		return nil, invalidArgument("a date range goes with the period range, and the period range with a date range")
	}
	lastN := 0
	if args.LastN != nil {
		lastN = *args.LastN
	}
	binding := provider.CursorBinding(annotationsList.ID, scopeBinding(resolved), id, args.Date, args.Period, lastN)
	paging, err := startPage(binding, args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(id)}}
	if args.Date != "" {
		params.Set("date", args.Date)
		if args.Period != "" {
			params.Set("period", args.Period)
		}
		if args.LastN != nil {
			params.Set("lastN", strconv.Itoa(*args.LastN))
		}
	}
	var answer map[string]json.RawMessage
	if err := client.call(ctx, op, annotationsRequirement(annotationsList.ID), "Annotations.getAll", params, &answer); err != nil {
		return nil, err
	}
	var rows []annotationJSON
	// Only the requested website's entry is read; any other key is dropped.
	if entry, found := answer[strconv.Itoa(id)]; found && json.Unmarshal(entry, &rows) != nil {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid annotation list")
	}
	annotations := []Annotation{}
	for _, row := range rows {
		if annotation, ok := row.annotation(id); ok {
			annotations = append(annotations, annotation)
		}
	}
	slices.SortStableFunc(annotations, func(a, b Annotation) int {
		if c := strings.Compare(a.Date, b.Date); c != 0 {
			return c
		}
		return a.IDNote - b.IDNote
	})
	batch, next := slice(paging, annotations)
	if batch == nil {
		batch = []Annotation{}
	}
	return &AnnotationsResult{IDSite: id, Annotations: batch, HasMore: next != "", NextCursor: next}, nil
}

func noteIDOf(given *int) (int, error) {
	if given == nil || *given < 1 || len(strconv.Itoa(*given)) > maxGoalIDDigits {
		return 0, invalidArgument("idNote must be a positive integer annotation ID")
	}
	return *given, nil
}

func invokeAnnotationsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get annotation"
	var args struct {
		IDSite *int `json:"idSite"`
		IDNote *int `json:"idNote"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	noteID, err := noteIDOf(args.IDNote)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(id)}, "idNote": {strconv.Itoa(noteID)}}
	var answer json.RawMessage
	if err := client.call(ctx, op, annotationsRequirement(annotationsGet.ID), "Annotations.get", params, &answer); err != nil {
		return nil, err
	}
	found, ok := readAnnotation(answer, id)
	if !ok || found.IDNote != noteID {
		return nil, provider.InvalidResponse(op, "Matomo returned another annotation than asked for")
	}
	return &AnnotationResult{IDSite: id, Annotation: found}, nil
}

// checkNote refuses a text Matomo would cut silently or take as empty.
func checkNote(note string) error {
	if strings.TrimSpace(note) == "" || utf8.RuneCountInString(note) > maxNoteLength {
		return invalidArgument("note must hold 1 to 255 characters")
	}
	return nil
}

func checkNoteDate(date string) error {
	if !annotationDate.MatchString(date) {
		return invalidArgument("date must be YYYY-MM-DD, today, or yesterday")
	}
	return nil
}

func invokeAnnotationsAdd(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "add annotation"
	var args struct {
		IDSite  *int   `json:"idSite"`
		Date    string `json:"date"`
		Note    string `json:"note"`
		Starred *bool  `json:"starred"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	if err := checkNoteDate(args.Date); err != nil {
		return nil, err
	}
	if err := checkNote(args.Note); err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(id)}, "date": {args.Date}, "note": {args.Note},
		"starred": {flagValue(args.Starred)}}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, annotationsRequirement(annotationsAdd.ID), "Annotations.add", params, annotationSentOnce, &answer); err != nil {
		return nil, err
	}
	created, ok := readAnnotation(answer, id)
	if !ok {
		return nil, unclearChange(op, "Matomo returned no usable annotation", annotationSentOnce)
	}
	return &AnnotationAdded{IDSite: id, Annotation: created, Created: true}, nil
}

func invokeAnnotationsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update annotation"
	var args struct {
		IDSite  *int    `json:"idSite"`
		IDNote  *int    `json:"idNote"`
		Date    *string `json:"date"`
		Note    *string `json:"note"`
		Starred *bool   `json:"starred"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	noteID, err := noteIDOf(args.IDNote)
	if err != nil {
		return nil, err
	}
	if args.Date == nil && args.Note == nil && args.Starred == nil {
		return nil, invalidArgument("give at least one of date, note, and starred")
	}
	params := url.Values{"idSite": {strconv.Itoa(id)}, "idNote": {strconv.Itoa(noteID)}}
	if args.Date != nil {
		if err := checkNoteDate(*args.Date); err != nil {
			return nil, err
		}
		params.Set("date", *args.Date)
	}
	if args.Note != nil {
		if err := checkNote(*args.Note); err != nil {
			return nil, err
		}
		params.Set("note", *args.Note)
	}
	if args.Starred != nil {
		params.Set("starred", flagValue(args.Starred))
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	// Annotations.save reads the pair itself and refuses an ID the website does not have, so no read comes first.
	var answer json.RawMessage
	if err := client.change(ctx, op, annotationsRequirement(annotationsUpdate.ID), "Annotations.save", params, annotationSentOnce, &answer); err != nil {
		return nil, err
	}
	updated, ok := readAnnotation(answer, id)
	if !ok || updated.IDNote != noteID {
		return nil, unclearChange(op, "Matomo returned no usable annotation", annotationSentOnce)
	}
	return &AnnotationUpdated{IDSite: id, Annotation: updated, Updated: true}, nil
}
