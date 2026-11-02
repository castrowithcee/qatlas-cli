package matomo

import (
	"context"
	"encoding/json"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxExperimentName = 255
	// experimentDateLayout is the UTC form the reference names for startDate and endDate.
	experimentDateLayout = "2006-01-02 15:04:05"
)

var (
	experimentSentOnceList = sentOnceFor("experiment", "matomo.experiments.list")
	experimentSentOnceGet  = sentOnceFor("experiment", "matomo.experiments.get")
	// experimentMetricPattern is Qatlas's own narrow form for a success metric such as nb_conversions.
	experimentMetricPattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,100}$`)
)

// experimentChangeRisk is the risk of every experiment change: confirmed, open to the instance, and about the
// experiment configuration of a website.
func experimentChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: experimentsSensitive}
}

const (
	experimentTextSchema = `"name":{"type":"string","minLength":1,"maxLength":255},` +
		`"description":{"type":"string","minLength":1,"maxLength":2048},` +
		`"hypothesis":{"type":"string","minLength":1,"maxLength":2048}`
	experimentVariationsSchema = `"variations":{"type":"array","minItems":1,"maxItems":100,"items":{"type":"object",` +
		`"properties":{"name":{"type":"string","minLength":1,"maxLength":255},` +
		`"percentage":{"type":"integer","minimum":1,"maximum":100},` +
		`"redirect_url":{"type":"string","maxLength":2048}},"required":["name"],"additionalProperties":false}}`
	experimentTargetsItem = `{"type":"array","maxItems":100,"items":{"type":"object","properties":{` +
		`"attribute":{"type":"string","pattern":"` + patternTypePattern + `"},` +
		`"type":{"type":"string","pattern":"` + patternTypePattern + `"},` +
		`"value":{"type":"string","maxLength":2048},"inverted":{"type":"boolean"}},` +
		`"required":["attribute","type","value"],"additionalProperties":false}}`
	experimentIncludedSchema = `"includedTargets":{"type":"array","minItems":1,"maxItems":100,"items":` +
		`{"type":"object","properties":{"attribute":{"type":"string","pattern":"` + patternTypePattern + `"},` +
		`"type":{"type":"string","pattern":"` + patternTypePattern + `"},` +
		`"value":{"type":"string","maxLength":2048},"inverted":{"type":"boolean"}},` +
		`"required":["attribute","type","value"],"additionalProperties":false}}`
	experimentMetricsSchema = `"successMetrics":{"type":"array","minItems":1,"maxItems":100,"uniqueItems":true,` +
		`"items":{"type":"string","pattern":"^[A-Za-z0-9_]{1,100}$"}}`
	experimentExcludedSchema = `"excludedTargets":` + experimentTargetsItem
	experimentDateSchema     = `{"type":"string","pattern":"^[0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2}$"}`
	experimentUpdateSchema   = `"confidenceThreshold":{"type":"number","enum":[90,95,98,99,99.5]},` +
		`"mdeRelative":{"type":"integer","minimum":1,"maximum":100},` +
		`"percentageParticipants":{"type":"integer","minimum":1,"maximum":100},` + experimentExcludedSchema + `,` +
		`"startDate":` + experimentDateSchema + `,"endDate":` + experimentDateSchema + `,` +
		`"forwardUtmParams":{"type":"boolean"},"forwardAllQueryParams":{"type":"boolean"}`

	experimentFormatHint = "variations are objects with name and optionally percentage (1 to 100) and redirect_url; " +
		"targets are objects with attribute, type, value (may be empty for type any), and optionally inverted; " +
		"all of this follows the AbTesting reference, which states no member names and is not verified"
)

func experimentDefinitionArguments(full bool) []capability.Argument {
	given := ""
	if full {
		given = "; replaces the stored value"
	}
	return []capability.Argument{
		{Name: "name", Required: true, Description: "Name of the experiment, 1 to 255 characters" + given},
		{Name: "description", Required: true, Description: "Description shown in Matomo, 1 to 2048 characters" + given},
		{Name: "hypothesis", Required: true, Description: "The hypothesis to test, 1 to 2048 characters" + given},
		{Name: "variations", Required: true, Description: "1 to 100 variations, each an object with name (1 to 255 " +
			"characters) and optionally percentage (integer 1 to 100) and redirect_url (up to 2048 characters; text that " +
			"Qatlas never fetches)" + given},
		{Name: "includedTargets", Required: true, Description: "1 to 100 rules that must match for the experiment to " +
			"run, each an object with attribute and type (lowercase letters and underscores, values from " +
			"matomo.experiment-options.list), value (up to 2048 characters, may be empty), and optionally inverted" + given},
		{Name: "successMetrics", Required: true, Description: "1 to 100 different metrics the experiment is judged by, " +
			"such as nb_conversions (letters, digits, underscores; values from matomo.experiment-options.list)" + given}}
}

var experimentsAdd = capability.Descriptor{
	ID: Provider + ".experiments.add", Version: 1, Title: "Create a Matomo A/B test experiment",
	Description: "Create an A/B test experiment on a website with name, description, hypothesis, variations, targeting " +
		"rules, and success metrics; all are required. Only these arguments exist for creating: confidence, effect size, " +
		"share of participants, excluded targets, dates, and parameter forwarding are set with " +
		"matomo.experiments.update. The experiment is not started by this tool. " + experimentFormatHint +
		". A repeated call creates a second experiment. Needs the premium plugin AbTesting and, expected, a write right on " +
		"the website" + experimentSentOnceList,
	Tags: []string{"matomo", "experiments", "ab-testing", "create"}, Risk: experimentChangeRisk(capability.EffectCreate,
		capability.IdempotencyNonIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + experimentTextSchema + `,` +
		experimentVariationsSchema + `,` + experimentIncludedSchema + `,` + experimentMetricsSchema + `},` +
		`"required":["name","description","hypothesis","variations","includedTargets","successMetrics"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idExperiment":{"type":"integer"},"created":{"type":"boolean"}},` +
		`"required":["idSite","idExperiment","created"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument()}, experimentDefinitionArguments(false)...),
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the experiment belongs to"},
		{Name: "idExperiment", Description: "ID of the new experiment, used as idExperiment by the other experiment tools"},
		{Name: "created", Description: "True once Matomo created the experiment"}},
	Examples: []capability.Example{{Description: "Pricing page test on website 1", Arguments: json.RawMessage(
		`{"idSite":1,"name":"Pricing page signup test","hypothesis":"A shorter signup flow will improve conversions.",` +
			`"description":"Compare two signup flows","variations":[{"name":"Short","percentage":50,` +
			`"redirect_url":"https://example.org/signup-short"}],"includedTargets":[{"attribute":"url","type":"any",` +
			`"value":""}],"successMetrics":["nb_conversions"]}`)}},
}

var experimentsUpdate = capability.Descriptor{
	ID: Provider + ".experiments.update", Version: 1, Title: "Replace the definition of a Matomo A/B test experiment",
	Description: "Replace the definition of an A/B test experiment of a website as a whole: name, description, hypothesis, " +
		"variations, includedTargets, successMetrics, confidenceThreshold, mdeRelative, and percentageParticipants are " +
		"required; excludedTargets, startDate, endDate, forwardUtmParams, and forwardAllQueryParams are sent only when " +
		"given, so Matomo applies its own defaults to them (by its signature: no excluded targets, no dates, forwarding " +
		"off). Nothing is filled in from the stored experiment, so a value left out may reset what it had; read the " +
		"experiment with matomo.experiments.get and pass everything to keep. " + experimentFormatHint + ". The " +
		"experiment is read first and must belong to the website, otherwise it is not found. Starting, finishing, and " +
		"archiving are not offered. Needs the premium plugin AbTesting and, expected, a write right on the website; " +
		"idempotent in effect" + experimentSentOnceGet,
	Tags: []string{"matomo", "experiments", "ab-testing", "update"}, Risk: experimentChangeRisk(capability.EffectUpdate,
		capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idExperimentSchema + `,` +
		experimentTextSchema + `,` + experimentVariationsSchema + `,` + experimentIncludedSchema + `,` +
		experimentMetricsSchema + `,` + experimentUpdateSchema + `},` +
		`"required":["idExperiment","name","description","hypothesis","variations","includedTargets","successMetrics",` +
		`"confidenceThreshold","mdeRelative","percentageParticipants"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idExperiment":{"type":"integer"},"saved":{"type":"boolean"}},` +
		`"required":["idSite","idExperiment","saved"],"additionalProperties":false}`),
	Arguments: append(append([]capability.Argument{idSiteArgument(),
		{Name: "idExperiment", Required: true, Description: "ID of the experiment to replace, from matomo.experiments.list"}},
		experimentDefinitionArguments(true)...), []capability.Argument{
		{Name: "confidenceThreshold", Required: true, Description: "Confidence threshold in percent: 90, 95, 98, 99, or 99.5"},
		{Name: "mdeRelative", Required: true, Description: "Minimum detectable effect in percent, integer 1 to 100"},
		{Name: "percentageParticipants", Required: true, Description: "Share of the visitors that take part in percent, integer 1 to 100"},
		{Name: "excludedTargets", Description: "Up to 100 rules that exclude visitors, same members as includedTargets; " +
			"Matomo's default (none) when omitted"},
		{Name: "startDate", Description: "UTC start date as 'YYYY-MM-DD HH:MM:SS'; Matomo's default (no scheduled start) when omitted"},
		{Name: "endDate", Description: "UTC end date as 'YYYY-MM-DD HH:MM:SS'; Matomo's default (no scheduled end) when omitted"},
		{Name: "forwardUtmParams", Description: "True to forward utm* query parameters on redirects; Matomo's default (false) when omitted"},
		{Name: "forwardAllQueryParams", Description: "True to forward all query parameters on redirects; Matomo's default (false) when omitted"}}...),
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the experiment belongs to"},
		{Name: "idExperiment", Description: "The experiment that was replaced"},
		{Name: "saved", Description: "True once Matomo accepted the definition"}},
	Examples: []capability.Example{{Description: "Replace experiment 4 of website 1", Arguments: json.RawMessage(
		`{"idSite":1,"idExperiment":4,"name":"Banner","description":"Banner test","hypothesis":"A larger banner converts better",` +
			`"variations":[{"name":"Large","percentage":50}],"includedTargets":[{"attribute":"url","type":"any","value":""}],` +
			`"successMetrics":["nb_conversions"],"confidenceThreshold":95,"mdeRelative":10,"percentageParticipants":100}`)}},
}

var experimentsDelete = capability.Descriptor{
	ID: Provider + ".experiments.delete", Version: 1, Title: "Delete a Matomo A/B test experiment",
	Description: "Permanently delete an A/B test experiment of a website; Qatlas offers no way to restore it, and Matomo " +
		"does not document what happens to its reports. The experiment is read first and must belong to the website, " +
		"otherwise it is not found. Needs the premium plugin AbTesting and, expected, a write right on the website; " +
		"idempotent in effect" + experimentSentOnceGet + "; offered only where a connection's tools list names it",
	Tags: []string{"matomo", "experiments", "ab-testing", "delete"}, Provider: Provider, RequiresToolAllowList: true,
	Risk: experimentChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idExperimentSchema + `},` +
		`"required":["idExperiment"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idExperiment":{"type":"integer"},"deleted":{"type":"boolean"}},` +
		`"required":["idSite","idExperiment","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(),
		{Name: "idExperiment", Required: true, Description: "ID of the experiment to delete, from matomo.experiments.list"}},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the experiment belonged to"},
		{Name: "idExperiment", Description: "The experiment that was deleted"},
		{Name: "deleted", Description: "True once Matomo accepted the deletion"}},
	Examples: []capability.Example{{Description: "Delete experiment 4 of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"idExperiment":4}`)}},
}

// ExperimentCreated is the answer of experiments.add.
type ExperimentCreated struct {
	IDSite       int  `json:"idSite"`
	IDExperiment int  `json:"idExperiment"`
	Created      bool `json:"created"`
}

// ExperimentSaved is the answer of experiments.update.
type ExperimentSaved struct {
	IDSite       int  `json:"idSite"`
	IDExperiment int  `json:"idExperiment"`
	Saved        bool `json:"saved"`
}

// ExperimentDeleted is the answer of experiments.delete.
type ExperimentDeleted struct {
	IDSite       int  `json:"idSite"`
	IDExperiment int  `json:"idExperiment"`
	Deleted      bool `json:"deleted"`
}

type experimentVariationInput struct {
	Name        string  `json:"name"`
	Percentage  *int    `json:"percentage"`
	RedirectURL *string `json:"redirect_url"`
}

type experimentTargetInput struct {
	Attribute string `json:"attribute"`
	Type      string `json:"type"`
	Value     string `json:"value"`
	Inverted  *bool  `json:"inverted"`
}

// experimentBase holds what add and update both take.
type experimentBase struct {
	Name            string                     `json:"name"`
	Description     string                     `json:"description"`
	Hypothesis      string                     `json:"hypothesis"`
	Variations      []experimentVariationInput `json:"variations"`
	IncludedTargets []experimentTargetInput    `json:"includedTargets"`
	SuccessMetrics  []string                   `json:"successMetrics"`
}

func targetParams(params url.Values, field string, rules []experimentTargetInput, minimum int) error {
	if len(rules) < minimum || len(rules) > maxExperimentRows {
		return invalidArgument(field + " has too few or too many rules")
	}
	for i, rule := range rules {
		if !patternTypeOK(rule.Attribute) || !patternTypeOK(rule.Type) || utf8.RuneCountInString(rule.Value) > maxUrlLength {
			return invalidArgument("each rule of " + field + " needs an attribute and a type of lowercase letters and " +
				"underscores and a value of up to 2048 characters")
		}
		key := field + "[" + strconv.Itoa(i) + "]"
		params.Set(key+"[attribute]", rule.Attribute)
		params.Set(key+"[type]", rule.Type)
		params.Set(key+"[value]", rule.Value)
		if rule.Inverted != nil {
			params.Set(key+"[inverted]", flagValue(rule.Inverted))
		}
	}
	return nil
}

// params checks the definition the way the descriptor promises. Matomo stays the authority: it checks again, and
// its answer decides.
func (b experimentBase) params(params url.Values) error {
	for _, text := range []struct {
		key, value string
		limit      int
	}{{"name", b.Name, maxExperimentName}, {"description", b.Description, maxUrlLength},
		{"hypothesis", b.Hypothesis, maxUrlLength}} {
		if strings.TrimSpace(text.value) == "" || utf8.RuneCountInString(text.value) > text.limit {
			return invalidArgument(text.key + " must hold 1 to " + strconv.Itoa(text.limit) + " characters")
		}
		params.Set(text.key, text.value)
	}
	if len(b.Variations) < 1 || len(b.Variations) > maxExperimentRows {
		return invalidArgument("variations must hold 1 to " + strconv.Itoa(maxExperimentRows) + " variations")
	}
	for i, variation := range b.Variations {
		if strings.TrimSpace(variation.Name) == "" || utf8.RuneCountInString(variation.Name) > maxExperimentName ||
			variation.Percentage != nil && (*variation.Percentage < 1 || *variation.Percentage > 100) ||
			variation.RedirectURL != nil && utf8.RuneCountInString(*variation.RedirectURL) > maxUrlLength {
			return invalidArgument("each variation needs a name of 1 to 255 characters, a percentage of 1 to 100 if " +
				"given, and a redirect_url of up to 2048 characters if given")
		}
		key := "variations[" + strconv.Itoa(i) + "]"
		params.Set(key+"[name]", variation.Name)
		if variation.Percentage != nil {
			params.Set(key+"[percentage]", strconv.Itoa(*variation.Percentage))
		}
		if variation.RedirectURL != nil {
			params.Set(key+"[redirect_url]", *variation.RedirectURL)
		}
	}
	if err := targetParams(params, "includedTargets", b.IncludedTargets, 1); err != nil {
		return err
	}
	if len(b.SuccessMetrics) < 1 || len(b.SuccessMetrics) > maxExperimentRows {
		return invalidArgument("successMetrics must hold 1 to " + strconv.Itoa(maxExperimentRows) + " metrics")
	}
	seen := map[string]bool{}
	for i, metric := range b.SuccessMetrics {
		if !experimentMetricPattern.MatchString(metric) || seen[metric] {
			return invalidArgument("successMetrics must hold different metrics of letters, digits, and underscores")
		}
		seen[metric] = true
		params.Set("successMetrics["+strconv.Itoa(i)+"]", metric)
	}
	return nil
}

func experimentDate(key string, value *string, params url.Values) error {
	if value == nil {
		return nil
	}
	if _, err := time.Parse(experimentDateLayout, *value); err != nil {
		return invalidArgument(key + " must be a UTC date as 'YYYY-MM-DD HH:MM:SS'")
	}
	params.Set(key, *value)
	return nil
}

func checkExperimentID(id *int) error {
	if id == nil || *id < 1 || *id > maxExperimentID {
		return invalidArgument("idExperiment must be a positive integer experiment ID from matomo.experiments.list")
	}
	return nil
}

func invokeExperimentsAdd(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "add experiment"
	var args struct {
		IDSite *int `json:"idSite"`
		experimentBase
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}}
	if err := args.params(params); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, experimentsRequirement(experimentsAdd.ID), "AbTesting.addExperiment", params,
		experimentSentOnceList, &answer); err != nil {
		return nil, err
	}
	var id int
	if scalar(answer, &id) != nil || id < 1 {
		return nil, unclearChange(op, "Matomo returned no ID of the experiment", experimentSentOnceList)
	}
	return &ExperimentCreated{IDSite: site, IDExperiment: id, Created: true}, nil
}

func invokeExperimentsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update experiment"
	var args struct {
		IDSite                 *int                    `json:"idSite"`
		IDExperiment           *int                    `json:"idExperiment"`
		ConfidenceThreshold    *float64                `json:"confidenceThreshold"`
		MdeRelative            *int                    `json:"mdeRelative"`
		PercentageParticipants *int                    `json:"percentageParticipants"`
		ExcludedTargets        []experimentTargetInput `json:"excludedTargets"`
		StartDate              *string                 `json:"startDate"`
		EndDate                *string                 `json:"endDate"`
		ForwardUTMParams       *bool                   `json:"forwardUtmParams"`
		ForwardAllQueryParams  *bool                   `json:"forwardAllQueryParams"`
		experimentBase
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := checkExperimentID(args.IDExperiment); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idExperiment": {strconv.Itoa(*args.IDExperiment)}}
	if err := args.params(params); err != nil {
		return nil, err
	}
	threshold := args.ConfidenceThreshold
	if threshold == nil || math.IsNaN(*threshold) || *threshold != 90 && *threshold != 95 && *threshold != 98 &&
		*threshold != 99 && *threshold != 99.5 {
		return nil, invalidArgument("confidenceThreshold must be 90, 95, 98, 99, or 99.5")
	}
	params.Set("confidenceThreshold", strconv.FormatFloat(*threshold, 'f', -1, 64))
	for _, number := range []struct {
		key   string
		value *int
	}{{"mdeRelative", args.MdeRelative}, {"percentageParticipants", args.PercentageParticipants}} {
		if number.value == nil || *number.value < 1 || *number.value > 100 {
			return nil, invalidArgument(number.key + " must be an integer between 1 and 100")
		}
		params.Set(number.key, strconv.Itoa(*number.value))
	}
	if args.ExcludedTargets != nil {
		if err := targetParams(params, "excludedTargets", args.ExcludedTargets, 0); err != nil {
			return nil, err
		}
	}
	if err := experimentDate("startDate", args.StartDate, params); err != nil {
		return nil, err
	}
	if err := experimentDate("endDate", args.EndDate, params); err != nil {
		return nil, err
	}
	for key, value := range map[string]*bool{"forwardUtmParams": args.ForwardUTMParams,
		"forwardAllQueryParams": args.ForwardAllQueryParams} {
		if value != nil {
			params.Set(key, flagValue(value))
		}
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := experimentsRequirement(experimentsUpdate.ID)
	// The experiment ID counts across websites: one read must show it on this website before it is replaced.
	if _, err := client.fetchExperiment(ctx, op, req, site, *args.IDExperiment); err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, req, "AbTesting.updateExperiment", params, experimentSentOnceGet, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the experiment", experimentSentOnceGet)
	}
	return &ExperimentSaved{IDSite: site, IDExperiment: *args.IDExperiment, Saved: true}, nil
}

func invokeExperimentsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete experiment"
	var args struct {
		IDSite       *int `json:"idSite"`
		IDExperiment *int `json:"idExperiment"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := checkExperimentID(args.IDExperiment); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idExperiment": {strconv.Itoa(*args.IDExperiment)}}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := experimentsRequirement(experimentsDelete.ID)
	// The experiment ID counts across websites: one read must show it on this website before anything is deleted.
	if _, err := client.fetchExperiment(ctx, op, req, site, *args.IDExperiment); err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, req, "AbTesting.deleteExperiment", params, experimentSentOnceGet, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the deletion", experimentSentOnceGet)
	}
	return &ExperimentDeleted{IDSite: site, IDExperiment: *args.IDExperiment, Deleted: true}, nil
}

func experimentChangeOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: experimentsAdd, Handler: capability.Handler(invokeExperimentsAdd)},
		{Descriptor: experimentsUpdate, Handler: capability.Handler(invokeExperimentsUpdate)},
		{Descriptor: experimentsDelete, Handler: capability.Handler(invokeExperimentsDelete)},
	}
}
