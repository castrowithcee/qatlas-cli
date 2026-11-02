package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// formsSensitive covers the names, selectors, page rules, and addresses people wrote into forms, and the
	// counts of what visitors did in them.
	formsSensitive = "matomo-forms"
	formsPlugin    = "FormAnalytics"
	maxFormRules   = 100
	maxFormFields  = 200
	maxFormID      = 2147483647
	maxFormStates  = 10
	// maxRealtimeMinutes is Qatlas's own look-back limit, as for the live tools; the reference states none.
	maxRealtimeMinutes = 2880
	// defaultPopularForms is the filter_limit default of the reference; maxPopularForms is Qatlas's own limit.
	defaultPopularForms = 5
	maxPopularForms     = 100
)

var formStatusPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,31}$`)

func formsRequirement(tool string) requirement {
	return requirement{tool: tool, plugin: formsPlugin}
}

const (
	idFormSchema = `"idForm":{"type":"integer","minimum":1,"maximum":2147483647}`
	formRuleProp = `{"type":"array","items":{"type":"object","properties":{"attribute":{"type":"string"},` +
		`"pattern":{"type":"string"},"value":{"type":"string"}},"additionalProperties":false}}`
	formProps = `"idForm":{"type":"integer"},"idSite":{"type":"integer"},"idGoal":{"type":"integer"},` +
		`"name":{"type":"string"},"description":{"type":"string"},"status":{"type":"string"},` +
		`"auto_created":{"type":"boolean"},"created_date":{"type":"string"},"updated_date":{"type":"string"},` +
		`"in_overview":{"type":"integer"},"match_form_rules":` + formRuleProp + `,"match_page_rules":` + formRuleProp + `,` +
		`"conversion_rule_option":{"type":"string"},"conversion_rules":` + formRuleProp + `,` +
		`"fields":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},` +
		`"type":{"type":"string"},"displayName":{"type":"string"}},"additionalProperties":false}}`
)

func formFields(prefix string) []capability.Field {
	return []capability.Field{
		{Name: prefix + "idForm", Description: "Form ID, used as idForm by the other form tools"},
		{Name: prefix + "idSite", Description: "The website the form belongs to"},
		{Name: prefix + "idGoal", Description: "The goal that counts form conversions; absent when none is set"},
		{Name: prefix + "name", Description: "Form name, untrusted"},
		{Name: prefix + "description", Description: "Form description, untrusted"},
		{Name: prefix + "status", Description: "Status of the form, such as running; the values come from " +
			"matomo.form-options.list when the token may read them"},
		{Name: prefix + "auto_created", Description: "True when Matomo created the form itself"},
		{Name: prefix + "created_date", Description: "Time the form was created as Matomo stores it"},
		{Name: prefix + "updated_date", Description: "Time the form was last changed as Matomo stores it"},
		{Name: prefix + "in_overview", Description: "Whether the form shows in the overview, as Matomo gives it (0 or 1)"},
		{Name: prefix + "match_form_rules", Description: "Rules that select the form in a page, at most 100, untrusted; " +
			"each holds attribute, pattern, and value (text that is never fetched)"},
		{Name: prefix + "match_page_rules", Description: "Rules that select the pages of the form, at most 100, untrusted; " +
			"same members as the form rules"},
		{Name: prefix + "conversion_rule_option", Description: "How a conversion of the form is decided, such as " +
			"page_visit; the values come from matomo.form-options.list when the token may read them"},
		{Name: prefix + "conversion_rules", Description: "Rules of a conversion, at most 100, untrusted; same members " +
			"as the form rules"},
		{Name: prefix + "fields", Description: "Fields of the form, at most 200, untrusted: name, type, and " +
			"displayName as Matomo gives them"},
	}
}

var formsList = capability.Descriptor{
	ID: Provider + ".forms.list", Version: 1, Title: "List Matomo forms",
	Description: "List the forms of a website with their rules, fields, and status, in batches. Without statuses " +
		"Matomo lists all forms (getForms); with statuses only the forms of these statuses (getFormsByStatuses). Names, " +
		"rules, and fields are untrusted. The reports of the forms are read with matomo.reports.get. Needs the premium " +
		"plugin FormAnalytics and a view right on the website",
	Tags: []string{"matomo", "forms", "behavior", "list"}, Risk: readRisk(formsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` +
		`"statuses":{"type":"array","minItems":1,"maxItems":10,"uniqueItems":true,"items":{"type":"string",` +
		`"pattern":"` + formStatusPattern.String() + `"}},` +
		pagingKeys + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"forms":{"type":"array","items":{"type":"object","properties":{` + formProps + `},` +
		`"required":["idForm","idSite"],"additionalProperties":false}},"has_more":{"type":"boolean"},` +
		`"next_cursor":{"type":"string"}},"required":["idSite","forms","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument(),
		{Name: "statuses", Description: "1 to 10 different statuses to list, such as running; the values come from " +
			"matomo.form-options.list; all forms when omitted"}},
		pagingArguments...),
	Fields: append(append([]capability.Field{
		{Name: "idSite", Description: "The website the forms belong to"},
		{Name: "forms", Description: "Forms in the order Matomo lists them; empty without one"}},
		formFields("forms[].")...), pagingFields...),
	Examples: []capability.Example{
		{Description: "Forms of website 1", Arguments: json.RawMessage(`{"idSite":1}`)},
		{Description: "Running forms of website 1", Arguments: json.RawMessage(`{"idSite":1,"statuses":["running"]}`)}},
}

var formsGet = capability.Descriptor{
	ID: Provider + ".forms.get", Version: 1, Title: "Get a Matomo form",
	Description: "Read the configuration of one form of a website by its ID. The form ID counts across websites, so " +
		"the answer is shown only when Matomo reports it for this website, otherwise it is not found. Names, rules, " +
		"and fields are untrusted. Needs the premium plugin FormAnalytics and a view right on the website",
	Tags: []string{"matomo", "forms", "behavior", "get"}, Risk: readRisk(formsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idFormSchema + `},` +
		`"required":["idForm"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + formProps + `},` +
		`"required":["idForm","idSite"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(),
		{Name: "idForm", Required: true, Description: "ID of the form, from matomo.forms.list"}},
	Fields:   formFields(""),
	Examples: []capability.Example{{Description: "Form 2 of website 1", Arguments: json.RawMessage(`{"idSite":1,"idForm":2}`)}},
}

// formRealtimeKinds maps the fixed choices of the realtime tool to their Matomo methods.
var formRealtimeKinds = []struct{ name, method string }{
	{"counters", "FormAnalytics.getCounters"},
	{"popular-forms", "FormAnalytics.getCurrentMostPopularForms"},
}

const (
	formRealtimeKind = "counters"
	formPopularKind  = "popular-forms"
)

var formsRealtime = capability.Descriptor{
	ID: Provider + ".forms.realtime", Version: 1, Title: "Read the real-time Matomo form activity",
	Description: "Read how the forms of a website did within the last minutes: kind counters gives the counters of " +
		"all forms (getCounters), popular-forms lists the currently most popular forms (getCurrentMostPopularForms, 5 " +
		"by default and at most 100). The visits can be limited with a segment. Form names are untrusted. Needs the " +
		"premium plugin FormAnalytics and a view right on the website",
	Tags: []string{"matomo", "forms", "behavior", "realtime", "get"}, Risk: readRisk(formsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` +
		`"kind":{"type":"string","enum":["counters","popular-forms"]},` +
		`"lastMinutes":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxRealtimeMinutes) + `},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxPopularForms) + `},` +
		`"segment":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxSegmentLength) + `}},` +
		`"required":["kind","lastMinutes"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},"kind":{"type":"string"},` +
		`"lastMinutes":{"type":"integer"},"counters":{"type":"object","properties":{` + counterProps + `},` +
		`"additionalProperties":false},"forms":{"type":"array","items":{"type":"object","properties":{` +
		`"label":{"type":"string"},"idForm":{"type":"integer"},` + counterProps + `},"required":["idForm"],` +
		`"additionalProperties":false}}},"required":["idSite","kind","lastMinutes"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(),
		{Name: "kind", Required: true, Description: "counters for the counters of all forms, popular-forms for the " +
			"currently most popular forms"},
		{Name: "lastMinutes", Required: true, Description: "Minutes to look back from now, 1 through " +
			strconv.Itoa(maxRealtimeMinutes)},
		{Name: "limit", Description: "Forms to list, 1 through " + strconv.Itoa(maxPopularForms) + "; 5 (Matomo's " +
			"default) when omitted; only with kind popular-forms"},
		{Name: "segment", Description: "Matomo segment that filters the visits, such as referrerName==example.com; " +
			"fields from matomo.segments.fields, values URL-encoded"}},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website read"},
		{Name: "kind", Description: "The kind read"},
		{Name: "lastMinutes", Description: "The look-back asked for"},
		{Name: "counters", Description: "Only for kind counters. Counts of all forms in the look-back: " +
			"nb_form_viewers, nb_form_starters, nb_form_submitters, nb_form_resubmitters, nb_form_conversions; " +
			"avg_form_time_spent (seconds); the rates form_starters_rate, form_submitter_rate, " +
			"form_resubmitters_rate, form_conversion_rate as Matomo writes them, such as 2.6%"},
		{Name: "forms", Description: "Only for kind popular-forms. The most popular forms in the order Matomo gives " +
			"them: label (form name, untrusted), idForm, and the same counts and rates as counters (without " +
			"avg_form_time_spent)"}},
	Examples: []capability.Example{
		{Description: "Counters of the last 30 minutes", Arguments: json.RawMessage(
			`{"idSite":1,"kind":"counters","lastMinutes":30}`)},
		{Description: "The five most popular forms of the last hour", Arguments: json.RawMessage(
			`{"idSite":1,"kind":"popular-forms","lastMinutes":60}`)}},
}

const counterProps = `"nb_form_viewers":{"type":"integer"},"nb_form_starters":{"type":"integer"},` +
	`"nb_form_submitters":{"type":"integer"},"nb_form_resubmitters":{"type":"integer"},` +
	`"nb_form_conversions":{"type":"integer"},"avg_form_time_spent":{"type":"number"},` +
	`"form_starters_rate":{"type":"string"},"form_submitter_rate":{"type":"string"},` +
	`"form_resubmitters_rate":{"type":"string"},"form_conversion_rate":{"type":"string"}`

// FormRule is one rule of a form. All text is untrusted provider data.
type FormRule struct {
	Attribute string `json:"attribute,omitempty"`
	Pattern   string `json:"pattern,omitempty"`
	Value     string `json:"value,omitempty"`
}

// FormField is one field of a form. All text is untrusted provider data.
type FormField struct {
	Name        string `json:"name,omitempty"`
	Type        string `json:"type,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
}

// Form is a form of a website. All text is untrusted provider data.
type Form struct {
	IDForm               int         `json:"idForm"`
	IDSite               int         `json:"idSite"`
	IDGoal               *int        `json:"idGoal,omitempty"`
	Name                 string      `json:"name,omitempty"`
	Description          string      `json:"description,omitempty"`
	Status               string      `json:"status,omitempty"`
	AutoCreated          *bool       `json:"auto_created,omitempty"`
	CreatedDate          string      `json:"created_date,omitempty"`
	UpdatedDate          string      `json:"updated_date,omitempty"`
	InOverview           *int        `json:"in_overview,omitempty"`
	MatchFormRules       []FormRule  `json:"match_form_rules,omitempty"`
	MatchPageRules       []FormRule  `json:"match_page_rules,omitempty"`
	ConversionRuleOption string      `json:"conversion_rule_option,omitempty"`
	ConversionRules      []FormRule  `json:"conversion_rules,omitempty"`
	Fields               []FormField `json:"fields,omitempty"`
}

// FormsResult is the answer of forms.list.
type FormsResult struct {
	IDSite     int    `json:"idSite"`
	Forms      []Form `json:"forms"`
	HasMore    bool   `json:"has_more"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// FormCounts are the counts and rates Matomo reports for one form or for all forms.
type FormCounts struct {
	Viewers          *int     `json:"nb_form_viewers,omitempty"`
	Starters         *int     `json:"nb_form_starters,omitempty"`
	Submitters       *int     `json:"nb_form_submitters,omitempty"`
	Resubmitters     *int     `json:"nb_form_resubmitters,omitempty"`
	Conversions      *int     `json:"nb_form_conversions,omitempty"`
	AvgTimeSpent     *float64 `json:"avg_form_time_spent,omitempty"`
	StartersRate     string   `json:"form_starters_rate,omitempty"`
	SubmitterRate    string   `json:"form_submitter_rate,omitempty"`
	ResubmittersRate string   `json:"form_resubmitters_rate,omitempty"`
	ConversionRate   string   `json:"form_conversion_rate,omitempty"`
}

// PopularForm is one row of the currently most popular forms. The label is untrusted provider data.
type PopularForm struct {
	Label  string `json:"label,omitempty"`
	IDForm int    `json:"idForm"`
	FormCounts
}

// FormsRealtimeResult is the answer of forms.realtime.
type FormsRealtimeResult struct {
	IDSite      int           `json:"idSite"`
	Kind        string        `json:"kind"`
	LastMinutes int           `json:"lastMinutes"`
	Counters    *FormCounts   `json:"counters,omitempty"`
	Forms       []PopularForm `json:"forms,omitempty"`
}

type formRuleJSON struct {
	Attribute *loose `json:"attribute"`
	Pattern   *loose `json:"pattern"`
	Value     *loose `json:"value"`
}

type formFieldJSON struct {
	Name        *loose `json:"name"`
	Type        *loose `json:"type"`
	DisplayName *loose `json:"displayName"`
}

// formJSON is a form as FormAnalytics reports it. Members are read loosely and none is trusted.
type formJSON struct {
	ID                   *loose          `json:"idsiteform"`
	IDSite               *loose          `json:"idsite"`
	IDGoal               *loose          `json:"idgoal"`
	Name                 *loose          `json:"name"`
	Description          *loose          `json:"description"`
	Status               *loose          `json:"status"`
	AutoCreated          *loose          `json:"auto_created"`
	CreatedDate          *loose          `json:"created_date"`
	UpdatedDate          *loose          `json:"updated_date"`
	InOverview           *loose          `json:"in_overview"`
	MatchFormRules       json.RawMessage `json:"match_form_rules"`
	MatchPageRules       json.RawMessage `json:"match_page_rules"`
	ConversionRuleOption *loose          `json:"conversion_rule_option"`
	ConversionRules      json.RawMessage `json:"conversion_rules"`
	Fields               json.RawMessage `json:"fields"`
}

func formRules(raw json.RawMessage) []FormRule {
	rows, valid := readRows[formRuleJSON](raw)
	if !valid {
		return nil
	}
	var rules []FormRule
	for _, row := range rows {
		if len(rules) == maxFormRules {
			break
		}
		rules = append(rules, FormRule{Attribute: row.Attribute.str(maxStringLength),
			Pattern: row.Pattern.str(maxStringLength), Value: row.Value.str(maxUrlLength)})
	}
	return rules
}

func formFieldList(raw json.RawMessage) []FormField {
	rows, valid := readRows[formFieldJSON](raw)
	if !valid {
		return nil
	}
	var fields []FormField
	for _, row := range rows {
		if len(fields) == maxFormFields {
			break
		}
		fields = append(fields, FormField{Name: row.Name.str(maxStringLength), Type: row.Type.str(maxStringLength),
			DisplayName: row.DisplayName.str(maxStringLength)})
	}
	return fields
}

// form converts a row. belongs is false for a row of another website and for a row without a website; ok is
// false for a row without a usable form ID.
func (f formJSON) form(site int) (form Form, belongs, ok bool) {
	id := f.ID.number()
	if id == nil || *id < 1 {
		return Form{}, false, false
	}
	if row := f.IDSite.number(); row == nil || *row != site {
		return Form{}, false, true
	}
	return Form{IDForm: *id, IDSite: site, IDGoal: f.IDGoal.number(), Name: f.Name.str(maxStringLength),
		Description: f.Description.str(maxUrlLength), Status: f.Status.str(maxStringLength),
		AutoCreated: f.AutoCreated.flag(), CreatedDate: f.CreatedDate.str(maxStringLength),
		UpdatedDate: f.UpdatedDate.str(maxStringLength), InOverview: f.InOverview.number(),
		MatchFormRules: formRules(f.MatchFormRules), MatchPageRules: formRules(f.MatchPageRules),
		ConversionRuleOption: f.ConversionRuleOption.str(maxStringLength), ConversionRules: formRules(f.ConversionRules),
		Fields: formFieldList(f.Fields)}, true, true
}

func formNotFound(op string) error {
	return &provider.Error{Class: provider.ClassNotFound, Op: op,
		Message: "Matomo has no such form for this website inside the targets of this connection"}
}

// fetchForm reads one form. The answer carries idsite, and a form of another website, or one without it, is
// never shown: a form ID is not tied to a website in the request, so the ID alone proves nothing.
func (c *Client) fetchForm(ctx context.Context, op string, req requirement, site, id int) (Form, error) {
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idForm": {strconv.Itoa(id)}}
	var answer json.RawMessage
	if err := c.call(ctx, op, req, "FormAnalytics.getForm", params, &answer); err != nil {
		return Form{}, err
	}
	trimmed := bytes.TrimSpace(answer)
	switch string(trimmed) {
	case "null", "false", "[]", "{}", "":
		return Form{}, formNotFound(op)
	}
	var row formJSON
	if !bytes.HasPrefix(trimmed, []byte("{")) || json.Unmarshal(trimmed, &row) != nil {
		return Form{}, provider.InvalidResponse(op, "Matomo returned an invalid form")
	}
	form, belongs, ok := row.form(site)
	if !ok {
		return Form{}, provider.InvalidResponse(op, "Matomo returned an invalid form")
	}
	if !belongs {
		return Form{}, formNotFound(op)
	}
	if form.IDForm != id {
		return Form{}, provider.InvalidResponse(op, "Matomo returned another form than asked for")
	}
	return form, nil
}

func checkSegmentExpression(segment string) error {
	if segment != "" && !validSegmentExpression(segment) {
		return invalidArgument("segment must be terms of field, operator, and URL-encoded value " +
			"joined by ; or , (fields: matomo.segments.fields)")
	}
	return nil
}

func invokeFormsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list forms"
	var args struct {
		IDSite   *int     `json:"idSite"`
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
	if args.Statuses != nil && (len(args.Statuses) == 0 || len(args.Statuses) > maxFormStates) {
		return nil, invalidArgument("statuses must hold 1 to " + strconv.Itoa(maxFormStates) + " statuses")
	}
	for i, status := range args.Statuses {
		if !formStatusPattern.MatchString(status) || slices.Index(args.Statuses, status) != i {
			return nil, invalidArgument("statuses must be different statuses such as running (matomo.form-options.list)")
		}
	}
	paging, err := startPage(provider.CursorBinding(formsList.ID, scopeBinding(resolved), site, args.Statuses),
		args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}}
	method := "FormAnalytics.getForms"
	if len(args.Statuses) > 0 {
		method = "FormAnalytics.getFormsByStatuses"
		for _, status := range args.Statuses {
			params.Add("statuses[]", status)
		}
	}
	var answer json.RawMessage
	if err := client.call(ctx, op, formsRequirement(formsList.ID), method, params, &answer); err != nil {
		return nil, err
	}
	rows, valid := readRows[formJSON](answer)
	if !valid {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid form list")
	}
	forms := []Form{}
	for i := range rows {
		if form, belongs, usable := rows[i].form(site); usable && belongs {
			forms = append(forms, form)
		}
	}
	batch, next := slice(paging, forms)
	return &FormsResult{IDSite: site, Forms: batch, HasMore: next != "", NextCursor: next}, nil
}

func invokeFormsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get form"
	var args struct {
		IDSite *int `json:"idSite"`
		IDForm *int `json:"idForm"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if args.IDForm == nil || *args.IDForm < 1 || *args.IDForm > maxFormID {
		return nil, invalidArgument("idForm must be a positive integer form ID from matomo.forms.list")
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	form, err := client.fetchForm(ctx, op, formsRequirement(formsGet.ID), site, *args.IDForm)
	if err != nil {
		return nil, err
	}
	return &form, nil
}

type formCountsJSON struct {
	Viewers          *loose          `json:"nb_form_viewers"`
	Starters         *loose          `json:"nb_form_starters"`
	Submitters       *loose          `json:"nb_form_submitters"`
	Resubmitters     *loose          `json:"nb_form_resubmitters"`
	Conversions      *loose          `json:"nb_form_conversions"`
	AvgTimeSpent     json.RawMessage `json:"avg_form_time_spent"`
	StartersRate     *loose          `json:"form_starters_rate"`
	SubmitterRate    *loose          `json:"form_submitter_rate"`
	ResubmittersRate *loose          `json:"form_resubmitters_rate"`
	ConversionRate   *loose          `json:"form_conversion_rate"`
}

func (c formCountsJSON) counts() FormCounts {
	var avg *float64
	if len(c.AvgTimeSpent) > 0 {
		var value loose
		if json.Unmarshal(c.AvgTimeSpent, &value) == nil {
			avg = value.numeric()
		}
	}
	return FormCounts{Viewers: c.Viewers.number(), Starters: c.Starters.number(), Submitters: c.Submitters.number(),
		Resubmitters: c.Resubmitters.number(), Conversions: c.Conversions.number(), AvgTimeSpent: avg,
		StartersRate: c.StartersRate.str(maxStringLength), SubmitterRate: c.SubmitterRate.str(maxStringLength),
		ResubmittersRate: c.ResubmittersRate.str(maxStringLength), ConversionRate: c.ConversionRate.str(maxStringLength)}
}

type popularFormJSON struct {
	formCountsJSON
	Label  *loose `json:"label"`
	ID     *loose `json:"idsiteform"`
	IDSite *loose `json:"idsite"`
}

func invokeFormsRealtime(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get form real time"
	var args struct {
		IDSite      *int   `json:"idSite"`
		Kind        string `json:"kind"`
		LastMinutes *int   `json:"lastMinutes"`
		Limit       *int   `json:"limit"`
		Segment     string `json:"segment"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	index := slices.IndexFunc(formRealtimeKinds, func(k struct{ name, method string }) bool { return k.name == args.Kind })
	if index < 0 {
		return nil, invalidArgument("kind must be " + formRealtimeKind + " or " + formPopularKind)
	}
	if args.LastMinutes == nil || *args.LastMinutes < 1 || *args.LastMinutes > maxRealtimeMinutes {
		return nil, invalidArgument("lastMinutes must be between 1 and " + strconv.Itoa(maxRealtimeMinutes))
	}
	if args.Limit != nil && (args.Kind != formPopularKind || *args.Limit < 1 || *args.Limit > maxPopularForms) {
		return nil, invalidArgument("limit must be between 1 and " + strconv.Itoa(maxPopularForms) +
			" and is used only with kind " + formPopularKind)
	}
	if err := checkSegmentExpression(args.Segment); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}, "lastMinutes": {strconv.Itoa(*args.LastMinutes)}}
	if args.Segment != "" {
		params.Set("segment", args.Segment)
	}
	limit := defaultPopularForms
	if args.Kind == formPopularKind {
		if args.Limit != nil {
			limit = *args.Limit
		}
		params.Set("filter_limit", strconv.Itoa(limit))
	}
	var answer json.RawMessage
	if err := client.call(ctx, op, formsRequirement(formsRealtime.ID), formRealtimeKinds[index].method, params, &answer); err != nil {
		return nil, err
	}
	out := &FormsRealtimeResult{IDSite: site, Kind: args.Kind, LastMinutes: *args.LastMinutes}
	if args.Kind == formRealtimeKind {
		trimmed := bytes.TrimSpace(answer)
		var counts formCountsJSON
		switch {
		case bytes.Equal(trimmed, []byte("[]")) || bytes.Equal(trimmed, []byte("null")):
		case !bytes.HasPrefix(trimmed, []byte("{")) || json.Unmarshal(trimmed, &counts) != nil:
			return nil, provider.InvalidResponse(op, "Matomo returned invalid form counters")
		}
		parsed := counts.counts()
		out.Counters = &parsed
		return out, nil
	}
	rows, valid := readRows[popularFormJSON](answer)
	if !valid {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid list of forms")
	}
	for i := range rows {
		id := rows[i].ID.number()
		// A row that names a website must name this one; the reference documents no website member for it.
		if site := rows[i].IDSite.number(); id == nil || *id < 1 || len(out.Forms) == limit ||
			site != nil && *site != out.IDSite {
			continue
		}
		out.Forms = append(out.Forms, PopularForm{Label: rows[i].Label.str(maxStringLength), IDForm: *id,
			FormCounts: rows[i].counts()})
	}
	return out, nil
}

func formsOperations() []capability.Operation {
	return append([]capability.Operation{
		{Descriptor: formsList, Handler: capability.Handler(invokeFormsList)},
		{Descriptor: formsGet, Handler: capability.Handler(invokeFormsGet)},
		{Descriptor: formsRealtime, Handler: capability.Handler(invokeFormsRealtime)},
	}, append(formOptionsOperations(), formChangeOperations()...)...)
}
