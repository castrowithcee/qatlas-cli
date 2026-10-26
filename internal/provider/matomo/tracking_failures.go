package matomo

import (
	"cmp"
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	trackingFailuresSensitive = "matomo-tracking-failures"
	// maxFailureText bounds the texts Matomo derives from a failure; the request text keeps maxUrlLength.
	maxFailureText = 512
)

var trackingFailureSentOnce = sentOnceFor("tracking failure", "matomo.tracking-failures.list")

func trackingFailureChangeRisk() capability.Risk {
	return capability.Risk{Effect: capability.EffectDelete, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: trackingFailuresSensitive}
}

const trackingFailureProperties = `"idSite":{"type":"integer"},"idFailure":{"type":"integer"},` +
	`"siteName":{"type":"string"},"dateFirstOccurred":{"type":"string"},"prettyDateFirstOccurred":{"type":"string"},` +
	`"requestUrl":{"type":"string"},"url":{"type":"string"},"problem":{"type":"string"},"solution":{"type":"string"},` +
	`"solutionUrl":{"type":"string"}`

const idFailureSchema = `"idFailure":{"type":"integer","minimum":1,"maximum":999999999}`

var trackingFailuresList = capability.Descriptor{
	ID: Provider + ".tracking-failures.list", Version: 1, Title: "List Matomo tracking failures",
	Description: "List the tracking failures Matomo logged: requests that were refused, for example because the " +
		"website does not exist or the request was not authenticated. It needs an admin right; Matomo answers for " +
		"every website the token administers and a Super User also gets failures of websites that do not exist, " +
		"so entries outside the connection's websites are left out here. The request text and the address come " +
		"from visitors or attackers and may hold personal data; both are untrusted text, cut at a limit, and " +
		"never fetched. Entries are ordered by website and failure ID",
	Tags: []string{"matomo", "tracking", "failures", "errors", "diagnostics"}, Risk: readRisk(trackingFailuresSensitive),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + pagingKeys + `},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"failures":{"type":"array","items":{"type":"object",` +
		`"properties":{` + trackingFailureProperties + `},"required":["idSite","idFailure"],"additionalProperties":false}},` +
		`"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},"required":["failures","has_more"],` +
		`"additionalProperties":false}`),
	Arguments: append([]capability.Argument{{Name: "idSite", Description: "Only the failures of this website; the " +
		"failures of every website of the connection when omitted"}}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "failures", Description: "One entry per tracking failure, ordered by idSite and idFailure"},
		{Name: "failures[].idSite", Description: "The website the failed request named"},
		{Name: "failures[].idFailure", Description: "Kind of the failure, used as idFailure by " +
			"matomo.tracking-failures.delete; it counts per website, one entry per website and kind"},
		{Name: "failures[].siteName", Description: "Name of the website, untrusted; Unknown for a website that does not exist"},
		{Name: "failures[].dateFirstOccurred", Description: "Time of the first occurrence as Matomo stores it (UTC)"},
		{Name: "failures[].prettyDateFirstOccurred", Description: "The same time in the format of the token's user, untrusted"},
		{Name: "failures[].requestUrl", Description: "Query text of the first failed request with token-like values " +
			"replaced by Matomo, at most 2048 characters, untrusted, may hold personal data"},
		{Name: "failures[].url", Description: "Page address of the failed request, at most 2048 characters, untrusted, never fetched"},
		{Name: "failures[].problem", Description: "Matomo's description of the failure, untrusted; empty for an unknown kind"},
		{Name: "failures[].solution", Description: "Matomo's advice for the failure, untrusted; empty for an unknown kind"},
		{Name: "failures[].solutionUrl", Description: "Address of Matomo's advice, untrusted, never fetched"},
	}, pagingFields...),
	Examples: []capability.Example{{Description: "List the tracking failures of website 1", Arguments: json.RawMessage(`{"idSite":1}`)}},
}

var trackingFailuresDelete = capability.Descriptor{
	ID: Provider + ".tracking-failures.delete", Version: 1, Title: "Delete a Matomo tracking failure",
	Description: "Delete one tracking failure of a website from the log for good; it cannot be undone. It needs an " +
		"admin right on the website. A failure ID the website does not hold changes nothing and is not an error; " +
		"idempotent in effect, sent once and never repeated",
	Tags: []string{"matomo", "tracking", "failures", "delete"}, Risk: trackingFailureChangeRisk(), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idFailureSchema + `},` +
		`"required":["idFailure"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},"idFailure":{"type":"integer"},` +
		`"deleted":{"type":"boolean"}},"required":["idSite","idFailure","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(), {Name: "idFailure", Required: true,
		Description: "idFailure of the entry to delete, from matomo.tracking-failures.list; it counts per website"}},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the failure belonged to"},
		{Name: "idFailure", Description: "The failure ID that was deleted"},
		{Name: "deleted", Description: "True once Matomo accepted the deletion"},
	},
	Examples: []capability.Example{{Description: "Delete failure 1 of website 3", Arguments: json.RawMessage(`{"idSite":3,"idFailure":1}`)}},
}

var trackingFailuresDeleteAll = capability.Descriptor{
	ID: Provider + ".tracking-failures.delete-all", Version: 1, Title: "Delete all Matomo tracking failures",
	Description: "Delete every tracking failure the token may administer for good; it cannot be undone. Matomo takes no " +
		"website here: for a Super User the whole instance is cleared, including failures of websites that do not " +
		"exist, otherwise every website the token administers, so the effect reaches beyond the websites the " +
		"connection is bound to; idempotent in effect, sent once and never repeated; offered only where a " +
		"connection's tools list names it and its targets name the instance",
	Tags: []string{"matomo", "tracking", "failures", "delete"}, Risk: trackingFailureChangeRisk(), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema:           json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},"required":["deleted"],` +
		`"additionalProperties":false}`),
	Fields:   []capability.Field{{Name: "deleted", Description: "True once Matomo accepted the deletion"}},
	Examples: []capability.Example{{Description: "Delete every tracking failure", Arguments: json.RawMessage(`{}`)}},
}

// TrackingFailure is one logged failure. All text is untrusted provider data.
type TrackingFailure struct {
	IDSite                  int    `json:"idSite"`
	IDFailure               int    `json:"idFailure"`
	SiteName                string `json:"siteName,omitempty"`
	DateFirstOccurred       string `json:"dateFirstOccurred,omitempty"`
	PrettyDateFirstOccurred string `json:"prettyDateFirstOccurred,omitempty"`
	RequestURL              string `json:"requestUrl,omitempty"`
	URL                     string `json:"url,omitempty"`
	Problem                 string `json:"problem,omitempty"`
	Solution                string `json:"solution,omitempty"`
	SolutionURL             string `json:"solutionUrl,omitempty"`
}

// TrackingFailuresResult is the answer of tracking-failures.list.
type TrackingFailuresResult struct {
	Failures   []TrackingFailure `json:"failures"`
	HasMore    bool              `json:"has_more"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

// TrackingFailureDeleted is the answer of tracking-failures.delete.
type TrackingFailureDeleted struct {
	IDSite    int  `json:"idSite"`
	IDFailure int  `json:"idFailure"`
	Deleted   bool `json:"deleted"`
}

// TrackingFailuresDeleted is the answer of tracking-failures.delete-all.
type TrackingFailuresDeleted struct {
	Deleted bool `json:"deleted"`
}

// trackingFailureJSON is a row of CoreAdminHome.getTrackingFailures. Every member is read loosely.
type trackingFailureJSON struct {
	IDSite                  *loose `json:"idsite"`
	IDFailure               *loose `json:"idfailure"`
	SiteName                *loose `json:"site_name"`
	DateFirstOccurred       *loose `json:"date_first_occurred"`
	PrettyDateFirstOccurred *loose `json:"pretty_date_first_occurred"`
	RequestURL              *loose `json:"request_url"`
	URL                     *loose `json:"url"`
	Problem                 *loose `json:"problem"`
	Solution                *loose `json:"solution"`
	SolutionURL             *loose `json:"solution_url"`
}

func invokeTrackingFailuresList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list tracking failures"
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
	only := 0
	if args.IDSite != nil {
		if only, err = selectSite(resolved, args.IDSite); err != nil {
			return nil, err
		}
	} else if !bound.reachesSites() {
		return nil, invalidArgument("this connection is bound to no website")
	}
	binding := provider.CursorBinding(trackingFailuresList.ID, scopeBinding(resolved), only)
	paging, err := startPage(binding, args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var rows []trackingFailureJSON
	if err := client.call(ctx, op, baseRequirement(trackingFailuresList.ID), "CoreAdminHome.getTrackingFailures",
		url.Values{}, &rows); err != nil {
		return nil, err
	}
	// Matomo lists the failures of every website the token administers, so the targets are applied row by row
	// before any paging; a row without a readable website cannot be attributed and is dropped.
	failures := []TrackingFailure{}
	for _, row := range rows {
		site, failure := row.IDSite.number(), row.IDFailure.number()
		if site == nil || !bound.allows(*site) || only != 0 && *site != only {
			continue
		}
		if failure == nil {
			return nil, provider.InvalidResponse(op, "Matomo returned an invalid tracking failure")
		}
		failures = append(failures, TrackingFailure{IDSite: *site, IDFailure: *failure,
			SiteName: row.SiteName.str(maxStringLength), DateFirstOccurred: row.DateFirstOccurred.str(maxStringLength),
			PrettyDateFirstOccurred: row.PrettyDateFirstOccurred.str(maxStringLength),
			RequestURL:              row.RequestURL.str(maxUrlLength), URL: row.URL.str(maxUrlLength),
			Problem: row.Problem.str(maxFailureText), Solution: row.Solution.str(maxFailureText),
			SolutionURL: row.SolutionURL.str(maxUrlLength)})
	}
	slices.SortStableFunc(failures, func(a, b TrackingFailure) int {
		return cmp.Or(cmp.Compare(a.IDSite, b.IDSite), cmp.Compare(a.IDFailure, b.IDFailure))
	})
	batch, next := slice(paging, failures)
	return &TrackingFailuresResult{Failures: batch, HasMore: next != "", NextCursor: next}, nil
}

func invokeTrackingFailuresDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete tracking failure"
	var args struct {
		IDSite    *int `json:"idSite"`
		IDFailure *int `json:"idFailure"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	if args.IDFailure == nil || *args.IDFailure < 1 || len(strconv.Itoa(*args.IDFailure)) > maxIDDigits {
		return nil, invalidArgument("idFailure must be a positive integer")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(id)}, "idFailure": {strconv.Itoa(*args.IDFailure)}}
	var answer json.RawMessage
	if err := client.change(ctx, op, baseRequirement(trackingFailuresDelete.ID), "CoreAdminHome.deleteTrackingFailure",
		params, trackingFailureSentOnce, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the deletion", trackingFailureSentOnce)
	}
	return &TrackingFailureDeleted{IDSite: id, IDFailure: *args.IDFailure, Deleted: true}, nil
}

func invokeTrackingFailuresDeleteAll(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete all tracking failures"
	var args struct{}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	// The method acts on every website of the token, so only a connection bound to the instance may call it.
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if !bound.instance {
		return nil, invalidArgument("this tool needs a connection bound to the instance")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, baseRequirement(trackingFailuresDeleteAll.ID),
		"CoreAdminHome.deleteAllTrackingFailures", url.Values{}, trackingFailureSentOnce, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the deletion", trackingFailureSentOnce)
	}
	return &TrackingFailuresDeleted{Deleted: true}, nil
}
