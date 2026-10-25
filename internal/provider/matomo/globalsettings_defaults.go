package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const maxTimezoneLength = 64

var currencyCode = regexp.MustCompile(`^[A-Z]{3}$`)

const globalDefaultNote = "It needs Super User rights and a connection whose targets name the instance, and it " +
	"changes the default of every website of the instance, including those outside the connection's website " +
	"targets; read the current value first with matomo.global-settings.get; idempotent"

func globalDefaultDescriptor(id, title, description string, properties, required string, args []capability.Argument,
	fields []capability.Field, outputProperties, example string) capability.Descriptor {
	return capability.Descriptor{
		ID: id, Version: 1, Title: title, Description: description + " " + globalDefaultNote + globalSentOnce,
		Tags: []string{"matomo", "global-settings", "defaults", "update", "instance"},
		Risk: siteChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
		InputSchema: json.RawMessage(`{"type":"object","properties":{` + properties + `},"required":[` + required +
			`],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{` + outputProperties +
			`,"updated":{"type":"boolean"}},"required":[` + required + `,"updated"],"additionalProperties":false}`),
		Arguments: args,
		Fields:    append(fields, capability.Field{Name: "updated", Description: "True once Matomo accepted the change"}),
		Examples:  []capability.Example{{Description: "Set the default", Arguments: json.RawMessage(example)}},
	}
}

func parameterList(name string) string {
	return `"` + name + `":{"type":"array","maxItems":500,"items":{"type":"string","minLength":1,"maxLength":256}}`
}

var globalSettingsSetSearchParameters = globalDefaultDescriptor(Provider+".global-settings.set-search-parameters",
	"Set the global site search parameters of Matomo",
	"Replace both lists of query parameter names that mark a URL as an internal site search for every website "+
		"(SitesManager.setGlobalSearchParameters); both lists are sent, an empty list clears it.",
	parameterList("searchKeywordParameters")+","+parameterList("searchCategoryParameters"),
	`"searchKeywordParameters","searchCategoryParameters"`,
	[]capability.Argument{
		{Name: "searchKeywordParameters", Required: true, Description: "The complete keyword list, 0 to 500 " +
			"parameter names of up to 256 characters without spaces, comma or control characters"},
		{Name: "searchCategoryParameters", Required: true, Description: "The complete category list, same form"},
	},
	[]capability.Field{
		{Name: "searchKeywordParameters", Description: "The keyword list that was sent"},
		{Name: "searchCategoryParameters", Description: "The category list that was sent"},
	},
	`"searchKeywordParameters":{"type":"array","items":{"type":"string"}},`+
		`"searchCategoryParameters":{"type":"array","items":{"type":"string"}}`,
	`{"searchKeywordParameters":["q","search"],"searchCategoryParameters":["cat"]}`)

var globalSettingsSetKeepURLFragments = globalDefaultDescriptor(Provider+".global-settings.set-keep-url-fragments",
	"Set whether Matomo keeps URL fragments globally",
	"Set whether tracked URLs of every website keep their #fragment by default (SitesManager.setKeepURLFragmentsGlobal).",
	`"enabled":{"type":"boolean"}`, `"enabled"`,
	[]capability.Argument{{Name: "enabled", Required: true, Description: "true keeps fragments, false strips them"}},
	[]capability.Field{{Name: "enabled", Description: "The value that was sent"}},
	`"enabled":{"type":"boolean"}`, `{"enabled":true}`)

var globalSettingsSetDefaultCurrency = globalDefaultDescriptor(Provider+".global-settings.set-default-currency",
	"Set the default currency of Matomo",
	"Set the currency that new websites and goals use by default (SitesManager.setDefaultCurrency); "+
		"matomo.reference-data.list with the kind currencies lists the codes, and Matomo checks the code again.",
	`"defaultCurrency":{"type":"string","pattern":"^[A-Z]{3}$"}`, `"defaultCurrency"`,
	[]capability.Argument{{Name: "defaultCurrency", Required: true,
		Description: "A currency code of three capital letters such as EUR"}},
	[]capability.Field{{Name: "defaultCurrency", Description: "The code that was sent"}},
	`"defaultCurrency":{"type":"string"}`, `{"defaultCurrency":"EUR"}`)

var globalSettingsSetDefaultTimezone = globalDefaultDescriptor(Provider+".global-settings.set-default-timezone",
	"Set the default time zone of Matomo",
	"Set the time zone that new websites use by default (SitesManager.setDefaultTimezone); "+
		"matomo.reference-data.list with the kind timezones lists the values, and Matomo checks the value again.",
	`"defaultTimezone":{"type":"string","minLength":1,"maxLength":`+strconv.Itoa(maxTimezoneLength)+`}`,
	`"defaultTimezone"`,
	[]capability.Argument{{Name: "defaultTimezone", Required: true, Description: "A time zone such as " +
		"Europe/Paris or UTC+8 of up to 64 characters without spaces or control characters"}},
	[]capability.Field{{Name: "defaultTimezone", Description: "The value that was sent"}},
	`"defaultTimezone":{"type":"string"}`, `{"defaultTimezone":"Europe/Paris"}`)

// globalDefaultKind is one default setter: build checks the arguments and returns the form values for Matomo
// and the answer, before any secret is read.
type globalDefaultKind struct {
	descriptor capability.Descriptor
	method     string
	op         string
	build      func(args map[string]json.RawMessage) (url.Values, map[string]any, error)
}

func onlyArguments(args map[string]json.RawMessage, names ...string) error {
	for key := range args {
		known := false
		for _, name := range names {
			known = known || key == name
		}
		if !known {
			return invalidArgument("only " + strings.Join(names, " and ") + " is accepted")
		}
	}
	for _, name := range names {
		if args[name] == nil {
			return invalidArgument(name + " is required")
		}
	}
	return nil
}

func buildSearchParameters(args map[string]json.RawMessage) (url.Values, map[string]any, error) {
	names := []string{"searchKeywordParameters", "searchCategoryParameters"}
	if err := onlyArguments(args, names...); err != nil {
		return nil, nil, err
	}
	form, answer := url.Values{}, map[string]any{"updated": true}
	for _, name := range names {
		var entries []string
		if json.Unmarshal(args[name], &entries) != nil || entries == nil {
			return nil, nil, invalidArgument(name + " must be a list of text")
		}
		list, err := excludedEntries(name, entries, plainText(false), true)
		if err != nil {
			return nil, nil, err
		}
		form.Set(name, strings.Join(list, ","))
		answer[name] = list
	}
	return form, answer, nil
}

func buildKeepURLFragments(args map[string]json.RawMessage) (url.Values, map[string]any, error) {
	if err := onlyArguments(args, "enabled"); err != nil {
		return nil, nil, err
	}
	var enabled bool
	if json.Unmarshal(args["enabled"], &enabled) != nil {
		return nil, nil, invalidArgument("enabled must be true or false")
	}
	// Matomo stores (string)(int)$enabled, and PHP reads the text "true" as 0.
	flag := "0"
	if enabled {
		flag = "1"
	}
	return url.Values{"enabled": {flag}}, map[string]any{"enabled": enabled, "updated": true}, nil
}

func buildDefaultCurrency(args map[string]json.RawMessage) (url.Values, map[string]any, error) {
	if err := onlyArguments(args, "defaultCurrency"); err != nil {
		return nil, nil, err
	}
	var code string
	if json.Unmarshal(args["defaultCurrency"], &code) != nil || !currencyCode.MatchString(code) {
		return nil, nil, invalidArgument("defaultCurrency must be a code of three capital letters")
	}
	return url.Values{"defaultCurrency": {code}}, map[string]any{"defaultCurrency": code, "updated": true}, nil
}

func buildDefaultTimezone(args map[string]json.RawMessage) (url.Values, map[string]any, error) {
	if err := onlyArguments(args, "defaultTimezone"); err != nil {
		return nil, nil, err
	}
	var zone string
	if json.Unmarshal(args["defaultTimezone"], &zone) != nil || zone == "" || len(zone) > maxTimezoneLength ||
		strings.IndexFunc(zone, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 {
		return nil, nil, invalidArgument("defaultTimezone must be text of 1 to 64 characters without spaces or " +
			"control characters")
	}
	return url.Values{"defaultTimezone": {zone}}, map[string]any{"defaultTimezone": zone, "updated": true}, nil
}

func (k globalDefaultKind) invoke(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var args map[string]json.RawMessage
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := needsInstance(resolved); err != nil {
		return nil, err
	}
	form, answer, err := k.build(args)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var reply json.RawMessage
	if err := client.change(ctx, k.op, siteRequirement(k.descriptor.ID), k.method, form, globalSentOnce,
		&reply); err != nil {
		return nil, err
	}
	if !confirmedByMatomo(reply) {
		return nil, unclearChange(k.op, "Matomo returned no confirmation of the change", globalSentOnce)
	}
	return GlobalListSet(answer), nil
}

var globalDefaultKinds = []globalDefaultKind{
	{globalSettingsSetSearchParameters, "SitesManager.setGlobalSearchParameters", "set search parameters",
		buildSearchParameters},
	{globalSettingsSetKeepURLFragments, "SitesManager.setKeepURLFragmentsGlobal", "set keep URL fragments",
		buildKeepURLFragments},
	{globalSettingsSetDefaultCurrency, "SitesManager.setDefaultCurrency", "set default currency",
		buildDefaultCurrency},
	{globalSettingsSetDefaultTimezone, "SitesManager.setDefaultTimezone", "set default time zone",
		buildDefaultTimezone},
}

func globalDefaultOperations() []capability.Operation {
	ops := make([]capability.Operation, 0, len(globalDefaultKinds))
	for _, kind := range globalDefaultKinds {
		ops = append(ops, capability.Operation{Descriptor: kind.descriptor, Handler: capability.Handler(kind.invoke)})
	}
	return ops
}
