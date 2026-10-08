package makeapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// make.organization.update changes the master data of the organization an organization-mode connection is
// bound to; the organization is always the connection's target. API (checked 2026-10-06 against
// developers.make.com's published API reference, section Organizations, not a live account):
// PATCH /organizations/{organizationId}, organizations:write, body name (letters, numbers, spaces and
// ' - . ( ) * + , @ _ /), countryId, timezoneId. The answer is assumed to be {"organization":{"id":...}}.
// Narrower reading: only these three fields; no plan, license, payment, or language fields (the reference
// documents no language field). The member list is in organizationmembers.go.

const (
	maxOrganizationNameLength = 128
	needOrganizationWrite     = "the organizations:write scope"
	organizationUncertain     = "; this change may have taken effect, read the organization before repeating it"
)

var organizationUpdate = capability.Descriptor{
	ID: Provider + ".organization.update", Version: 1, Title: "Update the bound Make organization",
	Description: "Change the name, country, or time zone of the organization this connection is bound to; at " +
		"least one of name, country_id, timezone_id. The organization is always the connection's own. Sends one " +
		"request and never repeats it. Only on an organization connection. Needs the organizations:write scope",
	Tags: []string{"make", "organization", "update", "automation"},
	Risk: makeChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minLength":1,` +
		`"maxLength":128},"country_id":{"type":"integer","minimum":1},"timezone_id":{"type":"integer",` +
		`"minimum":1}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"organization_id":{"type":"integer"},` +
		`"updated":{"type":"boolean"}},"required":["organization_id","updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "name", Description: "Organization name, 1 to " + strconv.Itoa(maxOrganizationNameLength) +
			" characters: letters, numbers, spaces, and ' - . ( ) * + , @ _ /"},
		{Name: "country_id", Description: "Make's country identifier"},
		{Name: "timezone_id", Description: "Make's time zone identifier"}},
	Fields: []capability.Field{
		{Name: "organization_id", Description: "The bound organization"},
		{Name: "updated", Description: "True when Make confirmed the bound organization"}},
	Examples: []capability.Example{{Description: "Rename the organization", Arguments: json.RawMessage(`{"name":"Acme"}`)}},
}

// OrganizationUpdate is the answer of organization.update.
type OrganizationUpdate struct {
	OrganizationID int64 `json:"organization_id"`
	Updated        bool  `json:"updated"`
}

func validOrganizationName(name string) bool {
	n := utf8.RuneCountInString(name)
	if n < 1 || n > maxOrganizationNameLength || !utf8.ValidString(name) {
		return false
	}
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(" '-.()*+,@_/", r) {
			return false
		}
	}
	return true
}

func invokeOrganizationUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update organization"
	var input struct {
		Name       *string `json:"name"`
		CountryID  *int64  `json:"country_id"`
		TimezoneID *int64  `json:"timezone_id"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if input.Name == nil && input.CountryID == nil && input.TimezoneID == nil {
		return nil, invalidRequest("name, country_id, or timezone_id is required")
	}
	body := map[string]any{}
	if input.Name != nil {
		if !validOrganizationName(*input.Name) {
			return nil, invalidRequest("name must be 1 to " + strconv.Itoa(maxOrganizationNameLength) +
				" characters: letters, numbers, spaces, and ' - . ( ) * + , @ _ /")
		}
		body["name"] = *input.Name
	}
	if input.CountryID != nil {
		if *input.CountryID <= 0 {
			return nil, invalidRequest("country_id must be a positive integer")
		}
		body["countryId"] = *input.CountryID
	}
	if input.TimezoneID != nil {
		if *input.TimezoneID <= 0 {
			return nil, invalidRequest("timezone_id must be a positive integer")
		}
		body["timezoneId"] = *input.TimezoneID
	}
	client, err := openOrganization(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer struct {
		Organization struct {
			ID int64 `json:"id"`
		} `json:"organization"`
	}
	path := "/organizations/" + strconv.FormatInt(client.scope.orgID, 10)
	if err := client.change(ctx, op, http.MethodPatch, path, nil, body, &answer, needOrganizationWrite,
		organizationUncertain); err != nil {
		return nil, err
	}
	if answer.Organization.ID != client.scope.orgID {
		return nil, invalidResponse(op, "Make did not report the bound organization"+organizationUncertain)
	}
	return &OrganizationUpdate{OrganizationID: client.scope.orgID, Updated: true}, nil
}
