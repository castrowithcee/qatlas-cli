package matomo

import (
	"bytes"
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
	// experimentsSensitive covers the names, hypotheses, target rules, and addresses people wrote into experiments.
	experimentsSensitive = "matomo-experiments"
	experimentsPlugin    = "AbTesting"
	maxExperimentRows    = 100
	maxExperimentID      = 2147483647
	maxExperimentStates  = 10
	// maxSnippetText is Qatlas's own limit for a JavaScript snippet; the reference states none.
	maxSnippetText = 16384
)

func experimentsRequirement(tool string) requirement {
	return requirement{tool: tool, plugin: experimentsPlugin}
}

const (
	idExperimentSchema = `"idExperiment":{"type":"integer","minimum":1,"maximum":2147483647}`
	targetProp         = `{"type":"array","items":{"type":"object","properties":{"attribute":{"type":"string"},` +
		`"inverted":{"type":"boolean"},"type":{"type":"string"},"value":{"type":"string"}},"additionalProperties":false}}`
	experimentProps = `"idExperiment":{"type":"integer"},"idSite":{"type":"integer"},` +
		`"confidence_threshold":{"type":"number"},"mde_relative":{"type":"number"},"name":{"type":"string"},` +
		`"description":{"type":"string"},"hypothesis":{"type":"string"},"included_targets":` + targetProp + `,` +
		`"excluded_targets":` + targetProp + `,"success_metrics":{"type":"array","items":{"type":"string"}},` +
		`"percentage_participants":{"type":"number"},"original_redirect_url":{"type":"string"},` +
		`"status":{"type":"string"},"start_date":{"type":"string"},"modified_date":{"type":"string"},` +
		`"end_date":{"type":"string"},"forward_utm_params":{"type":"boolean"},` +
		`"forward_all_query_params":{"type":"boolean"},"variations":{"type":"array","items":{"type":"object",` +
		`"properties":{"idvariation":{"type":"integer"},"name":{"type":"string"},"percentage":{"type":"number"},` +
		`"redirect_url":{"type":"string"}},"additionalProperties":false}},` +
		`"start_date_site_timezone":{"type":"string"},"end_date_site_timezone":{"type":"string"},` +
		`"date_range_string":{"type":"string"},"duration":{"type":"string"}`
)

func experimentFields(prefix string) []capability.Field {
	return []capability.Field{
		{Name: prefix + "idExperiment", Description: "Experiment ID, used as idExperiment by the other experiment tools"},
		{Name: prefix + "idSite", Description: "The website the experiment belongs to"},
		{Name: prefix + "confidence_threshold", Description: "Confidence threshold in percent"},
		{Name: prefix + "mde_relative", Description: "Minimum detectable effect, relative, in percent"},
		{Name: prefix + "name", Description: "Experiment name, untrusted"},
		{Name: prefix + "description", Description: "Experiment description, untrusted"},
		{Name: prefix + "hypothesis", Description: "Hypothesis of the experiment, untrusted"},
		{Name: prefix + "included_targets", Description: "Rules that select the visits taking part, at most 100, " +
			"untrusted; each holds attribute, inverted, type, and value (text that is never fetched)"},
		{Name: prefix + "excluded_targets", Description: "Rules that exclude visits, at most 100, untrusted; same " +
			"members as the included targets"},
		{Name: prefix + "success_metrics", Description: "Metrics the experiment is judged by, at most 100, such as " +
			"nb_conversions; the values come from matomo.experiment-options.list when the token may read them"},
		{Name: prefix + "percentage_participants", Description: "Share of the visits that take part, in percent"},
		{Name: prefix + "original_redirect_url", Description: "Redirect address of the original, untrusted text that is never fetched"},
		{Name: prefix + "status", Description: "Status of the experiment, such as running or archived; the values come " +
			"from matomo.experiment-options.list when the token may read them"},
		{Name: prefix + "start_date", Description: "Time the experiment started as Matomo stores it"},
		{Name: prefix + "modified_date", Description: "Time the experiment was last changed as Matomo stores it"},
		{Name: prefix + "end_date", Description: "Time the experiment ended as Matomo stores it; absent while it runs"},
		{Name: prefix + "forward_utm_params", Description: "Whether UTM parameters are forwarded on a redirect"},
		{Name: prefix + "forward_all_query_params", Description: "Whether all query parameters are forwarded on a redirect"},
		{Name: prefix + "variations", Description: "Variations of the experiment, at most 100, untrusted: idvariation, " +
			"name, percentage, and redirect_url (text that is never fetched)"},
		{Name: prefix + "start_date_site_timezone", Description: "Start time in the time zone of the website"},
		{Name: prefix + "end_date_site_timezone", Description: "End time in the time zone of the website"},
		{Name: prefix + "date_range_string", Description: "Dates of the experiment as a range, as Matomo writes it"},
		{Name: prefix + "duration", Description: "Duration of the experiment as Matomo writes it, such as 45 days and 1 hour"},
	}
}

var experimentsList = capability.Descriptor{
	ID: Provider + ".experiments.list", Version: 1, Title: "List Matomo A/B test experiments",
	Description: "List the A/B test experiments of a website with their targets, success metrics, and variations, in " +
		"batches. Without further arguments Matomo lists all experiments (getAllExperiments); with active true only " +
		"the active ones (getActiveExperiments); with statuses only the experiments of these statuses " +
		"(getExperimentsByStatuses). Active and statuses cannot be combined. Names, hypotheses, targets, and " +
		"variations are untrusted. The reports of the experiments are read with matomo.reports.get. Needs the " +
		"premium plugin AbTesting and a right on the website",
	Tags: []string{"matomo", "experiments", "ab-testing", "list"}, Risk: readRisk(experimentsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` +
		`"active":{"type":"boolean","enum":[true]},` +
		`"statuses":{"type":"array","minItems":1,"maxItems":10,"uniqueItems":true,"items":{"type":"string",` +
		`"pattern":"` + formStatusPattern.String() + `"}},` +
		pagingKeys + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"experiments":{"type":"array","items":{"type":"object","properties":{` + experimentProps + `},` +
		`"required":["idExperiment","idSite"],"additionalProperties":false}},"has_more":{"type":"boolean"},` +
		`"next_cursor":{"type":"string"}},"required":["idSite","experiments","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument(),
		{Name: "active", Description: "true to list only the active experiments; cannot be combined with statuses"},
		{Name: "statuses", Description: "1 to 10 different statuses to list, such as running; the values come from " +
			"matomo.experiment-options.list; cannot be combined with active"}},
		pagingArguments...),
	Fields: append(append([]capability.Field{
		{Name: "idSite", Description: "The website the experiments belong to"},
		{Name: "experiments", Description: "Experiments in the order Matomo lists them; empty without one"}},
		experimentFields("experiments[].")...), pagingFields...),
	Examples: []capability.Example{
		{Description: "Experiments of website 1", Arguments: json.RawMessage(`{"idSite":1}`)},
		{Description: "Active experiments of website 1", Arguments: json.RawMessage(`{"idSite":1,"active":true}`)},
		{Description: "Running and finished experiments of website 1", Arguments: json.RawMessage(
			`{"idSite":1,"statuses":["running","finished"]}`)}},
}

var experimentsGet = capability.Descriptor{
	ID: Provider + ".experiments.get", Version: 1, Title: "Get a Matomo A/B test experiment",
	Description: "Read the definition of one A/B test experiment of a website by its ID. The experiment ID counts " +
		"across websites, so the answer is shown only when Matomo reports it for this website, otherwise it is not " +
		"found. Names, hypotheses, targets, and variations are untrusted. Needs the premium plugin AbTesting and a " +
		"right on the website",
	Tags: []string{"matomo", "experiments", "ab-testing", "get"}, Risk: readRisk(experimentsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idExperimentSchema + `},` +
		`"required":["idExperiment"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + experimentProps + `},` +
		`"required":["idExperiment","idSite"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(),
		{Name: "idExperiment", Required: true, Description: "ID of the experiment, from matomo.experiments.list"}},
	Fields: experimentFields(""),
	Examples: []capability.Example{{Description: "Experiment 1 of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"idExperiment":1}`)}},
}

// ExperimentTarget is one targeting rule of an experiment. All text is untrusted provider data.
type ExperimentTarget struct {
	Attribute string `json:"attribute,omitempty"`
	Inverted  *bool  `json:"inverted,omitempty"`
	Type      string `json:"type,omitempty"`
	Value     string `json:"value,omitempty"`
}

// ExperimentVariation is one variation of an experiment. All text is untrusted provider data.
type ExperimentVariation struct {
	IDVariation *int     `json:"idvariation,omitempty"`
	Name        string   `json:"name,omitempty"`
	Percentage  *float64 `json:"percentage,omitempty"`
	RedirectURL string   `json:"redirect_url,omitempty"`
}

// Experiment is an A/B test experiment of a website. All text is untrusted provider data.
type Experiment struct {
	IDExperiment          int                   `json:"idExperiment"`
	IDSite                int                   `json:"idSite"`
	ConfidenceThreshold   *float64              `json:"confidence_threshold,omitempty"`
	MdeRelative           *float64              `json:"mde_relative,omitempty"`
	Name                  string                `json:"name,omitempty"`
	Description           string                `json:"description,omitempty"`
	Hypothesis            string                `json:"hypothesis,omitempty"`
	IncludedTargets       []ExperimentTarget    `json:"included_targets,omitempty"`
	ExcludedTargets       []ExperimentTarget    `json:"excluded_targets,omitempty"`
	SuccessMetrics        []string              `json:"success_metrics,omitempty"`
	PercentageParticipant *float64              `json:"percentage_participants,omitempty"`
	OriginalRedirectURL   string                `json:"original_redirect_url,omitempty"`
	Status                string                `json:"status,omitempty"`
	StartDate             string                `json:"start_date,omitempty"`
	ModifiedDate          string                `json:"modified_date,omitempty"`
	EndDate               string                `json:"end_date,omitempty"`
	ForwardUTMParams      *bool                 `json:"forward_utm_params,omitempty"`
	ForwardAllQueryParams *bool                 `json:"forward_all_query_params,omitempty"`
	Variations            []ExperimentVariation `json:"variations,omitempty"`
	StartDateSiteTimezone string                `json:"start_date_site_timezone,omitempty"`
	EndDateSiteTimezone   string                `json:"end_date_site_timezone,omitempty"`
	DateRangeString       string                `json:"date_range_string,omitempty"`
	Duration              string                `json:"duration,omitempty"`
}

// ExperimentsResult is the answer of experiments.list.
type ExperimentsResult struct {
	IDSite      int          `json:"idSite"`
	Experiments []Experiment `json:"experiments"`
	HasMore     bool         `json:"has_more"`
	NextCursor  string       `json:"next_cursor,omitempty"`
}

type experimentTargetJSON struct {
	Attribute *loose `json:"attribute"`
	Inverted  *loose `json:"inverted"`
	Type      *loose `json:"type"`
	Value     *loose `json:"value"`
}

type experimentVariationJSON struct {
	ID          *loose `json:"idvariation"`
	Name        *loose `json:"name"`
	Percentage  *loose `json:"percentage"`
	RedirectURL *loose `json:"redirect_url"`
}

type experimentMetricJSON struct {
	Metric *loose `json:"metric"`
}

// experimentJSON is an experiment as AbTesting reports it. Members are read loosely and none is trusted.
type experimentJSON struct {
	ID                    *loose          `json:"idexperiment"`
	IDSite                *loose          `json:"idsite"`
	ConfidenceThreshold   *loose          `json:"confidence_threshold"`
	MdeRelative           *loose          `json:"mde_relative"`
	Name                  *loose          `json:"name"`
	Description           *loose          `json:"description"`
	Hypothesis            *loose          `json:"hypothesis"`
	IncludedTargets       json.RawMessage `json:"included_targets"`
	ExcludedTargets       json.RawMessage `json:"excluded_targets"`
	SuccessMetrics        json.RawMessage `json:"success_metrics"`
	PercentageParticipant *loose          `json:"percentage_participants"`
	OriginalRedirectURL   *loose          `json:"original_redirect_url"`
	Status                *loose          `json:"status"`
	StartDate             *loose          `json:"start_date"`
	ModifiedDate          *loose          `json:"modified_date"`
	EndDate               *loose          `json:"end_date"`
	ForwardUTMParams      *loose          `json:"forward_utm_params"`
	ForwardAllQueryParams *loose          `json:"forward_all_query_params"`
	Variations            json.RawMessage `json:"variations"`
	StartDateSiteTimezone *loose          `json:"start_date_site_timezone"`
	EndDateSiteTimezone   *loose          `json:"end_date_site_timezone"`
	DateRangeString       *loose          `json:"date_range_string"`
	Duration              *loose          `json:"duration"`
}

func experimentTargets(raw json.RawMessage) []ExperimentTarget {
	rows, valid := readRows[experimentTargetJSON](raw)
	if !valid {
		return nil
	}
	var targets []ExperimentTarget
	for _, row := range rows {
		if len(targets) == maxExperimentRows {
			break
		}
		targets = append(targets, ExperimentTarget{Attribute: row.Attribute.str(maxStringLength),
			Inverted: row.Inverted.flag(), Type: row.Type.str(maxStringLength), Value: row.Value.str(maxUrlLength)})
	}
	return targets
}

func experimentMetrics(raw json.RawMessage) []string {
	rows, valid := readRows[experimentMetricJSON](raw)
	if !valid {
		return nil
	}
	var metrics []string
	for _, row := range rows {
		if metric := row.Metric.str(maxStringLength); metric != "" && len(metrics) < maxExperimentRows {
			metrics = append(metrics, metric)
		}
	}
	return metrics
}

func experimentVariations(raw json.RawMessage) []ExperimentVariation {
	rows, valid := readRows[experimentVariationJSON](raw)
	if !valid {
		return nil
	}
	var variations []ExperimentVariation
	for _, row := range rows {
		if len(variations) == maxExperimentRows {
			break
		}
		variations = append(variations, ExperimentVariation{IDVariation: row.ID.number(), Name: row.Name.str(maxStringLength),
			Percentage: row.Percentage.numeric(), RedirectURL: row.RedirectURL.str(maxUrlLength)})
	}
	return variations
}

// experiment converts a row. belongs is false for a row of another website and for a row without a website; ok
// is false for a row without a usable experiment ID.
func (e experimentJSON) experiment(site int) (experiment Experiment, belongs, ok bool) {
	id := e.ID.number()
	if id == nil || *id < 1 {
		return Experiment{}, false, false
	}
	if row := e.IDSite.number(); row == nil || *row != site {
		return Experiment{}, false, true
	}
	return Experiment{IDExperiment: *id, IDSite: site, ConfidenceThreshold: e.ConfidenceThreshold.numeric(),
		MdeRelative: e.MdeRelative.numeric(), Name: e.Name.str(maxStringLength), Description: e.Description.str(maxUrlLength),
		Hypothesis: e.Hypothesis.str(maxUrlLength), IncludedTargets: experimentTargets(e.IncludedTargets),
		ExcludedTargets: experimentTargets(e.ExcludedTargets), SuccessMetrics: experimentMetrics(e.SuccessMetrics),
		PercentageParticipant: e.PercentageParticipant.numeric(), OriginalRedirectURL: e.OriginalRedirectURL.str(maxUrlLength),
		Status: e.Status.str(maxStringLength), StartDate: e.StartDate.str(maxStringLength),
		ModifiedDate: e.ModifiedDate.str(maxStringLength), EndDate: e.EndDate.str(maxStringLength),
		ForwardUTMParams: e.ForwardUTMParams.flag(), ForwardAllQueryParams: e.ForwardAllQueryParams.flag(),
		Variations: experimentVariations(e.Variations), StartDateSiteTimezone: e.StartDateSiteTimezone.str(maxStringLength),
		EndDateSiteTimezone: e.EndDateSiteTimezone.str(maxStringLength), DateRangeString: e.DateRangeString.str(maxStringLength),
		Duration: e.Duration.str(maxStringLength)}, true, true
}

func experimentNotFound(op string) error {
	return &provider.Error{Class: provider.ClassNotFound, Op: op,
		Message: "Matomo has no such experiment for this website inside the targets of this connection"}
}

// unwrapValue returns the scalar Matomo wraps as {"value":...} in its JSON form; any other answer is unchanged.
func unwrapValue(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	var wrapper map[string]json.RawMessage
	if bytes.HasPrefix(trimmed, []byte("{")) && json.Unmarshal(trimmed, &wrapper) == nil && len(wrapper) == 1 {
		if value, found := wrapper["value"]; found {
			return bytes.TrimSpace(value)
		}
	}
	return trimmed
}

// fetchExperiment reads one experiment. The answer carries idsite, and an experiment of another website, or one
// without it, is never shown: an experiment ID is not tied to a website in the request, so the ID alone proves
// nothing.
func (c *Client) fetchExperiment(ctx context.Context, op string, req requirement, site, id int) (Experiment, error) {
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idExperiment": {strconv.Itoa(id)}}
	var answer json.RawMessage
	if err := c.call(ctx, op, req, "AbTesting.getExperiment", params, &answer); err != nil {
		return Experiment{}, err
	}
	trimmed := unwrapValue(answer)
	switch string(trimmed) {
	case "null", "false", "[]", "{}", "":
		return Experiment{}, experimentNotFound(op)
	}
	var row experimentJSON
	if !bytes.HasPrefix(trimmed, []byte("{")) || json.Unmarshal(trimmed, &row) != nil {
		return Experiment{}, provider.InvalidResponse(op, "Matomo returned an invalid experiment")
	}
	experiment, belongs, ok := row.experiment(site)
	if !ok {
		return Experiment{}, provider.InvalidResponse(op, "Matomo returned an invalid experiment")
	}
	if !belongs {
		return Experiment{}, experimentNotFound(op)
	}
	if experiment.IDExperiment != id {
		return Experiment{}, provider.InvalidResponse(op, "Matomo returned another experiment than asked for")
	}
	return experiment, nil
}

func invokeExperimentsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list experiments"
	var args struct {
		IDSite   *int     `json:"idSite"`
		Active   *bool    `json:"active"`
		Statuses []string `json:"statuses"`
		Limit    int      `json:"limit"`
		Cursor   string   `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	if args.Active != nil && !*args.Active {
		return nil, invalidArgument("active must be true or omitted")
	}
	if args.Active != nil && args.Statuses != nil {
		return nil, invalidArgument("active and statuses cannot be combined")
	}
	if args.Statuses != nil && (len(args.Statuses) == 0 || len(args.Statuses) > maxExperimentStates) {
		return nil, invalidArgument("statuses must hold 1 to " + strconv.Itoa(maxExperimentStates) + " statuses")
	}
	for i, status := range args.Statuses {
		if !formStatusPattern.MatchString(status) || slices.Index(args.Statuses, status) != i {
			return nil, invalidArgument("statuses must be different statuses such as running (matomo.experiment-options.list)")
		}
	}
	paging, err := startPage(provider.CursorBinding(experimentsList.ID, scopeBinding(resolved), site, args.Active != nil,
		args.Statuses), args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}}
	method := "AbTesting.getAllExperiments"
	switch {
	case args.Active != nil:
		method = "AbTesting.getActiveExperiments"
	case len(args.Statuses) > 0:
		method = "AbTesting.getExperimentsByStatuses"
		for _, status := range args.Statuses {
			params.Add("statuses[]", status)
		}
	}
	var answer json.RawMessage
	if err := client.call(ctx, op, experimentsRequirement(experimentsList.ID), method, params, &answer); err != nil {
		return nil, err
	}
	rows, valid := readRows[experimentJSON](unwrapValue(answer))
	if !valid {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid experiment list")
	}
	experiments := []Experiment{}
	for i := range rows {
		if experiment, belongs, usable := rows[i].experiment(site); usable && belongs {
			experiments = append(experiments, experiment)
		}
	}
	batch, next := slice(paging, experiments)
	return &ExperimentsResult{IDSite: site, Experiments: batch, HasMore: next != "", NextCursor: next}, nil
}

func invokeExperimentsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get experiment"
	var args struct {
		IDSite       *int `json:"idSite"`
		IDExperiment *int `json:"idExperiment"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if args.IDExperiment == nil || *args.IDExperiment < 1 || *args.IDExperiment > maxExperimentID {
		return nil, invalidArgument("idExperiment must be a positive integer experiment ID from matomo.experiments.list")
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	experiment, err := client.fetchExperiment(ctx, op, experimentsRequirement(experimentsGet.ID), site, *args.IDExperiment)
	if err != nil {
		return nil, err
	}
	return &experiment, nil
}

func experimentsOperations() []capability.Operation {
	return append(append([]capability.Operation{
		{Descriptor: experimentsList, Handler: capability.Handler(invokeExperimentsList)},
		{Descriptor: experimentsGet, Handler: capability.Handler(invokeExperimentsGet)},
	}, append(experimentOptionsOperations(), experimentTemplatesOperations()...)...), experimentChangeOperations()...)
}
