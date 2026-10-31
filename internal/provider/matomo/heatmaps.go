package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// heatmapsSensitive covers the names, page rules, and addresses people wrote into heatmaps.
	heatmapsSensitive = "matomo-heatmaps"
	// heatmapBehaviorSensitive covers where visitors clicked, moved, and scrolled on a page.
	heatmapBehaviorSensitive = "matomo-heatmaps-visitor-behavior"
	heatmapsPlugin           = "HeatmapSessionRecording"
	maxHeatmapRules          = 100
	// maxTreeMirror bounds the recorded page copy, which Matomo returns as a large document.
	maxTreeMirror = 8192
	maxHeatmapID  = 2147483647
)

func heatmapsRequirement(tool string) requirement {
	return requirement{tool: tool, plugin: heatmapsPlugin}
}

const (
	idHeatmapSchema = `"idSiteHsr":{"type":"integer","minimum":1,"maximum":2147483647}`
	heatmapProps    = `"idSiteHsr":{"type":"integer"},"idSite":{"type":"integer"},"name":{"type":"string"},` +
		`"sample_rate":{"type":"string"},"sample_limit":{"type":"integer"},"match_page_rules":{"type":"array",` +
		`"items":{"type":"object","properties":{"attribute":{"type":"string"},"type":{"type":"string"},` +
		`"value":{"type":"string"},"inverted":{"type":"boolean"}},"additionalProperties":false}},` +
		`"excluded_elements":{"type":"string"},"screenshot_url":{"type":"string"},` +
		`"breakpoint_mobile":{"type":"integer"},"breakpoint_tablet":{"type":"integer"},` +
		`"created_date":{"type":"string"},"updated_date":{"type":"string"},"status":{"type":"string"},` +
		`"capture_manually":{"type":"boolean"},"created_date_pretty":{"type":"string"},` +
		`"heatmapViewUrl":{"type":"string"},"page_treemirror":{"type":"string"}`
)

func heatmapFields(prefix string) []capability.Field {
	return []capability.Field{
		{Name: prefix + "idSiteHsr", Description: "Heatmap ID, used as idSiteHsr by the other heatmap tools"},
		{Name: prefix + "idSite", Description: "The website the heatmap belongs to"},
		{Name: prefix + "name", Description: "Heatmap name, untrusted"},
		{Name: prefix + "sample_rate", Description: "Share of page views that are recorded, in percent, as Matomo writes it"},
		{Name: prefix + "sample_limit", Description: "Number of page views after which recording ends"},
		{Name: prefix + "match_page_rules", Description: "Rules that select the pages of the heatmap, at most 100, untrusted"},
		{Name: prefix + "match_page_rules[].attribute", Description: "What the rule looks at, such as url"},
		{Name: prefix + "match_page_rules[].type", Description: "How the rule compares, such as equals_exactly"},
		{Name: prefix + "match_page_rules[].value", Description: "Value compared with, untrusted; text that is never fetched"},
		{Name: prefix + "match_page_rules[].inverted", Description: "True when the rule matches pages that do not fit"},
		{Name: prefix + "excluded_elements", Description: "CSS selectors of elements left out of the heatmap, untrusted"},
		{Name: prefix + "screenshot_url", Description: "Address of the page the heatmap shows, untrusted; text that is never fetched"},
		{Name: prefix + "breakpoint_mobile", Description: "Screen width in pixels up to which a visit counts as mobile"},
		{Name: prefix + "breakpoint_tablet", Description: "Screen width in pixels up to which a visit counts as tablet"},
		{Name: prefix + "created_date", Description: "Time the heatmap was created as Matomo stores it"},
		{Name: prefix + "updated_date", Description: "Time the heatmap was last changed as Matomo stores it"},
		{Name: prefix + "status", Description: "Status of the heatmap, such as active or ended; the values come from " +
			"matomo.heatmap-options.list when the token may read them"},
		{Name: prefix + "capture_manually", Description: "True when the page screenshot is taken by hand"},
		{Name: prefix + "created_date_pretty", Description: "Creation date as Matomo formats it for people"},
		{Name: prefix + "heatmapViewUrl", Description: "Address of the Matomo page that shows the heatmap, untrusted; " +
			"the parameter token_auth is removed; text that is never fetched"},
		{Name: prefix + "page_treemirror", Description: "Recorded copy of the page, untrusted markup shortened to 8192 " +
			"bytes; only for matomo.heatmaps.list with includePageTreeMirror true, and only when Matomo returns it"},
	}
}

var heatmapsList = capability.Descriptor{
	ID: Provider + ".heatmaps.list", Version: 1, Title: "List Matomo heatmaps",
	Description: "List the heatmap configurations of a website with their page rules and status, in batches. The " +
		"recorded page copy is left out unless includePageTreeMirror is true. Names and page rules are untrusted. " +
		"Recorded visitor data is read with matomo.recorded-heatmaps.get. Needs the premium plugin " +
		"HeatmapSessionRecording and a view right on the website",
	Tags: []string{"matomo", "heatmaps", "behavior", "list"}, Risk: readRisk(heatmapsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` +
		`"includePageTreeMirror":{"type":"boolean"},` + pagingKeys + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"heatmaps":{"type":"array","items":{"type":"object","properties":{` + heatmapProps + `},` +
		`"required":["idSiteHsr","idSite"],"additionalProperties":false}},"has_more":{"type":"boolean"},` +
		`"next_cursor":{"type":"string"}},"required":["idSite","heatmaps","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument(),
		{Name: "includePageTreeMirror", Description: "True to ask Matomo for the recorded page copy of each heatmap " +
			"(page_treemirror), which is large; false when omitted, where Matomo itself would include it"}},
		pagingArguments...),
	Fields: append(append([]capability.Field{
		{Name: "idSite", Description: "The website the heatmaps belong to"},
		{Name: "heatmaps", Description: "Heatmaps in the order Matomo lists them; empty without one"}},
		heatmapFields("heatmaps[].")...), pagingFields...),
	Examples: []capability.Example{{Description: "Heatmaps of website 1", Arguments: json.RawMessage(`{"idSite":1}`)}},
}

var heatmapsGet = capability.Descriptor{
	ID: Provider + ".heatmaps.get", Version: 1, Title: "Get a Matomo heatmap",
	Description: "Read the configuration of one heatmap of a website by its ID. The heatmap ID counts across " +
		"websites, so the answer is shown only when Matomo reports it for this website, otherwise it is not found. " +
		"Names and page rules are untrusted. Needs the premium plugin HeatmapSessionRecording and a view right on " +
		"the website",
	Tags: []string{"matomo", "heatmaps", "behavior", "get"}, Risk: readRisk(heatmapsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idHeatmapSchema + `},` +
		`"required":["idSiteHsr"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + heatmapProps + `},` +
		`"required":["idSiteHsr","idSite"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(),
		{Name: "idSiteHsr", Required: true, Description: "ID of the heatmap, from matomo.heatmaps.list"}},
	Fields:   heatmapFields(""),
	Examples: []capability.Example{{Description: "Heatmap 11 of website 1", Arguments: json.RawMessage(`{"idSite":1,"idSiteHsr":11}`)}},
}

var recordedHeatmapsGet = capability.Descriptor{
	ID: Provider + ".recorded-heatmaps.get", Version: 1, Title: "Get recorded Matomo heatmap data",
	Description: "Read what visitors did in one heatmap in one period: the sample counts and the " +
		"recorded activities (clicks, moves, or scrolling) of one heatmap type on one device type. This is visitor " +
		"behavior: element selectors and positions come from the visited site and are untrusted. The activities " +
		"come in batches. The heatmap must belong to the website, which is proved by one configuration read first. " +
		"Heatmap and device types come from matomo.heatmap-options.list. Needs the premium plugin " +
		"HeatmapSessionRecording and a view right on the website",
	Tags: []string{"matomo", "heatmaps", "behavior", "visitors", "get"}, Risk: readRisk(heatmapBehaviorSensitive),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idHeatmapSchema + `,` +
		`"heatmapType":{"type":"integer","minimum":1,"maximum":99},"deviceType":{"type":"integer","minimum":1,"maximum":99},` +
		`"period":{"type":"string","enum":["day","week","month","year","range"]},` +
		`"date":{"type":"string","minLength":1,"maxLength":32},` +
		`"segment":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxSegmentLength) + `},` + pagingKeys + `},` +
		`"required":["idSiteHsr","heatmapType","deviceType","period","date"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idSiteHsr":{"type":"integer"},"heatmapType":{"type":"integer"},"deviceType":{"type":"integer"},` +
		`"period":{"type":"string"},"date":{"type":"string"},"metadata":{"type":"object"},` +
		`"activities":{"type":"array","items":{"type":"object"}},"has_more":{"type":"boolean"},` +
		`"next_cursor":{"type":"string"}},` +
		`"required":["idSite","idSiteHsr","heatmapType","deviceType","period","date","metadata","activities","has_more"],` +
		`"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument(),
		{Name: "idSiteHsr", Required: true, Description: "ID of the heatmap, from matomo.heatmaps.list"},
		{Name: "heatmapType", Required: true, Description: "Heatmap type as a number from matomo.heatmap-options.list " +
			"(Matomo's reference: 1 move, 2 click, 3 scroll)"},
		{Name: "deviceType", Required: true, Description: "Device type as a number from matomo.heatmap-options.list " +
			"(Matomo's reference: 1 desktop, 2 tablet, 3 mobile)"},
		{Name: "period", Required: true, Description: "day, week, month, year, or range"},
		{Name: "date", Required: true, Description: "One period: YYYY-MM-DD, today, yesterday; with period range a " +
			"range YYYY-MM-DD,YYYY-MM-DD, lastN, or previousN"},
		{Name: "segment", Description: "Matomo segment that filters the visits, such as browserName==Firefox; fields " +
			"from matomo.segments.fields, values URL-encoded"}}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "idSite", Description: "The website the heatmap belongs to"},
		{Name: "idSiteHsr", Description: "The heatmap read"},
		{Name: "heatmapType", Description: "The heatmap type asked for"},
		{Name: "deviceType", Description: "The device type asked for"},
		{Name: "period", Description: "The period asked for"},
		{Name: "date", Description: "The date asked for"},
		{Name: "metadata", Description: "Counts Matomo reports for the heatmap, such as nb_samples_device_all, " +
			"nb_samples_device_N, and avg_fold_device_N (average fold position), as Matomo names them; at most 100 entries"},
		{Name: "activities", Description: "Recorded activities in the order Matomo gives them; for click and move " +
			"each holds selector, offset_x, offset_y, and value; other types as Matomo gives them, untrusted, shortened"}},
		pagingFields...),
	Examples: []capability.Example{{Description: "Clicks on desktop for heatmap 11 yesterday",
		Arguments: json.RawMessage(`{"idSite":1,"idSiteHsr":11,"heatmapType":2,"deviceType":1,"period":"day","date":"yesterday"}`)}},
}

// HeatmapPageRule is one rule that selects the pages of a heatmap. All text is untrusted provider data.
type HeatmapPageRule struct {
	Attribute string `json:"attribute,omitempty"`
	Type      string `json:"type,omitempty"`
	Value     string `json:"value,omitempty"`
	Inverted  *bool  `json:"inverted,omitempty"`
}

// Heatmap is a heatmap configuration of a website. All text is untrusted provider data.
type Heatmap struct {
	IDSiteHsr         int               `json:"idSiteHsr"`
	IDSite            int               `json:"idSite"`
	Name              string            `json:"name,omitempty"`
	SampleRate        string            `json:"sample_rate,omitempty"`
	SampleLimit       *int              `json:"sample_limit,omitempty"`
	MatchPageRules    []HeatmapPageRule `json:"match_page_rules,omitempty"`
	ExcludedElements  string            `json:"excluded_elements,omitempty"`
	ScreenshotURL     string            `json:"screenshot_url,omitempty"`
	BreakpointMobile  *int              `json:"breakpoint_mobile,omitempty"`
	BreakpointTablet  *int              `json:"breakpoint_tablet,omitempty"`
	CreatedDate       string            `json:"created_date,omitempty"`
	UpdatedDate       string            `json:"updated_date,omitempty"`
	Status            string            `json:"status,omitempty"`
	CaptureManually   *bool             `json:"capture_manually,omitempty"`
	CreatedDatePretty string            `json:"created_date_pretty,omitempty"`
	HeatmapViewURL    string            `json:"heatmapViewUrl,omitempty"`
	PageTreeMirror    string            `json:"page_treemirror,omitempty"`
}

// HeatmapsResult is the answer of heatmaps.list.
type HeatmapsResult struct {
	IDSite     int       `json:"idSite"`
	Heatmaps   []Heatmap `json:"heatmaps"`
	HasMore    bool      `json:"has_more"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

// RecordedHeatmapResult is the answer of recorded-heatmaps.get.
type RecordedHeatmapResult struct {
	IDSite      int            `json:"idSite"`
	IDSiteHsr   int            `json:"idSiteHsr"`
	HeatmapType int            `json:"heatmapType"`
	DeviceType  int            `json:"deviceType"`
	Period      string         `json:"period"`
	Date        string         `json:"date"`
	Metadata    map[string]any `json:"metadata"`
	Activities  []any          `json:"activities"`
	HasMore     bool           `json:"has_more"`
	NextCursor  string         `json:"next_cursor,omitempty"`
}

type heatmapRuleJSON struct {
	Attribute *loose `json:"attribute"`
	Type      *loose `json:"type"`
	Value     *loose `json:"value"`
	Inverted  *loose `json:"inverted"`
}

// heatmapJSON is a heatmap as HeatmapSessionRecording reports it. Members are read loosely and none is trusted.
type heatmapJSON struct {
	ID                *loose          `json:"idsitehsr"`
	IDSite            *loose          `json:"idsite"`
	Name              *loose          `json:"name"`
	SampleRate        *loose          `json:"sample_rate"`
	SampleLimit       *loose          `json:"sample_limit"`
	MatchPageRules    json.RawMessage `json:"match_page_rules"`
	ExcludedElements  *loose          `json:"excluded_elements"`
	ScreenshotURL     *loose          `json:"screenshot_url"`
	BreakpointMobile  *loose          `json:"breakpoint_mobile"`
	BreakpointTablet  *loose          `json:"breakpoint_tablet"`
	CreatedDate       *loose          `json:"created_date"`
	UpdatedDate       *loose          `json:"updated_date"`
	Status            *loose          `json:"status"`
	CaptureManually   *loose          `json:"capture_manually"`
	CreatedDatePretty *loose          `json:"created_date_pretty"`
	HeatmapViewURL    *loose          `json:"heatmapViewUrl"`
	PageTreeMirror    json.RawMessage `json:"page_treemirror"`
}

// withoutToken returns an address of Matomo without the parameter token_auth, which Matomo adds to the view
// and replay addresses it hands out. The query is cut apart by hand so that no spelling of the parameter name
// survives: case, percent-encoding, a ; separator, and an array suffix. An address that cannot be parsed, or
// that still names the parameter or holds the token anywhere, yields "" and the field is left out.
func withoutToken(address, token string) string {
	address = strings.TrimSpace(address)
	parsed, err := url.Parse(address)
	if err != nil {
		return ""
	}
	var kept []string
	for _, pair := range strings.FieldsFunc(parsed.RawQuery, func(r rune) bool { return r == '&' || r == ';' }) {
		name, value, _ := strings.Cut(pair, "=")
		decoded, nameErr := url.QueryUnescape(name)
		if _, valueErr := url.QueryUnescape(value); nameErr != nil || valueErr != nil {
			return ""
		}
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(decoded)), "token_auth") {
			continue
		}
		kept = append(kept, pair)
	}
	parsed.RawQuery = strings.Join(kept, "&")
	parsed.ForceQuery = false
	if strings.Contains(strings.ToLower(parsed.Fragment), "token_auth") {
		parsed.Fragment = ""
		parsed.RawFragment = ""
	}
	cleaned := parsed.String()
	lower := strings.ToLower(cleaned)
	if strings.Contains(lower, "token_auth") || strings.Contains(lower, "token%5fauth") ||
		token != "" && (strings.Contains(cleaned, token) || strings.Contains(cleaned, url.QueryEscape(token))) {
		return ""
	}
	return cleaned
}

func (r heatmapRuleJSON) rule() HeatmapPageRule {
	return HeatmapPageRule{Attribute: r.Attribute.str(maxStringLength), Type: r.Type.str(maxStringLength),
		Value: r.Value.str(maxUrlLength), Inverted: r.Inverted.flag()}
}

// heatmap converts a row. found is false for a row of another website; ok is false for a row without a usable
// heatmap ID or without the website that proves where it belongs.
func (h heatmapJSON) heatmap(site int, token string) (heatmap Heatmap, found, ok bool) {
	id, row := h.ID.number(), h.IDSite.number()
	if id == nil || *id < 1 || row == nil {
		return Heatmap{}, false, false
	}
	if *row != site {
		return Heatmap{}, false, true
	}
	var rules []HeatmapPageRule
	if rows, valid := readRows[heatmapRuleJSON](h.MatchPageRules); valid {
		for i := range rows {
			if len(rules) < maxHeatmapRules {
				rules = append(rules, rows[i].rule())
			}
		}
	}
	view := ""
	if address := h.HeatmapViewURL.str(maxUrlLength * 2); address != "" {
		view = boundString(withoutToken(address, token), maxUrlLength)
	}
	return Heatmap{IDSiteHsr: *id, IDSite: site, Name: h.Name.str(maxStringLength), SampleRate: h.SampleRate.str(maxStringLength),
		SampleLimit: h.SampleLimit.number(), MatchPageRules: rules, ExcludedElements: h.ExcludedElements.str(maxUrlLength),
		ScreenshotURL: h.ScreenshotURL.str(maxUrlLength), BreakpointMobile: h.BreakpointMobile.number(),
		BreakpointTablet: h.BreakpointTablet.number(), CreatedDate: h.CreatedDate.str(maxStringLength),
		UpdatedDate: h.UpdatedDate.str(maxStringLength), Status: h.Status.str(maxStringLength),
		CaptureManually: h.CaptureManually.flag(), CreatedDatePretty: h.CreatedDatePretty.str(maxStringLength),
		HeatmapViewURL: view, PageTreeMirror: boundString(funnelText(h.PageTreeMirror, maxTreeMirror*2), maxTreeMirror)}, true, true
}

func heatmapNotFound(op string) error {
	return &provider.Error{Class: provider.ClassNotFound, Op: op,
		Message: "Matomo has no such heatmap for this website inside the targets of this connection"}
}

// fetchHeatmap is the one read that both proves a heatmap belongs to the website and delivers it: the answer
// carries idsite, and a heatmap of another website, or one without it, is never shown. A heatmap ID is global
// in Matomo, so the ID alone proves nothing.
func (c *Client) fetchHeatmap(ctx context.Context, op string, req requirement, site, id int) (Heatmap, error) {
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idSiteHsr": {strconv.Itoa(id)}}
	var answer json.RawMessage
	if err := c.call(ctx, op, req, "HeatmapSessionRecording.getHeatmap", params, &answer); err != nil {
		return Heatmap{}, err
	}
	trimmed := bytes.TrimSpace(answer)
	switch string(trimmed) {
	case "null", "false", "[]", "{}", "":
		return Heatmap{}, heatmapNotFound(op)
	}
	var row heatmapJSON
	if !bytes.HasPrefix(trimmed, []byte("{")) || json.Unmarshal(trimmed, &row) != nil {
		return Heatmap{}, provider.InvalidResponse(op, "Matomo returned an invalid heatmap")
	}
	heatmap, belongs, ok := row.heatmap(site, c.token)
	if !ok {
		return Heatmap{}, provider.InvalidResponse(op, "Matomo returned an invalid heatmap")
	}
	if !belongs {
		return Heatmap{}, heatmapNotFound(op)
	}
	if heatmap.IDSiteHsr != id {
		return Heatmap{}, provider.InvalidResponse(op, "Matomo returned another heatmap than asked for")
	}
	return heatmap, nil
}

func invokeHeatmapsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list heatmaps"
	var args struct {
		IDSite                *int   `json:"idSite"`
		IncludePageTreeMirror bool   `json:"includePageTreeMirror"`
		Limit                 int    `json:"limit"`
		Cursor                string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	paging, err := startPage(provider.CursorBinding(heatmapsList.ID, scopeBinding(resolved), site,
		args.IncludePageTreeMirror), args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}, "includePageTreeMirror": {"0"}}
	if args.IncludePageTreeMirror {
		params.Set("includePageTreeMirror", "1")
	}
	var answer json.RawMessage
	if err := client.call(ctx, op, heatmapsRequirement(heatmapsList.ID), "HeatmapSessionRecording.getHeatmaps", params, &answer); err != nil {
		return nil, err
	}
	rows, valid := readRows[heatmapJSON](answer)
	if !valid {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid heatmap list")
	}
	heatmaps := []Heatmap{}
	for i := range rows {
		if heatmap, belongs, usable := rows[i].heatmap(site, client.token); usable && belongs {
			heatmaps = append(heatmaps, heatmap)
		}
	}
	batch, next := slice(paging, heatmaps)
	return &HeatmapsResult{IDSite: site, Heatmaps: batch, HasMore: next != "", NextCursor: next}, nil
}

func checkHeatmapID(id *int) error {
	if id == nil || *id < 1 || *id > maxHeatmapID {
		return invalidArgument("idSiteHsr must be a positive integer heatmap ID from matomo.heatmaps.list")
	}
	return nil
}

func invokeHeatmapsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get heatmap"
	var args struct {
		IDSite    *int `json:"idSite"`
		IDSiteHsr *int `json:"idSiteHsr"`
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
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	heatmap, err := client.fetchHeatmap(ctx, op, heatmapsRequirement(heatmapsGet.ID), site, *args.IDSiteHsr)
	if err != nil {
		return nil, err
	}
	return &heatmap, nil
}

func invokeRecordedHeatmapsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get recorded heatmap"
	var args struct {
		IDSite      *int   `json:"idSite"`
		IDSiteHsr   *int   `json:"idSiteHsr"`
		HeatmapType *int   `json:"heatmapType"`
		DeviceType  *int   `json:"deviceType"`
		Period      string `json:"period"`
		Date        string `json:"date"`
		Segment     string `json:"segment"`
		Limit       int    `json:"limit"`
		Cursor      string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := checkHeatmapID(args.IDSiteHsr); err != nil {
		return nil, err
	}
	if args.HeatmapType == nil || *args.HeatmapType < 1 || *args.HeatmapType > 99 ||
		args.DeviceType == nil || *args.DeviceType < 1 || *args.DeviceType > 99 {
		return nil, invalidArgument("heatmapType and deviceType must be numbers from matomo.heatmap-options.list")
	}
	if err := singlePeriod(args.Period, args.Date); err != nil {
		return nil, err
	}
	if args.Segment != "" && !validSegmentExpression(args.Segment) {
		return nil, invalidArgument("segment must be terms of field, operator, and URL-encoded value " +
			"joined by ; or , (fields: matomo.segments.fields)")
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	paging, err := startPage(provider.CursorBinding(recordedHeatmapsGet.ID, scopeBinding(resolved), site, *args.IDSiteHsr,
		*args.HeatmapType, *args.DeviceType, args.Period, args.Date, args.Segment), args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := heatmapsRequirement(recordedHeatmapsGet.ID)
	// The heatmap ID counts across websites: this read binds it to the website before any behavior is read.
	if _, err := client.fetchHeatmap(ctx, op, req, site, *args.IDSiteHsr); err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idSiteHsr": {strconv.Itoa(*args.IDSiteHsr)},
		"period": {args.Period}, "date": {args.Date}}
	if args.Segment != "" {
		params.Set("segment", args.Segment)
	}
	var metadata json.RawMessage
	if err := client.call(ctx, op, req, "HeatmapSessionRecording.getRecordedHeatmapMetadata", params, &metadata); err != nil {
		return nil, err
	}
	counts, valid := recordedMetadata(metadata)
	if !valid {
		return nil, provider.InvalidResponse(op, "Matomo returned invalid heatmap metadata")
	}
	params.Set("heatmapType", strconv.Itoa(*args.HeatmapType))
	params.Set("deviceType", strconv.Itoa(*args.DeviceType))
	var answer json.RawMessage
	if err := client.call(ctx, op, req, "HeatmapSessionRecording.getRecordedHeatmap", params, &answer); err != nil {
		return nil, err
	}
	rows, valid := readRows[map[string]any](answer)
	if !valid {
		return nil, provider.InvalidResponse(op, "Matomo returned invalid heatmap activities")
	}
	activities := make([]any, len(rows))
	for i := range rows {
		activities[i] = boundedValue(rows[i], 0)
	}
	batch, next := slice(paging, activities)
	if batch == nil {
		batch = []any{}
	}
	return &RecordedHeatmapResult{IDSite: site, IDSiteHsr: *args.IDSiteHsr, HeatmapType: *args.HeatmapType,
		DeviceType: *args.DeviceType, Period: args.Period, Date: args.Date, Metadata: counts, Activities: batch,
		HasMore: next != "", NextCursor: next}, nil
}

// recordedMetadata reads the counts of a heatmap: an object, or the empty list Matomo answers without data.
func recordedMetadata(raw json.RawMessage) (map[string]any, bool) {
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte("[]")) || bytes.Equal(trimmed, []byte("null")) {
		return map[string]any{}, true
	}
	var counts map[string]any
	if !bytes.HasPrefix(trimmed, []byte("{")) || json.Unmarshal(trimmed, &counts) != nil {
		return nil, false
	}
	bounded, _ := boundedValue(counts, 0).(map[string]any)
	return bounded, true
}

func heatmapsOperations() []capability.Operation {
	return append([]capability.Operation{
		{Descriptor: heatmapsList, Handler: capability.Handler(invokeHeatmapsList)},
		{Descriptor: heatmapsGet, Handler: capability.Handler(invokeHeatmapsGet)},
		{Descriptor: recordedHeatmapsGet, Handler: capability.Handler(invokeRecordedHeatmapsGet)},
	}, heatmapOptionsOperations()...)
}
