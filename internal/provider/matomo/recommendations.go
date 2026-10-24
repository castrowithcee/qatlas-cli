package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	recommendationsSensitive = "matomo-goal-recommendations"
	// A recommendation ID is the match attribute, a colon, and the normalized pattern, which a goal limits to 255.
	maxRecommendationIDLength = 300
	maxRecommendationItems    = 100
	maxRecommendationMembers  = 50
	maxRecommendationDepth    = 4
)

const recommendationsRight = "; it needs a write right on the website, which Matomo checks, and the Goal " +
	"recommendations feature of the instance (an instance without it reports the tool as unsupported)"

const recommendationProperties = `"idSite":{"type":"integer"},"mode":{"type":"string"},` +
	`"goals":{"type":"array","items":{"type":"object"}},"manualGoals":{"type":"array","items":{"type":"object"}},` +
	`"warnings":{"type":"array","items":{"type":"object"}},"aiError":{"type":"string"},"useAi":{"type":"boolean"},` +
	`"generatedAt":{"type":"integer"},"remainingAiScans":{"type":"integer"},"providerName":{"type":"string"},` +
	`"aiAvailability":{"type":"string"},"privacyNote":{"type":"string"}`

const recommendationOutput = `{"type":"object","properties":{` + recommendationProperties +
	`},"required":["idSite"],"additionalProperties":false}`

func recommendationFields(scan bool) []capability.Field {
	fields := []capability.Field{
		{Name: "idSite", Description: "The website the recommendations belong to"},
		{Name: "mode", Description: "How the goals were found: deterministic, or ai when a saved scan used an AI provider"},
		{Name: "goals", Description: "Recommended goals as Matomo reports them, untrusted: id (the recommendation ID " +
			"for dismiss), name, category, matchAttribute, pattern, patternType, caseSensitive, " +
			"allowMultipleConversionsPerVisit, revenue, useEventValueAsRevenue, reason, description, source, needsSetup, " +
			"implementationNote, evidence, sourcePages, and the other members Matomo adds; a goal is created with " +
			"matomo.goals.add"},
		{Name: "manualGoals", Description: "Ideas of goals to create by hand (name, howTo, category), untrusted"},
		{Name: "warnings", Description: "Why a result is thin (type, severity, message), untrusted"},
		{Name: "useAi", Description: "True when the saved scan used an AI provider; never true for a scan of Qatlas"},
		{Name: "generatedAt", Description: "Unix time of the scan; absent when nothing is saved or the website could " +
			"not be analysed"},
		{Name: "remainingAiScans", Description: "AI scans left today; absent when unlimited. Qatlas never starts one"},
		{Name: "providerName", Description: "Name of the AI provider the instance has configured, untrusted"},
		{Name: "aiAvailability", Description: "available, notConfigured, notActivated, or disabled"},
		{Name: "privacyNote", Description: "Matomo's note on what an AI scan would send out, untrusted"},
	}
	if scan {
		fields = append(fields, capability.Field{Name: "aiError", Description: "Matomo's explanation when the website " +
			"could not be analysed, untrusted"})
	}
	return fields
}

var goalRecommendationsList = capability.Descriptor{
	ID: Provider + ".goal-recommendations.list", Version: 1, Title: "List saved Matomo goal recommendations",
	Description: "Read the goal recommendations that the last scan of a website saved, without the ones dismissed one by " +
		"one; an empty result without generatedAt means nothing is saved; changes nothing and starts no scan" +
		recommendationsRight + ", so a token with a view right only is refused; names, patterns, and texts are untrusted",
	Tags: []string{"matomo", "goals", "recommendations", "list"}, Risk: readRisk(recommendationsSensitive),
	Provider:     Provider,
	InputSchema:  json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(recommendationOutput),
	Arguments:    []capability.Argument{idSiteArgument()},
	Fields:       recommendationFields(false),
	Examples:     []capability.Example{{Description: "Read the saved recommendations of website 1", Arguments: json.RawMessage(`{"idSite":1}`)}},
}

var goalRecommendationsScan = capability.Descriptor{
	ID: Provider + ".goal-recommendations.scan", Version: 1, Title: "Scan a Matomo website for goal recommendations",
	Description: "Let the Matomo server analyse the website and save new goal recommendations: the server fetches the main URL of the website " +
		"and up to 50 other same-site documents within about 25 seconds, scores links, " +
		"forms, and downloads, replaces the saved recommendations, and clears the single dismissals; it creates no " +
		"goal and uses no AI provider, so no site data goes to one (Qatlas never sends the AI option). A scan can " +
		"outlast the 30 second limit of the call; a scan already running for the website or the user is refused. " +
		"Not idempotent, since every call crawls the website again; sent once and never repeated" +
		recommendationsRight + "; names, patterns, and texts are untrusted",
	Tags: []string{"matomo", "goals", "recommendations", "scan"}, Risk: goalChangeRisk(capability.EffectExecute,
		capability.IdempotencyNonIdempotent), Provider: Provider,
	InputSchema:  json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(recommendationOutput),
	Arguments:    []capability.Argument{idSiteArgument()},
	Fields:       recommendationFields(true),
	Examples:     []capability.Example{{Description: "Scan website 1", Arguments: json.RawMessage(`{"idSite":1}`)}},
}

const recommendationDismissedOutput = `{"type":"object","properties":{"idSite":{"type":"integer"},` +
	`"recommendation_id":{"type":"string"},"all":{"type":"boolean"},"dismissed":{"type":"boolean"}},` +
	`"required":["idSite","dismissed"],"additionalProperties":false}`

var goalRecommendationsDismiss = capability.Descriptor{
	ID: Provider + ".goal-recommendations.dismiss", Version: 1, Title: "Dismiss Matomo goal recommendations",
	Description: "Dismiss one saved recommendation (recommendation_id, the id of a goal from " +
		"matomo.goal-recommendations.list) or, with all true, delete all saved recommendations of the website; give " +
		"exactly one of the two. A single dismissal lasts until the next scan; an ID that no saved recommendation has " +
		"changes nothing and returns dismissed false. Idempotent; creates and deletes no goal; sent once and never " +
		"repeated" + recommendationsRight,
	Tags: []string{"matomo", "goals", "recommendations", "update"}, Risk: goalChangeRisk(capability.EffectUpdate,
		capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,"recommendation_id":` +
		`{"type":"string","minLength":3,"maxLength":300},"all":{"type":"boolean"}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(recommendationDismissedOutput),
	Arguments: []capability.Argument{idSiteArgument(),
		{Name: "recommendation_id", Description: "The id of one saved recommendation, from " +
			"matomo.goal-recommendations.list: a match attribute, a colon, and a lower-case pattern, up to 300 characters"},
		{Name: "all", Description: "True to delete all saved recommendations of the website instead of one"}},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the recommendations belong to"},
		{Name: "recommendation_id", Description: "The dismissed recommendation; absent with all"},
		{Name: "all", Description: "True when all saved recommendations were deleted"},
		{Name: "dismissed", Description: "True once Matomo dismissed; false when no saved recommendation had the ID"},
	},
	Examples: []capability.Example{
		{Description: "Dismiss one recommendation", Arguments: json.RawMessage(`{"idSite":1,"recommendation_id":"url:thank-you"}`)},
		{Description: "Delete all saved recommendations", Arguments: json.RawMessage(`{"idSite":1,"all":true}`)},
	},
}

// Recommendations is the answer of the list and scan tools. All text is untrusted provider data; the nested
// recommendations keep the members Matomo reports.
type Recommendations struct {
	IDSite           int              `json:"idSite"`
	Mode             string           `json:"mode,omitempty"`
	Goals            []map[string]any `json:"goals,omitempty"`
	ManualGoals      []map[string]any `json:"manualGoals,omitempty"`
	Warnings         []map[string]any `json:"warnings,omitempty"`
	AIError          string           `json:"aiError,omitempty"`
	UseAI            *bool            `json:"useAi,omitempty"`
	GeneratedAt      *int             `json:"generatedAt,omitempty"`
	RemainingAIScans *int             `json:"remainingAiScans,omitempty"`
	ProviderName     string           `json:"providerName,omitempty"`
	AIAvailability   string           `json:"aiAvailability,omitempty"`
	PrivacyNote      string           `json:"privacyNote,omitempty"`
}

// RecommendationDismissed is the answer of goal-recommendations.dismiss.
type RecommendationDismissed struct {
	IDSite           int    `json:"idSite"`
	RecommendationID string `json:"recommendation_id,omitempty"`
	All              bool   `json:"all,omitempty"`
	Dismissed        bool   `json:"dismissed"`
}

// recommendationsJSON is a recommendation result. The debug payload Matomo adds in development mode is not read.
type recommendationsJSON struct {
	Mode             *loose          `json:"mode"`
	Goals            json.RawMessage `json:"goals"`
	ManualGoals      json.RawMessage `json:"manualGoals"`
	Warnings         json.RawMessage `json:"warnings"`
	AIError          *loose          `json:"aiError"`
	UseAI            *loose          `json:"useAi"`
	GeneratedAt      *loose          `json:"generatedAt"`
	RemainingAIScans *loose          `json:"remainingAiScans"`
	ProviderName     *loose          `json:"providerName"`
	AIAvailability   *loose          `json:"aiAvailability"`
	PrivacyNote      *loose          `json:"privacyNote"`
}

// boundValue keeps a Matomo value inside the text, list, and depth limits; what exceeds the depth is dropped.
func boundValue(value any, depth int) any {
	switch v := value.(type) {
	case string:
		return bounded(v)
	case []any:
		if depth >= maxRecommendationDepth {
			return nil
		}
		out := make([]any, 0, min(len(v), maxRecommendationMembers))
		for _, item := range v[:min(len(v), maxRecommendationMembers)] {
			out = append(out, boundValue(item, depth+1))
		}
		return out
	case map[string]any:
		if depth >= maxRecommendationDepth {
			return nil
		}
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		out := map[string]any{}
		for _, key := range keys[:min(len(keys), maxRecommendationMembers)] {
			out[bounded(key)] = boundValue(v[key], depth+1)
		}
		return out
	}
	return value
}

func recommendationList(raw json.RawMessage) ([]map[string]any, bool) {
	rows, ok := rowList(raw)
	if !ok {
		return nil, false
	}
	var out []map[string]any
	for _, row := range rows[:min(len(rows), maxRecommendationItems)] {
		if value, ok := decodeValue(row); ok {
			if object, isObject := boundValue(value, 0).(map[string]any); isObject {
				out = append(out, object)
			}
		}
	}
	return out, true
}

func readRecommendations(raw json.RawMessage, site int) (*Recommendations, bool) {
	raw = bytes.TrimSpace(raw)
	var row recommendationsJSON
	if !bytes.HasPrefix(raw, []byte("{")) || json.Unmarshal(raw, &row) != nil {
		return nil, false
	}
	out := &Recommendations{IDSite: site, Mode: row.Mode.str(maxStringLength), AIError: row.AIError.str(maxReportText),
		UseAI: row.UseAI.flag(), GeneratedAt: row.GeneratedAt.number(), RemainingAIScans: row.RemainingAIScans.number(),
		ProviderName: row.ProviderName.str(maxStringLength), AIAvailability: row.AIAvailability.str(maxStringLength),
		PrivacyNote: row.PrivacyNote.str(maxReportText)}
	if row.UseAI != nil && row.UseAI.text == "" {
		out.UseAI = nil
	}
	var ok bool
	if out.Goals, ok = recommendationList(row.Goals); !ok {
		return nil, false
	}
	if out.ManualGoals, ok = recommendationList(row.ManualGoals); !ok {
		return nil, false
	}
	if out.Warnings, ok = recommendationList(row.Warnings); !ok {
		return nil, false
	}
	return out, true
}

func siteOnly(raw json.RawMessage, resolved *config.Resolved) (int, error) {
	var args struct {
		IDSite *int `json:"idSite"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return 0, err
	}
	return selectSite(resolved, args.IDSite)
}

func invokeGoalRecommendationsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list goal recommendations"
	id, err := siteOnly(raw, resolved)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	params := url.Values{"idSite": {strconv.Itoa(id)}}
	if err := client.call(ctx, op, goalsRequirement(goalRecommendationsList.ID), "Goals.getSavedRecommendedGoals", params,
		&answer); err != nil {
		return nil, err
	}
	result, ok := readRecommendations(answer, id)
	if !ok {
		return nil, provider.InvalidResponse(op, "Matomo returned invalid goal recommendations")
	}
	return result, nil
}

func invokeGoalRecommendationsScan(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "scan goal recommendations"
	id, err := siteOnly(raw, resolved)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	// useAi is left out on purpose: Matomo then scans without an AI provider, and no site data leaves the instance.
	params := url.Values{"idSite": {strconv.Itoa(id)}}
	var answer json.RawMessage
	if err := client.change(ctx, op, goalsRequirement(goalRecommendationsScan.ID),
		"Goals.runGoalRecommendationScan", params, scanSentOnce, &answer); err != nil {
		return nil, err
	}
	result, ok := readRecommendations(answer, id)
	if !ok {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid scan result"+scanSentOnce)
	}
	return result, nil
}

// validRecommendationID checks the form Matomo builds: a match attribute, a colon, and a trimmed pattern.
func validRecommendationID(value string) bool {
	attribute, pattern, found := strings.Cut(value, ":")
	if !found || pattern == "" || !slices.Contains(goalMatchAttributes, attribute) ||
		utf8.RuneCountInString(value) > maxRecommendationIDLength || !utf8.ValidString(value) ||
		strings.TrimSpace(value) != value {
		return false
	}
	return !strings.ContainsFunc(value, unicode.IsControl)
}

func invokeGoalRecommendationsDismiss(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "dismiss goal recommendations"
	var args struct {
		IDSite           *int   `json:"idSite"`
		RecommendationID string `json:"recommendation_id"`
		All              bool   `json:"all"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	switch {
	case args.All == (args.RecommendationID != ""):
		return nil, invalidArgument("give exactly one of recommendation_id and all true")
	case !args.All && !validRecommendationID(args.RecommendationID):
		return nil, invalidArgument("recommendation_id must be an id from matomo.goal-recommendations.list: a match " +
			"attribute, a colon, and a pattern, up to 300 characters without control characters")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(id)}}
	method := "Goals.dismissRecommendedGoals"
	if !args.All {
		method = "Goals.dismissRecommendedGoal"
		params.Set("recommendationId", args.RecommendationID)
	}
	var answer struct {
		Success *loose `json:"success"`
	}
	if err := client.change(ctx, op, goalsRequirement(goalRecommendationsDismiss.ID), method, params,
		dismissSentOnce, &answer); err != nil {
		return nil, err
	}
	if answer.Success == nil {
		return nil, unclearChange(op, "Matomo returned no confirmation of the dismissal", dismissSentOnce)
	}
	done := answer.Success.flag()
	return &RecommendationDismissed{IDSite: id, RecommendationID: args.RecommendationID, All: args.All,
		Dismissed: done != nil && *done}, nil
}

var dismissSentOnce = sentOnceFor("recommendations", "matomo.goal-recommendations.list")

const scanSentOnce = "; sent once and never repeated by Qatlas: a scan may have run and saved its result, so read it " +
	"with matomo.goal-recommendations.list before scanning again"
