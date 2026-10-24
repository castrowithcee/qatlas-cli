package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const deletedAnnotationOutput = `{"type":"object","properties":{"idSite":{"type":"integer"},"idNote":{"type":"integer"},` +
	`"deleted":{"type":"boolean"}},"required":["idSite","idNote","deleted"],"additionalProperties":false}`

var annotationsDelete = capability.Descriptor{
	ID: Provider + ".annotations.delete", Version: 1, Title: "Delete a Matomo annotation",
	Description: "Delete one annotation of a website for good; it cannot be undone. Matomo pairs website and ID itself, " +
		"so an ID the website does not have is not found; it needs an admin right on the website, or the user is " +
		"the author of the annotation; idempotent in effect, sent once and never repeated; offered only where a " +
		"connection's tools list names it",
	Tags: []string{"matomo", "annotations", "notes", "delete"}, Risk: annotationChangeRisk(capability.EffectDelete,
		capability.IdempotencyIdempotent), Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idNoteSchema + `},` +
		`"required":["idNote"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(deletedAnnotationOutput),
	Arguments:    []capability.Argument{idSiteArgument(), idNoteArgument("delete")},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the annotation belonged to"},
		{Name: "idNote", Description: "ID of the deleted annotation"},
		{Name: "deleted", Description: "True once Matomo deleted the annotation"},
	},
	Examples: []capability.Example{{Description: "Delete annotation 4 of website 1", Arguments: json.RawMessage(`{"idSite":1,"idNote":4}`)}},
}

var annotationsDeleteAll = capability.Descriptor{
	ID: Provider + ".annotations.delete-all", Version: 1, Title: "Delete all Matomo annotations of a website",
	Description: "Delete every annotation of one website for good, whoever wrote it; it needs Super User rights and " +
		"cannot be undone; idempotent in effect, sent once and never repeated; offered only where a connection's " +
		"tools list names it and its targets name the instance",
	Tags: []string{"matomo", "annotations", "notes", "delete"}, Risk: annotationChangeRisk(capability.EffectDelete,
		capability.IdempotencyIdempotent), Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},"deleted":{"type":"boolean"}},` +
		`"required":["idSite","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument()},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website whose annotations were deleted"},
		{Name: "deleted", Description: "True once Matomo deleted the annotations"},
	},
	Examples: []capability.Example{{Description: "Delete every annotation of website 1", Arguments: json.RawMessage(`{"idSite":1}`)}},
}

var annotationsCount = capability.Descriptor{
	ID: Provider + ".annotations.count", Version: 1, Title: "Count Matomo annotations",
	Description: "Count the annotations of one website per period of a date range, and how many are starred; the note " +
		"text is added for a period with exactly one annotation when asked; notes are untrusted",
	Tags: []string{"matomo", "annotations", "notes", "count"}, Risk: readRisk(annotationsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` +
		`"date":{"type":"string","minLength":1,"maxLength":32},"period":{"type":"string","enum":` +
		enumJSON(annotationPeriods) + `},"lastN":{"type":"integer","minimum":1,"maximum":1000},` +
		`"getAnnotationText":{"type":"boolean"}},"required":["date","period"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},"counts":{"type":"array",` +
		`"items":{"type":"object","properties":{"date":{"type":"string"},"count":{"type":"integer"},` +
		`"starred":{"type":"integer"},"note":{"type":"string"}},"required":["date","count","starred"],` +
		`"additionalProperties":false}}},"required":["idSite","counts"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(),
		{Name: "date", Required: true, Description: "Date of the period: YYYY-MM-DD, today, yesterday, or for period " +
			"range YYYY-MM-DD,YYYY-MM-DD"},
		{Name: "period", Required: true, Description: "Period type of date and of each count: day, week, month, year, " +
			"or range (counted per day)"},
		{Name: "lastN", Description: "Count the last N periods up to date, 1 to 1000; ignored for period range"},
		{Name: "getAnnotationText", Description: "True to add the note text for a period with exactly one annotation; " +
			"false when omitted"},
	},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the counts belong to"},
		{Name: "counts", Description: "One entry per period, in date order"},
		{Name: "counts[].date", Description: "First day of the period, YYYY-MM-DD"},
		{Name: "counts[].count", Description: "Number of annotations in the period"},
		{Name: "counts[].starred", Description: "Number of starred annotations in the period"},
		{Name: "counts[].note", Description: "Text of the only annotation of the period, untrusted; only when asked for " +
			"and the period holds exactly one"},
	},
	Examples: []capability.Example{{Description: "Count the annotations of the last 3 months of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"date":"today","period":"month","lastN":3}`)}},
}

// AnnotationDeleted is the answer of annotations.delete.
type AnnotationDeleted struct {
	IDSite  int  `json:"idSite"`
	IDNote  int  `json:"idNote"`
	Deleted bool `json:"deleted"`
}

// AnnotationsDeleted is the answer of annotations.delete-all.
type AnnotationsDeleted struct {
	IDSite  int  `json:"idSite"`
	Deleted bool `json:"deleted"`
}

// AnnotationCount is the count of one period.
type AnnotationCount struct {
	Date    string `json:"date"`
	Count   int    `json:"count"`
	Starred int    `json:"starred"`
	Note    string `json:"note,omitempty"`
}

// AnnotationCountsResult is the answer of annotations.count.
type AnnotationCountsResult struct {
	IDSite int               `json:"idSite"`
	Counts []AnnotationCount `json:"counts"`
}

func invokeAnnotationsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete annotation"
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
	// Annotations.delete reads the pair itself and refuses an ID the website does not have, so no read comes first.
	params := url.Values{"idSite": {strconv.Itoa(id)}, "idNote": {strconv.Itoa(noteID)}}
	var answer json.RawMessage
	if err := client.change(ctx, op, annotationsRequirement(annotationsDelete.ID), "Annotations.delete", params,
		annotationSentOnce, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the deletion", annotationSentOnce)
	}
	return &AnnotationDeleted{IDSite: id, IDNote: noteID, Deleted: true}, nil
}

func invokeAnnotationsDeleteAll(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete all annotations"
	var args struct {
		IDSite *int `json:"idSite"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	if bound, err := boundScope(resolved); err != nil || !bound.instance {
		if err != nil {
			return nil, err
		}
		return nil, invalidArgument("this tool needs a connection bound to the instance")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(id)}}
	var answer json.RawMessage
	if err := client.change(ctx, op, annotationsRequirement(annotationsDeleteAll.ID), "Annotations.deleteAll", params,
		annotationSentOnce, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the deletion", annotationSentOnce)
	}
	return &AnnotationsDeleted{IDSite: id, Deleted: true}, nil
}

// countRow is one [date, {count, starred, note}] pair of Annotations.getAnnotationCountForDates.
type countRow struct {
	Count   *loose `json:"count"`
	Starred *loose `json:"starred"`
	Note    *loose `json:"note"`
}

func invokeAnnotationsCount(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "count annotations"
	var args struct {
		IDSite            *int   `json:"idSite"`
		Date              string `json:"date"`
		Period            string `json:"period"`
		LastN             *int   `json:"lastN"`
		GetAnnotationText *bool  `json:"getAnnotationText"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	if !annotationListDate.MatchString(args.Date) {
		return nil, invalidArgument("date must be YYYY-MM-DD, today, yesterday, or YYYY-MM-DD,YYYY-MM-DD")
	}
	if !slices.Contains(annotationPeriods, args.Period) {
		return nil, invalidArgument("period must be one of " + strings.Join(annotationPeriods, ", "))
	}
	if args.LastN != nil && (*args.LastN < 1 || *args.LastN > maxLastN) {
		return nil, invalidArgument("lastN must be between 1 and " + strconv.Itoa(maxLastN))
	}
	if strings.Contains(args.Date, ",") != (args.Period == "range") {
		return nil, invalidArgument("a date range goes with the period range, and the period range with a date range")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(id)}, "date": {args.Date}, "period": {args.Period}}
	if args.LastN != nil {
		params.Set("lastN", strconv.Itoa(*args.LastN))
	}
	if args.GetAnnotationText != nil && *args.GetAnnotationText {
		params.Set("getAnnotationText", "1")
	}
	var answer map[string]json.RawMessage
	if err := client.call(ctx, op, annotationsRequirement(annotationsCount.ID), "Annotations.getAnnotationCountForDates",
		params, &answer); err != nil {
		return nil, err
	}
	counts := []AnnotationCount{}
	// Only the requested website's entry is read; any other key is dropped.
	if entry, found := answer[strconv.Itoa(id)]; found {
		var rows []json.RawMessage
		if json.Unmarshal(entry, &rows) != nil || len(rows) > maxLastN+1 {
			return nil, provider.InvalidResponse(op, "Matomo returned an invalid annotation count")
		}
		for _, row := range rows {
			var pair []json.RawMessage
			var detail countRow
			var date loose
			if json.Unmarshal(row, &pair) != nil || len(pair) != 2 || json.Unmarshal(pair[0], &date) != nil ||
				!bytes.HasPrefix(bytes.TrimSpace(pair[1]), []byte("{")) || json.Unmarshal(pair[1], &detail) != nil {
				return nil, provider.InvalidResponse(op, "Matomo returned an invalid annotation count")
			}
			count, starred := detail.Count.number(), detail.Starred.number()
			if count == nil || starred == nil {
				return nil, provider.InvalidResponse(op, "Matomo returned an invalid annotation count")
			}
			day := date.str(maxStringLength)
			if len(day) > 10 {
				day = day[:10]
			}
			counts = append(counts, AnnotationCount{Date: day, Count: *count, Starred: *starred,
				Note: detail.Note.str(maxNoteAnswerLength)})
		}
	}
	return &AnnotationCountsResult{IDSite: id, Counts: counts}, nil
}
