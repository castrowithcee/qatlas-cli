package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	smsSensitive = "matomo-sms-settings"
	// maxSMSNumbers bounds the phone numbers one read returns.
	maxSMSNumbers = 200
)

var smsSentOnce = sentOnceFor("setting", "matomo.sms-settings.get")

// smsPhonePattern is the narrow form accepted for removal: E.164 as Matomo stores it, no formatting characters.
var smsPhonePattern = regexp.MustCompile(`^\+[0-9]{5,30}$`)

const smsPhoneSchema = `"phoneNumber":{"type":"string","pattern":"^\\+[0-9]{5,30}$","maxLength":31}`

func smsRequirement(tool string) requirement {
	return requirement{tool: tool, plugin: "MobileMessaging"}
}

func smsChangeRisk(effect capability.Effect) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: smsSensitive}
}

// smsSections maps the section argument to the Matomo method that serves it.
var smsSections = map[string]string{"credential-provided": "MobileMessaging.areSMSAPICredentialProvided",
	"provider": "MobileMessaging.getSMSProvider", "credit-left": "MobileMessaging.getCreditLeft",
	"phone-numbers": "MobileMessaging.getPhoneNumbers", "delegated-management": "MobileMessaging.getDelegatedManagement"}

const smsScopeNote = "The SMS settings belong to the account of the token, or to the instance while management is " +
	"not delegated, not to a website, so it needs a connection whose targets name the instance"

var smsSettingsGet = capability.Descriptor{
	ID: Provider + ".sms-settings.get", Version: 1, Title: "Get Matomo SMS settings",
	Description: "Read one part of the MobileMessaging settings with one request: whether SMS provider credentials are " +
		"stored, the configured SMS provider, the remaining credit, the phone numbers of the token's user, or whether " +
		"regular users may manage the credentials. Credentials are never returned. The credit is asked from the SMS " +
		"provider by the Matomo server. Reading the provider and the credit needs the right to manage credentials. " +
		smsScopeNote + "; the phone numbers are personal data and the tool is in no profile",
	Tags: []string{"matomo", "sms", "mobile-messaging", "settings", "instance"}, Risk: readRisk(smsSensitive),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"section":{"type":"string","enum":["credential-provided",` +
		`"provider","credit-left","phone-numbers","delegated-management"]}},"required":["section"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"section":{"type":"string"},"credentialProvided":{"type":"boolean"},` +
		`"provider":{"type":"string"},"creditLeft":{"type":"string"},"phoneNumbers":{"type":"array","items":{"type":"object",` +
		`"properties":{"phoneNumber":{"type":"string"},"verified":{"type":"boolean"}},"required":["phoneNumber","verified"],` +
		`"additionalProperties":false}},"delegatedManagement":{"type":"boolean"}},"required":["section"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "section", Required: true, Description: "credential-provided, provider, " +
		"credit-left, phone-numbers, or delegated-management"}},
	Fields: []capability.Field{
		{Name: "section", Description: "The section that was read"},
		{Name: "credentialProvided", Description: "Whether SMS provider credentials are stored; only for credential-provided"},
		{Name: "provider", Description: "Identifier of the SMS provider, untrusted; empty when none is set; only for provider"},
		{Name: "creditLeft", Description: "Remaining credit as the SMS provider reports it, untrusted; only for credit-left"},
		{Name: "phoneNumbers", Description: "Phone numbers of the token's user, at most 200; only for phone-numbers"},
		{Name: "phoneNumbers[].phoneNumber", Description: "The number, untrusted personal data; used by matomo.sms-phone-numbers.remove"},
		{Name: "phoneNumbers[].verified", Description: "Whether the number completed verification"},
		{Name: "delegatedManagement", Description: "Whether regular users manage the credentials; only for delegated-management"},
	},
	Examples: []capability.Example{{Description: "Read the stored phone numbers", Arguments: json.RawMessage(`{"section":"phone-numbers"}`)}},
}

var smsPhoneNumbersRemove = capability.Descriptor{
	ID: Provider + ".sms-phone-numbers.remove", Version: 1, Title: "Remove a Matomo SMS phone number",
	Description: "Remove one phone number from the token's user (MobileMessaging.removePhoneNumber) and from the scheduled " +
		"reports that name it. It cannot be undone here. The number must be given as + and 5 to 30 digits without " +
		"spaces or other characters. A number that is not stored changes nothing and is not an error. " + smsScopeNote +
		"; idempotent in effect" + smsSentOnce,
	Tags: []string{"matomo", "sms", "mobile-messaging", "delete", "instance"}, Risk: smsChangeRisk(capability.EffectDelete),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + smsPhoneSchema + `},"required":["phoneNumber"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"removed":{"type":"boolean"}},"required":["removed"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "phoneNumber", Required: true,
		Description: "The number as read by matomo.sms-settings.get: + and 5 to 30 digits"}},
	Fields:   []capability.Field{{Name: "removed", Description: "True once Matomo accepted the removal"}},
	Examples: []capability.Example{{Description: "Remove a number", Arguments: json.RawMessage(`{"phoneNumber":"+` + `99900011122"}`)}},
}

var smsCredentialsDelete = capability.Descriptor{
	ID: Provider + ".sms-credentials.delete", Version: 1, Title: "Delete Matomo SMS provider credentials",
	Description: "Delete the stored SMS provider credentials (MobileMessaging.deleteSMSAPICredential); SMS reports cannot " +
		"be sent until credentials are stored again, which Qatlas cannot do. It needs the right to manage credentials. " +
		smsScopeNote + "; idempotent in effect; offered only where a connection's tools list names it" + smsSentOnce,
	Tags: []string{"matomo", "sms", "mobile-messaging", "delete", "instance"}, Risk: smsChangeRisk(capability.EffectDelete),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},"required":["deleted"],` +
		`"additionalProperties":false}`),
	Fields:   []capability.Field{{Name: "deleted", Description: "True once Matomo accepted the deletion"}},
	Examples: []capability.Example{{Description: "Delete the credentials", Arguments: json.RawMessage(`{}`)}},
}

var smsDelegationSet = capability.Descriptor{
	ID: Provider + ".sms-delegation.set", Version: 1, Title: "Set Matomo SMS credential delegation",
	Description: "Set whether regular users may manage the SMS provider credentials or only Super Users " +
		"(MobileMessaging.setDelegatedManagement). It needs Super User rights. " + smsScopeNote + "; idempotent" + smsSentOnce,
	Tags: []string{"matomo", "sms", "mobile-messaging", "update", "instance"}, Risk: smsChangeRisk(capability.EffectUpdate),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"delegatedManagement":{"type":"boolean"}},` +
		`"required":["delegatedManagement"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"delegatedManagement":{"type":"boolean"},"updated":{"type":"boolean"}},` +
		`"required":["delegatedManagement","updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "delegatedManagement", Required: true,
		Description: "true to let regular users manage the credentials, false to restrict it to Super Users"}},
	Fields: []capability.Field{
		{Name: "delegatedManagement", Description: "The value that was sent"},
		{Name: "updated", Description: "True once Matomo accepted the change"},
	},
	Examples: []capability.Example{{Description: "Restrict management to Super Users", Arguments: json.RawMessage(`{"delegatedManagement":false}`)}},
}

// SMSPhoneNumber is one stored number with its verification state.
type SMSPhoneNumber struct {
	PhoneNumber string `json:"phoneNumber"`
	Verified    bool   `json:"verified"`
}

// SMSSettings is the answer of sms-settings.get; only the member of the section read is set.
type SMSSettings struct {
	Section             string           `json:"section"`
	CredentialProvided  *bool            `json:"credentialProvided,omitempty"`
	Provider            *string          `json:"provider,omitempty"`
	CreditLeft          *string          `json:"creditLeft,omitempty"`
	PhoneNumbers        []SMSPhoneNumber `json:"phoneNumbers,omitempty"`
	DelegatedManagement *bool            `json:"delegatedManagement,omitempty"`
}

// SMSPhoneNumberRemoved is the answer of sms-phone-numbers.remove.
type SMSPhoneNumberRemoved struct {
	Removed bool `json:"removed"`
}

// SMSCredentialsDeleted is the answer of sms-credentials.delete.
type SMSCredentialsDeleted struct {
	Deleted bool `json:"deleted"`
}

// SMSDelegationSet is the answer of sms-delegation.set.
type SMSDelegationSet struct {
	DelegatedManagement bool `json:"delegatedManagement"`
	Updated             bool `json:"updated"`
}

func invokeSMSSettingsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get SMS settings"
	var args struct {
		Section *string `json:"section"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := needsInstance(resolved); err != nil {
		return nil, err
	}
	if args.Section == nil || smsSections[*args.Section] == "" {
		return nil, invalidArgument("section must be credential-provided, provider, credit-left, phone-numbers, or delegated-management")
	}
	section := *args.Section
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.call(ctx, op, smsRequirement(smsSettingsGet.ID), smsSections[section], url.Values{}, &answer); err != nil {
		return nil, err
	}
	out := &SMSSettings{Section: section}
	switch section {
	case "credential-provided", "delegated-management":
		var value loose
		if scalar(answer, &value) != nil || value.flag() == nil || value.text == "" {
			return nil, provider.InvalidResponse(op, "Matomo returned an invalid flag")
		}
		flag := value.flag()
		if section == "credential-provided" {
			out.CredentialProvided = flag
		} else {
			out.DelegatedManagement = flag
		}
	case "provider", "credit-left":
		var value loose
		if scalar(answer, &value) != nil {
			return nil, provider.InvalidResponse(op, "Matomo returned an invalid value")
		}
		text := value.str(maxStringLength)
		if section == "provider" {
			out.Provider = &text
		} else {
			out.CreditLeft = &text
		}
	default:
		numbers, err := smsNumbers(answer)
		if err != nil {
			return nil, provider.InvalidResponse(op, "Matomo returned invalid phone numbers")
		}
		out.PhoneNumbers = numbers
	}
	return out, nil
}

// smsNumbers reads the numbers keyed by phone number; Matomo answers an empty list as [].
func smsNumbers(answer json.RawMessage) ([]SMSPhoneNumber, error) {
	var empty []json.RawMessage
	if json.Unmarshal(answer, &empty) == nil && len(empty) == 0 {
		return []SMSPhoneNumber{}, nil
	}
	var rows map[string]struct {
		Verified *loose `json:"verified"`
	}
	if err := json.Unmarshal(answer, &rows); err != nil {
		return nil, err
	}
	numbers := make([]SMSPhoneNumber, 0, len(rows))
	for number, row := range rows {
		verified := row.Verified.flag()
		numbers = append(numbers, SMSPhoneNumber{PhoneNumber: boundString(strings.TrimSpace(number), 64),
			Verified: verified != nil && *verified})
	}
	slices.SortFunc(numbers, func(a, b SMSPhoneNumber) int { return strings.Compare(a.PhoneNumber, b.PhoneNumber) })
	if len(numbers) > maxSMSNumbers {
		numbers = numbers[:maxSMSNumbers]
	}
	return numbers, nil
}

func smsChange(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	op, tool, method string, params url.Values) error {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, smsRequirement(tool), method, params, smsSentOnce, &answer); err != nil {
		return err
	}
	if !confirmedByMatomo(answer) {
		return unclearChange(op, "Matomo returned no confirmation of the change", smsSentOnce)
	}
	return nil
}

func invokeSMSPhoneNumbersRemove(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var args struct {
		PhoneNumber *string `json:"phoneNumber"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := needsInstance(resolved); err != nil {
		return nil, err
	}
	if args.PhoneNumber == nil || !smsPhonePattern.MatchString(*args.PhoneNumber) {
		return nil, invalidArgument("phoneNumber must be + and 5 to 30 digits")
	}
	err := smsChange(ctx, resolved, secrets, red, "remove SMS phone number", smsPhoneNumbersRemove.ID,
		"MobileMessaging.removePhoneNumber", url.Values{"phoneNumber": {*args.PhoneNumber}})
	if err != nil {
		return nil, err
	}
	return &SMSPhoneNumberRemoved{Removed: true}, nil
}

func invokeSMSCredentialsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var args struct{}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := needsInstance(resolved); err != nil {
		return nil, err
	}
	err := smsChange(ctx, resolved, secrets, red, "delete SMS credentials", smsCredentialsDelete.ID,
		"MobileMessaging.deleteSMSAPICredential", url.Values{})
	if err != nil {
		return nil, err
	}
	return &SMSCredentialsDeleted{Deleted: true}, nil
}

func invokeSMSDelegationSet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var args struct {
		DelegatedManagement *bool `json:"delegatedManagement"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := needsInstance(resolved); err != nil {
		return nil, err
	}
	if args.DelegatedManagement == nil {
		return nil, invalidArgument("delegatedManagement is required")
	}
	err := smsChange(ctx, resolved, secrets, red, "set SMS delegation", smsDelegationSet.ID,
		"MobileMessaging.setDelegatedManagement", url.Values{"delegatedManagement": {flagValue(args.DelegatedManagement)}})
	if err != nil {
		return nil, err
	}
	return &SMSDelegationSet{DelegatedManagement: *args.DelegatedManagement, Updated: true}, nil
}

func smsOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: smsSettingsGet, Handler: capability.Handler(invokeSMSSettingsGet)},
		{Descriptor: smsPhoneNumbersRemove, Handler: capability.Handler(invokeSMSPhoneNumbersRemove)},
		{Descriptor: smsCredentialsDelete, Handler: capability.Handler(invokeSMSCredentialsDelete)},
		{Descriptor: smsDelegationSet, Handler: capability.Handler(invokeSMSDelegationSet)},
	}
}
