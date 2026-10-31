package matomo

import (
	"context"
	"encoding/json"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxHeatmapName         = 255
	maxHeatmapDescription  = 255
	maxHeatmapDestinations = 20
	maxHeatmapSampleLimit  = 999999999
	maxHeatmapBreakpoint   = 99999
)

var (
	heatmapSentOnceList = sentOnceFor("heatmap", "matomo.heatmaps.list")
	heatmapSentOnceGet  = sentOnceFor("heatmap", "matomo.heatmaps.get")
)

// heatmapChangeRisk is the risk of every heatmap change: confirmed, open to the instance, and about the heatmap
// configuration of a website. No change deletes anything.
func heatmapChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: heatmapsSensitive}
}

const (
	heatmapSettingsSchema = `"name":{"type":"string","minLength":1,"maxLength":255},"matchPageRules":` + matchRuleSchema + `,` +
		`"sampleLimit":{"type":"integer","minimum":1,"maximum":999999999},` +
		`"sampleRate":{"type":"number","minimum":0.1,"maximum":100},` +
		`"excludedElements":{"type":"string","maxLength":2048},"screenshotUrl":{"type":"string","maxLength":2048},` +
		`"breakpointMobile":{"type":"integer","minimum":1,"maximum":99999},` +
		`"breakpointTablet":{"type":"integer","minimum":1,"maximum":99999},` +
		`"captureDomManually":{"type":"boolean"},"description":{"type":"string","maxLength":255},` +
		`"autoRepeat":{"type":"boolean"}`
	heatmapDestinationsSchema = `"idDestinationSites":{"type":"array","minItems":1,"maxItems":20,"uniqueItems":true,` +
		`"items":{"type":"integer","minimum":1,"maximum":999999999}}`
	heatmapSettingsHint = "Optional settings that are not given are not sent, so Matomo applies its own defaults " +
		"(by its signature: sampleLimit 1000, sampleRate 5, captureDomManually and autoRepeat false, the others empty)"
)

func heatmapSettingArguments(replaces bool) []capability.Argument {
	given := "; Matomo's default when omitted"
	if replaces {
		given = "; reset to Matomo's default when omitted"
	}
	return []capability.Argument{
		{Name: "name", Required: true, Description: "Name of the heatmap, 1 to 255 characters"},
		{Name: "matchPageRules", Required: true, Description: "1 to 20 page rules that select the pages of the " +
			"heatmap; each holds attribute and type (lowercase letters and underscores, values from " +
			"matomo.heatmap-options.list) and value, and optionally inverted"},
		{Name: "sampleLimit", Description: "Number of page views after which recording ends, 1 to 999999999; the " +
			"values Matomo offers are in matomo.heatmap-options.list" + given},
		{Name: "sampleRate", Description: "Share of page views that are recorded in percent, 0.1 to 100 with at " +
			"most one decimal" + given},
		{Name: "excludedElements", Description: "Comma-separated CSS selectors of elements left out of the heatmap, " +
			"up to 2048 characters" + given},
		{Name: "screenshotUrl", Description: "Address of the page the screenshot is taken from, up to 2048 " +
			"characters; text that Qatlas never fetches" + given},
		{Name: "breakpointMobile", Description: "Screen width in pixels below which a device of unknown type counts " +
			"as mobile, 1 to 99999" + given},
		{Name: "breakpointTablet", Description: "Screen width in pixels below which a device of unknown type counts " +
			"as tablet, 1 to 99999" + given},
		{Name: "captureDomManually", Description: "True to capture the page copy by hand instead of automatically" + given},
		{Name: "description", Description: "Description of the heatmap, up to 255 characters; only the legacy " +
			"reference lists it, and a Matomo that does not know it may refuse it" + given},
		{Name: "autoRepeat", Description: "True to repeat the heatmap automatically; only the legacy reference lists " +
			"it, and a Matomo that does not know it may refuse it" + given}}
}

var heatmapsAdd = capability.Descriptor{
	ID: Provider + ".heatmaps.add", Version: 1, Title: "Create a Matomo heatmap",
	Description: "Create a heatmap on a website with a name and page rules, and optional settings. " + heatmapSettingsHint +
		". A repeated call creates a second heatmap. Needs the premium plugin HeatmapSessionRecording and, expected, " +
		"a write right on the website" + heatmapSentOnceList,
	Tags: []string{"matomo", "heatmaps", "behavior", "create"}, Risk: heatmapChangeRisk(capability.EffectCreate,
		capability.IdempotencyNonIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + heatmapSettingsSchema + `},` +
		`"required":["name","matchPageRules"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idSiteHsr":{"type":"integer"},"created":{"type":"boolean"}},"required":["idSite","idSiteHsr","created"],` +
		`"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument()}, heatmapSettingArguments(false)...),
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the heatmap belongs to"},
		{Name: "idSiteHsr", Description: "ID of the new heatmap, used as idSiteHsr by the other heatmap tools"},
		{Name: "created", Description: "True once Matomo created the heatmap"}},
	Examples: []capability.Example{{Description: "Heatmap of the pricing page of website 1", Arguments: json.RawMessage(
		`{"idSite":1,"name":"Pricing page","matchPageRules":[{"attribute":"url","type":"equals_simple",` +
			`"value":"https://example.org/pricing"}],"sampleRate":10}`)}},
}

var heatmapsUpdate = capability.Descriptor{
	ID: Provider + ".heatmaps.update", Version: 1, Title: "Replace the configuration of a Matomo heatmap",
	Description: "Replace the configuration of a heatmap of a website as a whole: name and matchPageRules are " +
		"required, and " + heatmapSettingsHint + ", so a setting left out may reset a value the heatmap had; read " +
		"the heatmap with matomo.heatmaps.get and pass every setting to keep. The heatmap is read first and must " +
		"belong to the website, otherwise it is not found. Needs the premium plugin HeatmapSessionRecording and, " +
		"expected, a write right on the website; idempotent in effect" + heatmapSentOnceGet,
	Tags: []string{"matomo", "heatmaps", "behavior", "update"}, Risk: heatmapChangeRisk(capability.EffectUpdate,
		capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idHeatmapSchema + `,` +
		heatmapSettingsSchema + `},"required":["idSiteHsr","name","matchPageRules"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idSiteHsr":{"type":"integer"},"saved":{"type":"boolean"}},"required":["idSite","idSiteHsr","saved"],` +
		`"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument(),
		{Name: "idSiteHsr", Required: true, Description: "ID of the heatmap to replace, from matomo.heatmaps.list"}},
		heatmapSettingArguments(true)...),
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the heatmap belongs to"},
		{Name: "idSiteHsr", Description: "The heatmap that was replaced"},
		{Name: "saved", Description: "True once Matomo accepted the configuration"}},
	Examples: []capability.Example{{Description: "Rename heatmap 11 of website 1 and keep one page rule",
		Arguments: json.RawMessage(`{"idSite":1,"idSiteHsr":11,"name":"Home","matchPageRules":[{"attribute":"url",` +
			`"type":"equals_simple","value":"https://example.org/"}],"sampleLimit":2000,"sampleRate":100}`)}},
}

var heatmapsDuplicate = capability.Descriptor{
	ID: Provider + ".heatmaps.duplicate", Version: 1, Title: "Copy a Matomo heatmap to websites",
	Description: "Copy a heatmap of a website to one or more websites, each a new heatmap. Every website in " +
		"idDestinationSites must lie inside the connection's targets, otherwise the whole call is refused before " +
		"anything is sent; the list is required, holds 1 to 20 different websites, and may name the source website. " +
		"The heatmap is read first and must belong to the source website, otherwise it is not found. A repeated call " +
		"copies it again. Needs the premium plugin HeatmapSessionRecording and, expected, a write right on the " +
		"websites" + heatmapSentOnceList,
	Tags: []string{"matomo", "heatmaps", "behavior", "create"}, Risk: heatmapChangeRisk(capability.EffectCreate,
		capability.IdempotencyNonIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idHeatmapSchema + `,` +
		heatmapDestinationsSchema + `},"required":["idSiteHsr","idDestinationSites"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idSiteHsr":{"type":"integer"},"idDestinationSites":{"type":"array","items":{"type":"integer"}},` +
		`"duplicated":{"type":"boolean"},"answer":{}},"required":["idSite","idSiteHsr","idDestinationSites",` +
		`"duplicated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(),
		{Name: "idSiteHsr", Required: true, Description: "ID of the heatmap to copy, from matomo.heatmaps.list"},
		{Name: "idDestinationSites", Required: true, Description: "1 to 20 different website IDs that receive a copy, " +
			"each inside the connection's targets; the source website is allowed"}},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the heatmap was copied from"},
		{Name: "idSiteHsr", Description: "The heatmap that was copied"},
		{Name: "idDestinationSites", Description: "The websites that were asked to receive a copy"},
		{Name: "duplicated", Description: "True once Matomo accepted the request"},
		{Name: "answer", Description: "What Matomo answered, such as the IDs of the new heatmaps, as Matomo gives it, " +
			"untrusted, shortened; the form is not documented"}},
	Examples: []capability.Example{{Description: "Copy heatmap 11 of website 1 to website 2",
		Arguments: json.RawMessage(`{"idSite":1,"idSiteHsr":11,"idDestinationSites":[2]}`)}},
}

// HeatmapCreated is the answer of heatmaps.add.
type HeatmapCreated struct {
	IDSite    int  `json:"idSite"`
	IDSiteHsr int  `json:"idSiteHsr"`
	Created   bool `json:"created"`
}

// HeatmapSaved is the answer of heatmaps.update.
type HeatmapSaved struct {
	IDSite    int  `json:"idSite"`
	IDSiteHsr int  `json:"idSiteHsr"`
	Saved     bool `json:"saved"`
}

// HeatmapDuplicated is the answer of heatmaps.duplicate.
type HeatmapDuplicated struct {
	IDSite             int   `json:"idSite"`
	IDSiteHsr          int   `json:"idSiteHsr"`
	IDDestinationSites []int `json:"idDestinationSites"`
	Duplicated         bool  `json:"duplicated"`
	Answer             any   `json:"answer,omitempty"`
}

// heatmapSettings holds the settings of add and update. Pointers tell a setting that is not given, which is
// not sent, from one that is.
type heatmapSettings struct {
	Name               string           `json:"name"`
	MatchPageRules     []matchRuleInput `json:"matchPageRules"`
	SampleLimit        *int             `json:"sampleLimit"`
	SampleRate         *float64         `json:"sampleRate"`
	ExcludedElements   *string          `json:"excludedElements"`
	ScreenshotURL      *string          `json:"screenshotUrl"`
	BreakpointMobile   *int             `json:"breakpointMobile"`
	BreakpointTablet   *int             `json:"breakpointTablet"`
	CaptureDomManually *bool            `json:"captureDomManually"`
	Description        *string          `json:"description"`
	AutoRepeat         *bool            `json:"autoRepeat"`
}

// params checks the settings the way the descriptor promises and adds only those that are given. Matomo stays the
// authority: it checks again, and its answer decides.
func (s heatmapSettings) params(params url.Values) error {
	if strings.TrimSpace(s.Name) == "" || utf8.RuneCountInString(s.Name) > maxHeatmapName {
		return invalidArgument("name must hold 1 to 255 characters")
	}
	params.Set("name", s.Name)
	if err := matchRuleParams(params, s.MatchPageRules); err != nil {
		return err
	}
	if s.SampleLimit != nil {
		if *s.SampleLimit < 1 || *s.SampleLimit > maxHeatmapSampleLimit {
			return invalidArgument("sampleLimit must be between 1 and 999999999")
		}
		params.Set("sampleLimit", strconv.Itoa(*s.SampleLimit))
	}
	if s.SampleRate != nil {
		rate := *s.SampleRate
		if math.IsNaN(rate) || rate < 0.1 || rate > 100 || math.Abs(rate*10-math.Round(rate*10)) > 1e-9 {
			return invalidArgument("sampleRate must be between 0.1 and 100 with at most one decimal")
		}
		params.Set("sampleRate", strconv.FormatFloat(math.Round(rate*10)/10, 'f', -1, 64))
	}
	for _, text := range []struct {
		key   string
		value *string
		limit int
	}{{"excludedElements", s.ExcludedElements, maxUrlLength}, {"screenshotUrl", s.ScreenshotURL, maxUrlLength},
		{"description", s.Description, maxHeatmapDescription}} {
		if text.value == nil {
			continue
		}
		if utf8.RuneCountInString(*text.value) > text.limit {
			return invalidArgument(text.key + " is longer than allowed")
		}
		params.Set(text.key, *text.value)
	}
	for _, width := range []struct {
		key   string
		value *int
	}{{"breakpointMobile", s.BreakpointMobile}, {"breakpointTablet", s.BreakpointTablet}} {
		if width.value == nil {
			continue
		}
		if *width.value < 1 || *width.value > maxHeatmapBreakpoint {
			return invalidArgument(width.key + " must be between 1 and 99999")
		}
		params.Set(width.key, strconv.Itoa(*width.value))
	}
	for key, value := range map[string]*bool{"captureDomManually": s.CaptureDomManually, "autoRepeat": s.AutoRepeat} {
		if value != nil {
			params.Set(key, flagValue(value))
		}
	}
	return nil
}

func invokeHeatmapsAdd(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "add heatmap"
	var args struct {
		IDSite *int `json:"idSite"`
		heatmapSettings
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
	if err := client.change(ctx, op, heatmapsRequirement(heatmapsAdd.ID), "HeatmapSessionRecording.addHeatmap", params,
		heatmapSentOnceList, &answer); err != nil {
		return nil, err
	}
	var id int
	if scalar(answer, &id) != nil || id < 1 {
		return nil, unclearChange(op, "Matomo returned no ID of the heatmap", heatmapSentOnceList)
	}
	return &HeatmapCreated{IDSite: site, IDSiteHsr: id, Created: true}, nil
}

func invokeHeatmapsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update heatmap"
	var args struct {
		IDSite    *int `json:"idSite"`
		IDSiteHsr *int `json:"idSiteHsr"`
		heatmapSettings
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := checkHeatmapID(args.IDSiteHsr); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idSiteHsr": {strconv.Itoa(*args.IDSiteHsr)}}
	if err := args.params(params); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := heatmapsRequirement(heatmapsUpdate.ID)
	// The heatmap ID counts across websites: one read must show it on this website before it is replaced.
	if _, err := client.fetchHeatmap(ctx, op, req, site, *args.IDSiteHsr); err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, req, "HeatmapSessionRecording.updateHeatmap", params, heatmapSentOnceGet,
		&answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the heatmap", heatmapSentOnceGet)
	}
	return &HeatmapSaved{IDSite: site, IDSiteHsr: *args.IDSiteHsr, Saved: true}, nil
}

// destinationSites checks the websites that receive a copy against the targets as a whole: one website outside
// them refuses the call, and no message names a value.
func destinationSites(resolved *config.Resolved, given []int) ([]int, error) {
	if len(given) < 1 || len(given) > maxHeatmapDestinations {
		return nil, invalidArgument("idDestinationSites must hold 1 to 20 website IDs")
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	seen := make([]int, 0, len(given))
	outside := false
	for _, id := range given {
		if id < 1 || len(strconv.Itoa(id)) > maxIDDigits {
			return nil, invalidArgument("idDestinationSites must hold positive integer website IDs")
		}
		if slices.Contains(seen, id) {
			return nil, invalidArgument("idDestinationSites must name each website once")
		}
		seen = append(seen, id)
		outside = outside || !bound.allows(id)
	}
	if outside {
		return nil, invalidArgument("every website in idDestinationSites must be available to this connection")
	}
	return seen, nil
}

func invokeHeatmapsDuplicate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "duplicate heatmap"
	var args struct {
		IDSite             *int  `json:"idSite"`
		IDSiteHsr          *int  `json:"idSiteHsr"`
		IDDestinationSites []int `json:"idDestinationSites"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := checkHeatmapID(args.IDSiteHsr); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	destinations, err := destinationSites(resolved, args.IDDestinationSites)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idSiteHsr": {strconv.Itoa(*args.IDSiteHsr)}}
	for i, id := range destinations {
		params.Set("idDestinationSites["+strconv.Itoa(i)+"]", strconv.Itoa(id))
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := heatmapsRequirement(heatmapsDuplicate.ID)
	if _, err := client.fetchHeatmap(ctx, op, req, site, *args.IDSiteHsr); err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, req, "HeatmapSessionRecording.duplicateHeatmap", params, heatmapSentOnceList,
		&answer); err != nil {
		return nil, err
	}
	shown, ok := duplicateAnswer(answer)
	if !ok {
		return nil, unclearChange(op, "Matomo returned no confirmation of the copy", heatmapSentOnceList)
	}
	return &HeatmapDuplicated{IDSite: site, IDSiteHsr: *args.IDSiteHsr, IDDestinationSites: destinations,
		Duplicated: true, Answer: shown}, nil
}

// duplicateAnswer accepts what the reference describes only in words ("whether it was successful and the IDs of
// the newly created heatmaps"): the generic success form, or an object that does not say it failed. The answer is
// shown bounded and untrusted.
func duplicateAnswer(raw json.RawMessage) (any, bool) {
	var object map[string]any
	if json.Unmarshal(raw, &object) != nil || len(object) == 0 {
		return nil, false
	}
	if failed, found := object["success"].(bool); found && !failed {
		return nil, false
	}
	return boundedValue(object, 0), true
}

func heatmapChangeOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: heatmapsAdd, Handler: capability.Handler(invokeHeatmapsAdd)},
		{Descriptor: heatmapsUpdate, Handler: capability.Handler(invokeHeatmapsUpdate)},
		{Descriptor: heatmapsDuplicate, Handler: capability.Handler(invokeHeatmapsDuplicate)},
	}
}
