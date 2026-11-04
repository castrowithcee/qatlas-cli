package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

const (
	maxFormName        = 255
	maxFormDescription = maxUrlLength
	maxFormRuleValue   = maxUrlLength
	maxFormFieldText   = 255
)

var (
	formSentOnceList = sentOnceFor("form", "matomo.forms.list")
	formSentOnceGet  = sentOnceFor("form", "matomo.forms.get")
	// formRuleWord limits attributes, patterns, and the conversion rule option to the lowercase words the
	// reference shows (form_name, equals, page_visit); the values come from matomo.form-options.list.
	formRuleWord = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

// formChangeRisk is the risk of every form change: confirmed, open to the instance, and about the form
// configuration of a website.
func formChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: formsSensitive}
}

const (
	formRuleInputSchema = `{"type":"array","minItems":1,"maxItems":100,"items":{"type":"object","properties":{` +
		`"attribute":{"type":"string","pattern":"^[a-z][a-z0-9_]{0,63}$"},` +
		`"pattern":{"type":"string","pattern":"^[a-z][a-z0-9_]{0,63}$"},` +
		`"value":{"type":"string","minLength":1,"maxLength":2048}},` +
		`"required":["attribute","pattern","value"],"additionalProperties":false}}`
	formSettingsSchema = `"name":{"type":"string","minLength":1,"maxLength":255},` +
		`"description":{"type":"string","maxLength":2048},` +
		`"matchFormRules":` + formRuleInputSchema + `,"matchPageRules":` + formRuleInputSchema + `,` +
		`"conversionRuleOption":{"type":"string","pattern":"^[a-z][a-z0-9_]{0,63}$"},` +
		`"conversionRules":` + formRuleInputSchema + `,` +
		`"idGoal":{"type":"integer","minimum":1,"maximum":2147483647}`
	formSettingsHint = "Settings that are not given are not sent, so Matomo applies its own defaults (by its " +
		"signature: description empty, no rules, conversionRuleOption page_visit, no goal)"
)

func formSettingArguments(nameRequired bool, given string) []capability.Argument {
	return []capability.Argument{
		{Name: "name", Required: nameRequired, Description: "Name of the form, 1 to 255 characters"},
		{Name: "description", Description: "Description of the form, up to 2048 characters" + given},
		{Name: "matchFormRules", Description: "1 to 100 rules that select the forms of a page to track, any one " +
			"matching starts tracking; each holds attribute, pattern, and value (attribute and pattern are lowercase " +
			"words from matomo.form-options.list, value 1 to 2048 characters)" + given},
		{Name: "matchPageRules", Description: "1 to 100 rules that restrict tracking to matching pages, any one " +
			"matching starts tracking; same members as matchFormRules" + given},
		{Name: "conversionRuleOption", Description: "How a conversion is decided, a lowercase word such as " +
			"page_visit from matomo.form-options.list" + given},
		{Name: "conversionRules", Description: "1 to 100 rules that count a conversion when a visitor matches one " +
			"of the pages; same members as matchFormRules" + given},
		{Name: "idGoal", Description: "ID of the goal to trigger when the form converts, from matomo.form-options.list" + given}}
}

var formsAdd = capability.Descriptor{
	ID: Provider + ".forms.add", Version: 1, Title: "Create a Matomo form",
	Description: "Create a form on a website with a name and optional description, rules, conversion settings, and " +
		"goal. " + formSettingsHint + ". The rule format follows the reference's examples and is not verified " +
		"against Matomo. A repeated call creates a second form. Needs the premium plugin FormAnalytics and, " +
		"expected, a write right on the website" + formSentOnceList,
	Tags: []string{"matomo", "forms", "behavior", "create"}, Provider: Provider,
	Risk: formChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + formSettingsSchema + `},` +
		`"required":["name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idForm":{"type":"integer"},"created":{"type":"boolean"}},"required":["idSite","idForm","created"],` +
		`"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument()}, formSettingArguments(true, "; Matomo's default when omitted")...),
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the form belongs to"},
		{Name: "idForm", Description: "ID of the new form, used as idForm by the other form tools"},
		{Name: "created", Description: "True once Matomo created the form"}},
	Examples: []capability.Example{{Description: "Sign-up form of website 1", Arguments: json.RawMessage(
		`{"idSite":1,"name":"Sign-up","matchFormRules":[{"attribute":"form_name","pattern":"equals","value":"signup"}],` +
			`"matchPageRules":[{"attribute":"path","pattern":"equals","value":"/sign-up"}]}`)}},
}

var formsUpdate = capability.Descriptor{
	ID: Provider + ".forms.update", Version: 1, Title: "Update a Matomo form",
	Description: "Update a form of a website in one of two ways, each one request. With name, replace the " +
		"configuration as a whole (updateForm): " + formSettingsHint + ", so a setting left out may reset a value " +
		"the form had; read the form with matomo.forms.get and pass every setting to keep. With fields instead of " +
		"name, set the display names of known fields (updateFormFieldDisplayName), matched by field name, and no " +
		"setting may be given. Exactly one of name and fields is required. The form is read first and must belong " +
		"to the website, otherwise it is not found. The rule format follows the reference's examples and is not " +
		"verified against Matomo. Needs the premium plugin FormAnalytics and, expected, a write right on the " +
		"website; idempotent in effect" + formSentOnceGet,
	Tags: []string{"matomo", "forms", "behavior", "update"}, Provider: Provider,
	Risk: formChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idFormSchema + `,` +
		formSettingsSchema + `,"fields":{"type":"array","minItems":1,"maxItems":200,"items":{"type":"object",` +
		`"properties":{"name":{"type":"string","minLength":1,"maxLength":255},` +
		`"displayName":{"type":"string","minLength":1,"maxLength":255}},"required":["name","displayName"],` +
		`"additionalProperties":false}}},"required":["idForm"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idForm":{"type":"integer"},"saved":{"type":"boolean"}},"required":["idSite","idForm","saved"],` +
		`"additionalProperties":false}`),
	Arguments: append(append([]capability.Argument{idSiteArgument(),
		{Name: "idForm", Required: true, Description: "ID of the form to update, from matomo.forms.list"}},
		formSettingArguments(false, "; reset to Matomo's default when omitted, only with name")...),
		capability.Argument{Name: "fields", Description: "1 to 200 fields to name, each holding name (the field " +
			"name from matomo.forms.get, 1 to 255 characters) and displayName (1 to 255 characters), each field " +
			"once; instead of name"}),
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the form belongs to"},
		{Name: "idForm", Description: "The form that was changed"},
		{Name: "saved", Description: "True once Matomo accepted the change"}},
	Examples: []capability.Example{
		{Description: "Rename form 2 of website 1 and keep one form rule", Arguments: json.RawMessage(
			`{"idSite":1,"idForm":2,"name":"Sign-up","matchFormRules":[{"attribute":"form_name","pattern":"equals",` +
				`"value":"signup"}]}`)},
		{Description: "Name a field of form 2", Arguments: json.RawMessage(
			`{"idSite":1,"idForm":2,"fields":[{"name":"input1","displayName":"Email"}]}`)}},
}

// formAction is archive or delete: a change of a form that takes only the website and the form.
type formAction struct {
	action     string
	method     string
	op         string
	title      string
	summary    string
	done       string
	effect     capability.Effect
	listedOnly bool
}

var formActions = []formAction{
	{action: "archive", method: "FormAnalytics.archiveForm", op: "archive form", title: "Archive a Matomo form",
		summary: "Archive a form of a website. Matomo does not document what archiving does to tracking or to the " +
			"data, or whether it can be undone", done: "archived", effect: capability.EffectUpdate},
	{action: "delete", method: "FormAnalytics.deleteForm", op: "delete form", title: "Delete a Matomo form",
		summary: "Delete a form of a website; Qatlas Connect offers no way to restore it. Matomo does not document what " +
			"happens to the recorded data", done: "deleted", effect: capability.EffectDelete, listedOnly: true},
}

func (a formAction) descriptor() capability.Descriptor {
	offer := ""
	if a.listedOnly {
		offer = "; offered only where a connection's tools list names it"
	}
	return capability.Descriptor{
		ID: Provider + ".forms." + a.action, Version: 1, Title: a.title,
		Description: a.summary + ". The form is read first and must belong to the website, otherwise it is not " +
			"found. Needs the premium plugin FormAnalytics and, expected, a write right on the website; idempotent " +
			"in effect" + formSentOnceGet + offer,
		Tags: []string{"matomo", "forms", "behavior", string(a.effect)}, Provider: Provider,
		RequiresToolAllowList: a.listedOnly,
		Risk:                  formChangeRisk(a.effect, capability.IdempotencyIdempotent),
		InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idFormSchema + `},` +
			`"required":["idForm"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
			`"idForm":{"type":"integer"},"` + a.done + `":{"type":"boolean"}},` +
			`"required":["idSite","idForm","` + a.done + `"],"additionalProperties":false}`),
		Arguments: []capability.Argument{idSiteArgument(),
			{Name: "idForm", Required: true, Description: "ID of the form, from matomo.forms.list"}},
		Fields: []capability.Field{
			{Name: "idSite", Description: "The website the form belongs to"},
			{Name: "idForm", Description: "The form that was changed"},
			{Name: a.done, Description: "True once Matomo accepted the change"}},
		Examples: []capability.Example{{Description: a.title + ", form 2 of website 1",
			Arguments: json.RawMessage(`{"idSite":1,"idForm":2}`)}},
	}
}

// FormCreated is the answer of forms.add.
type FormCreated struct {
	IDSite  int  `json:"idSite"`
	IDForm  int  `json:"idForm"`
	Created bool `json:"created"`
}

// FormSaved is the answer of forms.update.
type FormSaved struct {
	IDSite int  `json:"idSite"`
	IDForm int  `json:"idForm"`
	Saved  bool `json:"saved"`
}

// FormActed is the answer of forms.archive and forms.delete; exactly one of the flags is present.
type FormActed struct {
	IDSite   int  `json:"idSite"`
	IDForm   int  `json:"idForm"`
	Archived bool `json:"archived,omitempty"`
	Deleted  bool `json:"deleted,omitempty"`
}

type formRuleInput struct {
	Attribute string `json:"attribute"`
	Pattern   string `json:"pattern"`
	Value     string `json:"value"`
}

type formFieldInput struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

// formSettings holds the settings of add and update. Pointers and nil slices tell a setting that is not given,
// which is not sent, from one that is.
type formSettings struct {
	Name                 *string         `json:"name"`
	Description          *string         `json:"description"`
	MatchFormRules       []formRuleInput `json:"matchFormRules"`
	MatchPageRules       []formRuleInput `json:"matchPageRules"`
	ConversionRuleOption *string         `json:"conversionRuleOption"`
	ConversionRules      []formRuleInput `json:"conversionRules"`
	IDGoal               *int            `json:"idGoal"`
}

// givenBesidesName tells whether any setting besides the name was passed.
func (s formSettings) givenBesidesName() bool {
	return s.Description != nil || s.MatchFormRules != nil || s.MatchPageRules != nil ||
		s.ConversionRuleOption != nil || s.ConversionRules != nil || s.IDGoal != nil
}

// formRuleParams checks one rule list and adds it as key[N][member]. An empty list cannot be sent in a form
// body, so it is refused instead of being taken for "no rules".
func formRuleParams(params url.Values, key string, rules []formRuleInput) error {
	if rules == nil {
		return nil
	}
	if len(rules) < 1 || len(rules) > maxFormRules {
		return invalidArgument(key + " must hold 1 to " + strconv.Itoa(maxFormRules) + " rules")
	}
	for i, rule := range rules {
		if !formRuleWord.MatchString(rule.Attribute) || !formRuleWord.MatchString(rule.Pattern) || rule.Value == "" ||
			utf8.RuneCountInString(rule.Value) > maxFormRuleValue {
			return invalidArgument("each rule in " + key + " needs an attribute and a pattern of lowercase " +
				"words and a value of 1 to 2048 characters")
		}
		prefix := key + "[" + strconv.Itoa(i) + "]"
		params.Set(prefix+"[attribute]", rule.Attribute)
		params.Set(prefix+"[pattern]", rule.Pattern)
		params.Set(prefix+"[value]", rule.Value)
	}
	return nil
}

// params checks the settings the way the descriptor promises and adds only those that are given. Matomo stays
// the authority: it checks again, and its answer decides.
func (s formSettings) params(params url.Values) error {
	if s.Name == nil || strings.TrimSpace(*s.Name) == "" || utf8.RuneCountInString(*s.Name) > maxFormName {
		return invalidArgument("name must hold 1 to 255 characters")
	}
	params.Set("name", *s.Name)
	if s.Description != nil {
		if utf8.RuneCountInString(*s.Description) > maxFormDescription {
			return invalidArgument("description is longer than allowed")
		}
		params.Set("description", *s.Description)
	}
	for _, list := range []struct {
		key   string
		rules []formRuleInput
	}{{"matchFormRules", s.MatchFormRules}, {"matchPageRules", s.MatchPageRules}, {"conversionRules", s.ConversionRules}} {
		if err := formRuleParams(params, list.key, list.rules); err != nil {
			return err
		}
	}
	if s.ConversionRuleOption != nil {
		if !formRuleWord.MatchString(*s.ConversionRuleOption) {
			return invalidArgument("conversionRuleOption must be a lowercase word such as page_visit")
		}
		params.Set("conversionRuleOption", *s.ConversionRuleOption)
	}
	if s.IDGoal != nil {
		if *s.IDGoal < 1 || *s.IDGoal > maxFormID {
			return invalidArgument("idGoal must be a positive integer goal ID")
		}
		params.Set("idGoal", strconv.Itoa(*s.IDGoal))
	}
	return nil
}

func checkFormID(id *int) error {
	if id == nil || *id < 1 || *id > maxFormID {
		return invalidArgument("idForm must be a positive integer form ID from matomo.forms.list")
	}
	return nil
}

func fieldParams(params url.Values, fields []formFieldInput) error {
	if len(fields) < 1 || len(fields) > maxFormFields {
		return invalidArgument("fields must hold 1 to " + strconv.Itoa(maxFormFields) + " fields")
	}
	seen := make([]string, 0, len(fields))
	for i, field := range fields {
		if field.Name == "" || utf8.RuneCountInString(field.Name) > maxFormFieldText ||
			field.DisplayName == "" || utf8.RuneCountInString(field.DisplayName) > maxFormFieldText {
			return invalidArgument("each field needs a name and a displayName of 1 to 255 characters")
		}
		if slices.Contains(seen, field.Name) {
			return invalidArgument("fields must name each field once")
		}
		seen = append(seen, field.Name)
		prefix := "fields[" + strconv.Itoa(i) + "]"
		params.Set(prefix+"[name]", field.Name)
		params.Set(prefix+"[displayName]", field.DisplayName)
	}
	return nil
}

func invokeFormsAdd(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "add form"
	var args struct {
		IDSite *int `json:"idSite"`
		formSettings
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
	if err := client.change(ctx, op, formsRequirement(formsAdd.ID), "FormAnalytics.addForm", params, formSentOnceList,
		&answer); err != nil {
		return nil, err
	}
	var id int
	if scalar(answer, &id) != nil || id < 1 {
		return nil, unclearChange(op, "Matomo returned no ID of the form", formSentOnceList)
	}
	return &FormCreated{IDSite: site, IDForm: id, Created: true}, nil
}

func invokeFormsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update form"
	var args struct {
		IDSite *int `json:"idSite"`
		IDForm *int `json:"idForm"`
		formSettings
		Fields []formFieldInput `json:"fields"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := checkFormID(args.IDForm); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idForm": {strconv.Itoa(*args.IDForm)}}
	method := "FormAnalytics.updateForm"
	switch {
	case args.Fields != nil && (args.Name != nil || args.givenBesidesName()):
		return nil, invalidArgument("fields is given instead of name and the other settings, not together with them")
	case args.Fields != nil:
		method = "FormAnalytics.updateFormFieldDisplayName"
		err = fieldParams(params, args.Fields)
	default:
		err = args.params(params)
	}
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := formsRequirement(formsUpdate.ID)
	// The form ID counts across websites: one read must show it on this website before it is changed.
	if _, err := client.fetchForm(ctx, op, req, site, *args.IDForm); err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, req, method, params, formSentOnceGet, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the form", formSentOnceGet)
	}
	return &FormSaved{IDSite: site, IDForm: *args.IDForm, Saved: true}, nil
}

func (a formAction) invoke(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var args struct {
		IDSite *int `json:"idSite"`
		IDForm *int `json:"idForm"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := checkFormID(args.IDForm); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idForm": {strconv.Itoa(*args.IDForm)}}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := formsRequirement(Provider + ".forms." + a.action)
	// The form ID counts across websites: one read must show it on this website before anything is changed.
	if _, err := client.fetchForm(ctx, a.op, req, site, *args.IDForm); err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, a.op, req, a.method, params, formSentOnceGet, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(a.op, "Matomo returned no confirmation of the form", formSentOnceGet)
	}
	out := &FormActed{IDSite: site, IDForm: *args.IDForm}
	if a.action == "delete" {
		out.Deleted = true
	} else {
		out.Archived = true
	}
	return out, nil
}

func formChangeOperations() []capability.Operation {
	ops := []capability.Operation{
		{Descriptor: formsAdd, Handler: capability.Handler(invokeFormsAdd)},
		{Descriptor: formsUpdate, Handler: capability.Handler(invokeFormsUpdate)},
	}
	for _, a := range formActions {
		ops = append(ops, capability.Operation{Descriptor: a.descriptor(), Handler: capability.Handler(a.invoke)})
	}
	return ops
}
