package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

const (
	rollUpTimezone = `^[A-Za-z0-9_+/-]{1,64}$`
	rollUpCurrency = `^[A-Z]{3}$`
)

var (
	rollUpTimezonePattern = regexp.MustCompile(rollUpTimezone)
	rollUpCurrencyPattern = regexp.MustCompile(rollUpCurrency)
	rollUpSentOnce        = sentOnceFor("roll-up", "matomo.roll-ups.list")
)

const (
	rollUpNameSchema     = `"name":{"type":"string","minLength":1,"maxLength":255}`
	rollUpSourcesSchema  = `"sourceIdSites":` + customSiteListSchema
	rollUpTimezoneSchema = `"timezone":{"type":"string","pattern":"` + rollUpTimezone + `"}`
	rollUpCurrencySchema = `"currency":{"type":"string","pattern":"` + rollUpCurrency + `"}`
)

// rollUpChangeRisk is the risk of every roll-up change: confirmed, open to the instance, and about websites.
func rollUpChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: sitesSensitive}
}

const rollUpFieldHints = "name is 1 to 255 characters; sourceIdSites holds 1 to 20 different website IDs, each inside " +
	"the connection's targets; timezone is an identifier such as Europe/Berlin and currency a code such as EUR, and " +
	"Matomo checks both"

var rollUpsAdd = capability.Descriptor{
	ID: Provider + ".roll-ups.add", Version: 1, Title: "Create a Matomo roll-up website",
	Description: "Create a roll-up website that reports over source websites (addRollUp); a repeated call creates a " +
		"second roll-up. A roll-up belongs to the instance, not to a website, so the tool needs Super User rights " +
		"(expected) and a connection whose targets name the instance. " + rollUpFieldHints + ". Names are untrusted " +
		"text. Needs the premium plugin RollUpReporting" + rollUpSentOnce,
	Tags: []string{"matomo", "roll-ups", "superuser", "instance", "create"}, Provider: Provider,
	Risk: rollUpChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + rollUpNameSchema + `,` + rollUpSourcesSchema + `,` +
		rollUpTimezoneSchema + `,` + rollUpCurrencySchema + `},"required":["name","sourceIdSites","timezone","currency"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},"created":{"type":"boolean"}},` +
		`"required":["idSite","created"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "name", Required: true, Description: "Name of the roll-up, 1 to 255 characters"},
		{Name: "sourceIdSites", Required: true, Description: "1 to 20 different website IDs the roll-up reports over, " +
			"each inside the connection's targets"},
		{Name: "timezone", Required: true, Description: "Time zone such as Europe/Berlin; Matomo checks it"},
		{Name: "currency", Required: true, Description: "Currency code of three capital letters such as EUR; Matomo checks it"}},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website ID of the new roll-up; Matomo's answer is not verified against a reference"},
		{Name: "created", Description: "True once Matomo accepted the roll-up"}},
	Examples: []capability.Example{{Description: "Roll up websites 1 and 2",
		Arguments: json.RawMessage(`{"name":"All shops","sourceIdSites":[1,2],"timezone":"Europe/Berlin","currency":"EUR"}`)}},
}

var rollUpsUpdate = capability.Descriptor{
	ID: Provider + ".roll-ups.update", Version: 1, Title: "Change a Matomo roll-up website",
	Description: "Change a roll-up website (updateRollUp): only the fields that are given are sent, and at least one " +
		"is required. What Matomo does with a field that is left out is not documented; read the roll-up with " +
		"matomo.roll-ups.list first. sourceIdSites replaces the whole list of sources. The roll-up is read first " +
		"and must exist, otherwise it is not found. The roll-up's own website (idSite) must lie inside the " +
		"connection's website targets, and the tool needs Super User rights and a connection whose targets name " +
		"the instance. " + rollUpFieldHints + ". Names are untrusted text. Needs the premium plugin RollUpReporting" +
		rollUpSentOnce,
	Tags: []string{"matomo", "roll-ups", "superuser", "instance", "update"}, Provider: Provider,
	Risk: rollUpChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer","minimum":1},` + rollUpNameSchema +
		`,` + rollUpSourcesSchema + `,` + rollUpTimezoneSchema + `,` + rollUpCurrencySchema + `},"required":["idSite"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},"updated":{"type":"boolean"}},` +
		`"required":["idSite","updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "idSite", Required: true, Description: "Website ID of the roll-up, from matomo.roll-ups.list"},
		{Name: "name", Description: "New name, 1 to 255 characters; not sent when omitted"},
		{Name: "sourceIdSites", Description: "New list of 1 to 20 source websites, each inside the connection's " +
			"targets; not sent when omitted"},
		{Name: "timezone", Description: "New time zone; not sent when omitted"},
		{Name: "currency", Description: "New currency code; not sent when omitted"}},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The roll-up that was changed"},
		{Name: "updated", Description: "True once Matomo accepted the change"}},
	Examples: []capability.Example{{Description: "Rename roll-up 5", Arguments: json.RawMessage(`{"idSite":5,"name":"All shops"}`)}},
}

// RollUpAdded is the answer of roll-ups.add.
type RollUpAdded struct {
	IDSite  int  `json:"idSite"`
	Created bool `json:"created"`
}

// RollUpUpdated is the answer of roll-ups.update.
type RollUpUpdated struct {
	IDSite  int  `json:"idSite"`
	Updated bool `json:"updated"`
}

// rollUpParams validates the given fields and turns them into form fields. Every source must lie inside the
// targets; the refusal does not name the website.
func rollUpParams(resolved *config.Resolved, name, timezone, currency *string, sources []int) (url.Values, error) {
	params := url.Values{}
	if name != nil {
		if err := customMetricText("name", *name, maxRollUpName, true); err != nil {
			return nil, err
		}
		params.Set("name", *name)
	}
	if sources != nil {
		if err := customSites(resolved, "sourceIdSites", sources, maxRollUpSources); err != nil {
			return nil, err
		}
		for i, id := range sources {
			params.Set("sourceIdSites["+strconv.Itoa(i)+"]", strconv.Itoa(id))
		}
	}
	if timezone != nil {
		if !rollUpTimezonePattern.MatchString(*timezone) {
			return nil, invalidArgument("timezone must be an identifier such as Europe/Berlin")
		}
		params.Set("timezone", *timezone)
	}
	if currency != nil {
		if !rollUpCurrencyPattern.MatchString(*currency) {
			return nil, invalidArgument("currency must be a code of three capital letters")
		}
		params.Set("currency", *currency)
	}
	return params, nil
}

func invokeRollUpsAdd(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "add roll-up"
	var args struct {
		Name          *string `json:"name"`
		SourceIDSites []int   `json:"sourceIdSites"`
		Timezone      *string `json:"timezone"`
		Currency      *string `json:"currency"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if args.Name == nil || args.SourceIDSites == nil || args.Timezone == nil || args.Currency == nil {
		return nil, invalidArgument("name, sourceIdSites, timezone, and currency are required")
	}
	params, err := rollUpParams(resolved, args.Name, args.Timezone, args.Currency, args.SourceIDSites)
	if err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if err := requireInstance(bound); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, rollUpsRequirement(rollUpsAdd.ID), "RollUpReporting.addRollUp", params, rollUpSentOnce, &answer); err != nil {
		return nil, err
	}
	var id int
	if scalar(answer, &id) != nil || id < 1 {
		return nil, unclearChange(op, "Matomo returned no ID of the roll-up", rollUpSentOnce)
	}
	return &RollUpAdded{IDSite: id, Created: true}, nil
}

func invokeRollUpsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update roll-up"
	var args struct {
		IDSite        *int    `json:"idSite"`
		Name          *string `json:"name"`
		SourceIDSites []int   `json:"sourceIdSites"`
		Timezone      *string `json:"timezone"`
		Currency      *string `json:"currency"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if args.IDSite == nil {
		return nil, invalidArgument("idSite is required")
	}
	if args.Name == nil && args.SourceIDSites == nil && args.Timezone == nil && args.Currency == nil {
		return nil, invalidArgument("at least one of name, sourceIdSites, timezone, and currency is required")
	}
	params, err := rollUpParams(resolved, args.Name, args.Timezone, args.Currency, args.SourceIDSites)
	if err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if err := requireInstance(bound); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := rollUpsRequirement(rollUpsUpdate.ID)
	rows, err := readRollUps(ctx, client, op, req)
	if err != nil {
		return nil, err
	}
	known := false
	for _, row := range rows {
		known = known || row.IDSite == id
	}
	if !known {
		return nil, &provider.Error{Class: provider.ClassNotFound, Op: op, Message: "Matomo has no such roll-up"}
	}
	params.Set("idSite", strconv.Itoa(id))
	var answer json.RawMessage
	if err := client.change(ctx, op, req, "RollUpReporting.updateRollUp", params, rollUpSentOnce, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the roll-up change", rollUpSentOnce)
	}
	return &RollUpUpdated{IDSite: id, Updated: true}, nil
}

func rollUpChangeOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: rollUpsAdd, Handler: capability.Handler(invokeRollUpsAdd)},
		{Descriptor: rollUpsUpdate, Handler: capability.Handler(invokeRollUpsUpdate)},
	}
}
