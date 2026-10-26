package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

var privacySentOnce = sentOnceFor("setting", "matomo.privacy-settings.get")

// referrerAnonymizations are the options of anonymizeReferrer; the empty text keeps the whole referrer.
var referrerAnonymizations = []string{"", "exclude_query", "exclude_path", "exclude_all"}

const privacySetNote = " It needs Super User rights and a connection whose targets name the instance; it changes the " +
	"global settings of every website of the instance, including those outside the connection's website targets"

var privacySettingsSetAnonymizeIP = capability.Descriptor{
	ID: Provider + ".privacy-settings.set-anonymize-ip", Version: 1, Title: "Set the global IP anonymization of Matomo",
	Description: "Replace the global anonymization settings (PrivacyManager.setAnonymizeIpSettings): IP anonymization " +
		"and the number of masked bytes, the use of the anonymized IP for location lookups, and the pseudonymization " +
		"of user IDs, order IDs, and referrers. It replaces the global settings completely, so every argument is " +
		"required; read the current values first with matomo.privacy-settings.get. " +
		"Compliance policies of the instance can make Matomo keep a value while it still answers success, so read " +
		"the settings afterwards. Only the global settings are changed, never those of one website." + privacySetNote +
		". Only randomizeConfigId true makes Matomo ask for a password confirmation: then the credential needs the " +
		"secret role " + rolePasswordConfirmation + ", which no argument carries, and the call is refused before any " +
		"request when it is not set; in every other case no password is sent; idempotent" + privacySentOnce,
	Tags: []string{"matomo", "gdpr", "privacy", "anonymization", "settings", "update", "instance"},
	Risk: siteChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"anonymizeIPEnable":{"type":"boolean"},` +
		`"ipAddressMaskLength":{"type":"integer","minimum":1,"maximum":4},` +
		`"useAnonymizedIpForVisitEnrichment":{"type":"boolean"},"anonymizeUserId":{"type":"boolean"},` +
		`"anonymizeOrderId":{"type":"boolean"},"anonymizeReferrer":{"type":"string","enum":[` +
		quotedList(referrerAnonymizations) + `]},"forceCookielessTracking":{"type":"boolean"},` +
		`"randomizeConfigId":{"type":"boolean"}},` +
		`"required":["anonymizeIPEnable","ipAddressMaskLength","useAnonymizedIpForVisitEnrichment",` +
		`"anonymizeUserId","anonymizeOrderId","anonymizeReferrer","forceCookielessTracking","randomizeConfigId"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"anonymizeIPEnable":{"type":"boolean"},` +
		`"ipAddressMaskLength":{"type":"integer"},"useAnonymizedIpForVisitEnrichment":{"type":"boolean"},` +
		`"anonymizeUserId":{"type":"boolean"},"anonymizeOrderId":{"type":"boolean"},"anonymizeReferrer":{"type":"string"},` +
		`"forceCookielessTracking":{"type":"boolean"},"randomizeConfigId":{"type":"boolean"},` +
		`"updated":{"type":"boolean"}},"required":["anonymizeIPEnable","ipAddressMaskLength",` +
		`"useAnonymizedIpForVisitEnrichment","anonymizeUserId","anonymizeOrderId","anonymizeReferrer",` +
		`"forceCookielessTracking","randomizeConfigId","updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "anonymizeIPEnable", Required: true, Description: "True to anonymize visitor IP addresses"},
		{Name: "ipAddressMaskLength", Required: true, Description: "Number of IP address bytes to mask, 1 to 4; " +
			"the compliance policies may allow fewer values"},
		{Name: "useAnonymizedIpForVisitEnrichment", Required: true, Description: "True to use the anonymized IP " +
			"address for location and provider lookups"},
		{Name: "anonymizeUserId", Required: true, Description: "True to pseudonymize user IDs"},
		{Name: "anonymizeOrderId", Required: true, Description: "True to anonymize ecommerce order IDs"},
		{Name: "anonymizeReferrer", Required: true, Description: "How much of a referrer is dropped: empty for nothing, exclude_query, " +
			"exclude_path, or exclude_all; empty means none"},
		{Name: "forceCookielessTracking", Required: true, Description: "True to force tracking without cookies"},
		{Name: "randomizeConfigId", Required: true, Description: "True to make a new random device fingerprint per request. True needs the secret role " + rolePasswordConfirmation},
	},
	Fields: []capability.Field{
		{Name: "anonymizeIPEnable", Description: "The value that was sent"},
		{Name: "ipAddressMaskLength", Description: "The value that was sent"},
		{Name: "useAnonymizedIpForVisitEnrichment", Description: "The value that was sent"},
		{Name: "anonymizeUserId", Description: "The value that was sent"},
		{Name: "anonymizeOrderId", Description: "The value that was sent"},
		{Name: "anonymizeReferrer", Description: "The value that was sent"},
		{Name: "forceCookielessTracking", Description: "The value that was sent"},
		{Name: "randomizeConfigId", Description: "The value that was sent"},
		{Name: "updated", Description: "True once Matomo accepted the change"},
	},
	Examples: []capability.Example{{Description: "Mask two bytes of every IP address",
		Arguments: json.RawMessage(`{"anonymizeIPEnable":true,"ipAddressMaskLength":2,"useAnonymizedIpForVisitEnrichment":false,` +
			`"anonymizeUserId":false,"anonymizeOrderId":false,"anonymizeReferrer":"","forceCookielessTracking":false,` +
			`"randomizeConfigId":false}`)}},
}

// PrivacySettingsSet is the answer of privacy-settings.set-anonymize-ip.
type PrivacySettingsSet struct {
	AnonymizeIPEnable                 bool   `json:"anonymizeIPEnable"`
	IPAddressMaskLength               int    `json:"ipAddressMaskLength"`
	UseAnonymizedIPForVisitEnrichment bool   `json:"useAnonymizedIpForVisitEnrichment"`
	AnonymizeUserID                   bool   `json:"anonymizeUserId"`
	AnonymizeOrderID                  bool   `json:"anonymizeOrderId"`
	AnonymizeReferrer                 string `json:"anonymizeReferrer"`
	ForceCookielessTracking           bool   `json:"forceCookielessTracking"`
	RandomizeConfigID                 bool   `json:"randomizeConfigId"`
	Updated                           bool   `json:"updated"`
}

func invokePrivacySettingsSetAnonymizeIP(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "set IP anonymization"
	var args struct {
		Enable      *bool   `json:"anonymizeIPEnable"`
		MaskLength  *int    `json:"ipAddressMaskLength"`
		Enrichment  *bool   `json:"useAnonymizedIpForVisitEnrichment"`
		UserID      *bool   `json:"anonymizeUserId"`
		OrderID     *bool   `json:"anonymizeOrderId"`
		Referrer    *string `json:"anonymizeReferrer"`
		Cookieless  *bool   `json:"forceCookielessTracking"`
		RandomizeID *bool   `json:"randomizeConfigId"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := needsInstance(resolved); err != nil {
		return nil, err
	}
	if args.Enable == nil || args.Enrichment == nil {
		return nil, invalidArgument("anonymizeIPEnable and useAnonymizedIpForVisitEnrichment are required booleans")
	}
	if args.MaskLength == nil || *args.MaskLength < 1 || *args.MaskLength > 4 {
		return nil, invalidArgument("ipAddressMaskLength must be an integer from 1 to 4")
	}
	if args.UserID == nil || args.OrderID == nil || args.Cookieless == nil || args.RandomizeID == nil || args.Referrer == nil {
		return nil, invalidArgument("anonymizeUserId, anonymizeOrderId, anonymizeReferrer, forceCookielessTracking, " +
			"and randomizeConfigId are required")
	}
	referrer := *args.Referrer
	if !slices.Contains(referrerAnonymizations, referrer) {
		return nil, invalidArgument("anonymizeReferrer must be empty, exclude_query, exclude_path, or exclude_all")
	}
	set := &PrivacySettingsSet{AnonymizeIPEnable: *args.Enable, IPAddressMaskLength: *args.MaskLength,
		UseAnonymizedIPForVisitEnrichment: *args.Enrichment, AnonymizeReferrer: referrer, Updated: true,
		AnonymizeUserID: *args.UserID, AnonymizeOrderID: *args.OrderID,
		ForceCookielessTracking: *args.Cookieless,
		RandomizeConfigID:       *args.RandomizeID}
	params := url.Values{"anonymizeIPEnable": {flagValue(args.Enable)},
		"ipAddressMaskLength":               {strconv.Itoa(*args.MaskLength)},
		"useAnonymizedIpForVisitEnrichment": {flagValue(args.Enrichment)},
		"anonymizeUserId":                   {flagValue(args.UserID)}, "anonymizeOrderId": {flagValue(args.OrderID)},
		"anonymizeReferrer": {referrer}, "forceCookielessTracking": {flagValue(args.Cookieless)},
		"randomizeConfigId": {flagValue(args.RandomizeID)}}
	// The token is opened first, then the password role; a role that is not set ends the call before any request.
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if set.RandomizeConfigID {
		if err := addPasswordConfirmation(ctx, resolved, secrets, red, params); err != nil {
			return nil, err
		}
	}
	var answer json.RawMessage
	req := requirement{tool: privacySettingsSetAnonymizeIP.ID, plugin: "PrivacyManager"}
	if err := client.change(ctx, op, req, "PrivacyManager.setAnonymizeIpSettings", params, privacySentOnce, &answer); err != nil {
		return nil, err
	}
	if !confirmedByMatomo(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the change", privacySentOnce)
	}
	return set, nil
}

func doNotTrackDescriptor(id, title, verb, method string) capability.Descriptor {
	return capability.Descriptor{
		ID: id, Version: 1, Title: title,
		Description: verb + " support for the Do Not Track browser header, which makes Matomo skip the tracking of " +
			"visitors who send it (PrivacyManager." + method + "). It takes no argument and applies to every website of " +
			"the instance. Read the state first with matomo.privacy-settings.get." + privacySetNote +
			"; no password is sent; idempotent" + privacySentOnce,
		Tags: []string{"matomo", "gdpr", "privacy", "do-not-track", "update", "instance"},
		Risk: siteChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"doNotTrack":{"type":"boolean"},` +
			`"updated":{"type":"boolean"}},"required":["doNotTrack","updated"],"additionalProperties":false}`),
		Fields: []capability.Field{
			{Name: "doNotTrack", Description: "The state that was set"},
			{Name: "updated", Description: "True once Matomo accepted the change"},
		},
		Examples: []capability.Example{{Description: verb + " Do Not Track support", Arguments: json.RawMessage(`{}`)}},
	}
}

var doNotTrackEnable = doNotTrackDescriptor(Provider+".do-not-track.enable", "Enable Do Not Track in Matomo",
	"Enable", "activateDoNotTrack")

var doNotTrackDisable = doNotTrackDescriptor(Provider+".do-not-track.disable", "Disable Do Not Track in Matomo",
	"Disable", "deactivateDoNotTrack")

// DoNotTrackSet is the answer of the do-not-track tools.
type DoNotTrackSet struct {
	DoNotTrack bool `json:"doNotTrack"`
	Updated    bool `json:"updated"`
}

func invokeDoNotTrack(descriptor capability.Descriptor, method, op string, enabled bool) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		var args struct{}
		if err := decodeArguments(raw, &args); err != nil {
			return nil, err
		}
		if err := needsInstance(resolved); err != nil {
			return nil, err
		}
		client, err := Open(ctx, resolved, secrets, red)
		if err != nil {
			return nil, err
		}
		var answer json.RawMessage
		req := requirement{tool: descriptor.ID, plugin: "PrivacyManager"}
		if err := client.change(ctx, op, req, method, url.Values{}, privacySentOnce, &answer); err != nil {
			return nil, err
		}
		if !confirmedByMatomo(answer) {
			return nil, unclearChange(op, "Matomo returned no confirmation of the change", privacySentOnce)
		}
		return &DoNotTrackSet{DoNotTrack: enabled, Updated: true}, nil
	}
}

func privacySetOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: privacySettingsSetAnonymizeIP, Handler: capability.Handler(invokePrivacySettingsSetAnonymizeIP)},
		{Descriptor: doNotTrackEnable, Handler: invokeDoNotTrack(doNotTrackEnable, "PrivacyManager.activateDoNotTrack",
			"enable Do Not Track", true)},
		{Descriptor: doNotTrackDisable, Handler: invokeDoNotTrack(doNotTrackDisable, "PrivacyManager.deactivateDoNotTrack",
			"disable Do Not Track", false)},
	}
}
