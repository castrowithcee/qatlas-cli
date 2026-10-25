package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxPolicySettingValues = 50
	policySettingPattern   = `^[A-Za-z0-9_]{1,64}[.][A-Za-z0-9_]{1,64}$`
)

var (
	// firstGranularCompliance is the first release with the granular compliance methods.
	firstGranularCompliance = version{5, 14, 0}
	// firstComplianceStatusSetter is the first release with PrivacyManager.setComplianceStatus.
	firstComplianceStatusSetter = version{5, 9, 0}

	policySettingID = regexp.MustCompile(policySettingPattern)

	complianceSentOnce = sentOnceFor("policy", "matomo.compliance.get")
)

// complianceChangeRisk is the risk of the compliance setters; each takes the instance target because a change
// for one website can rewrite the state of the instance and of every other website.
func complianceChangeRisk(effect capability.Effect) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: instanceSensitive}
}

const complianceSetNote = " It needs Super User rights and a connection bound to the instance target, even for one " +
	"website, because releasing a website under an enforcing instance changes the instance and the other websites; " +
	"a policy controlled by Matomo's configuration file is refused"

const complianceSiteSchema = `"policy":{"type":"string","pattern":"` + policyPattern + `","maxLength":64},` +
	`"idSite":{"type":"integer","minimum":1},"all_sites":{"type":"boolean"}`

var complianceSettingsOutput = json.RawMessage(`{"type":"object","properties":{"kind":{"type":"string"},` +
	`"settings":{"type":"object"}},"required":["kind","settings"],"additionalProperties":false}`)

var complianceSettingsFields = []capability.Field{
	{Name: "kind", Description: "Always settings: the answer has the form of matomo.compliance.get with kind settings"},
	{Name: "settings", Description: "The granular settings of the policy after the change"},
	{Name: "settings.policy", Description: "Policy ID"},
	{Name: "settings.title", Description: "Title of the policy, untrusted"},
	{Name: "settings.description", Description: "Description of the policy, untrusted"},
	{Name: "settings.config_controlled", Description: "True when the policy is controlled by the configuration file"},
	{Name: "settings.policy_enforced", Description: "True when every toggleable setting is enforced"},
	{Name: "settings.settings", Description: "The settings of the policy, each with id, name, what_it_does, impact, " +
		"status, enforced (absent when the setting cannot be toggled), toggleable, and section, untrusted"},
}

var complianceTargetArguments = []capability.Argument{
	{Name: "policy", Required: true, Description: "Policy ID from matomo.compliance.get, such as cnil_v1"},
	{Name: "idSite", Description: "The website to change; must lie inside the connection's website targets; " +
		"exclusive with all_sites", Target: "site"},
	{Name: "all_sites", Description: "True changes the instance-wide state instead of a website"},
}

var complianceSetPolicySettings = capability.Descriptor{
	ID: Provider + ".compliance.set-policy-settings", Version: 1, Title: "Enforce or release settings of a Matomo compliance policy",
	Description: "Enforce or release single settings of a compliance policy for a website or the whole instance " +
		"(PrivacyManager.setCompliancePolicySettings). Only the settings named in settingValues change: true " +
		"enforces a setting, false releases it explicitly for that level. Matomo stores only this enforced state; " +
		"the underlying setting stays as it is and is overridden when read. true acts additively across the " +
		"instance and the website, false releases the policy explicitly for that level, and for a website under an enforcing instance it " +
		"rewrites the state of all websites and releases the instance. It needs Matomo " +
		"5.14.0 and works only where Matomo's granular compliance feature is enabled (feature flag " +
		"GranularPrivacyCompliance); read the settings first with matomo.compliance.get. Names and texts in the " +
		"answer are translated by Matomo and untrusted." + complianceSetNote +
		complianceSentOnce,
	Tags: []string{"matomo", "gdpr", "privacy", "compliance", "policy", "update", "instance"},
	Risk: complianceChangeRisk(capability.EffectUpdate), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + complianceSiteSchema + `,"settingValues":` +
		`{"type":"object","minProperties":1,"maxProperties":50,"propertyNames":{"pattern":"` + policySettingPattern +
		`"},"additionalProperties":{"type":"boolean"}}},"required":["policy","settingValues"],"additionalProperties":false}`),
	OutputSchema: complianceSettingsOutput,
	Arguments: append(slices.Clone(complianceTargetArguments), capability.Argument{Name: "settingValues", Required: true,
		Description: "Map of setting ID to true (enforce) or false (release), 1 to 50 entries; the IDs are the id " +
			"of the toggleable settings from matomo.compliance.get. An unknown or non-toggleable ID makes Matomo " +
			"refuse the whole call before it writes"}),
	Fields: complianceSettingsFields,
	Examples: []capability.Example{{Description: "Enforce one setting for the whole instance",
		Arguments: json.RawMessage(`{"policy":"cnil_v1","all_sites":true,"settingValues":{"PrivacyManager.IPAnonymisation":true}}`)}},
}

var complianceEnforce = capability.Descriptor{
	ID: Provider + ".compliance.enforce", Version: 1, Title: "Enforce a Matomo compliance policy completely",
	Description: "Enforce every toggleable setting of a compliance policy at once for a website or the whole " +
		"instance (PrivacyManager.enforceCompliancePolicySettings); for cnil_v1 that includes IP anonymization, " +
		"referrer shortening, user ID and visitor log off, and raw data kept at most 759 days. The data effect is " +
		"final: data anonymized in the meantime stays anonymized, and the scheduled purge then deletes raw data " +
		"older than 759 days when deleting is active and was set longer. Later releasing the settings with " +
		"matomo.compliance.set-policy-settings sets them to explicitly not enforced; the earlier inherited state " +
		"cannot be restored through the API. It needs Matomo 5.14.0 and Matomo's granular compliance feature " +
		"(feature flag GranularPrivacyCompliance); read the policy first with matomo.compliance.get. Names and texts in the answer are translated by " +
		"Matomo and untrusted." + complianceSetNote + "; offered only where a connection's tools list names it" + complianceSentOnce,
	Tags: []string{"matomo", "gdpr", "privacy", "compliance", "policy", "execute", "instance"},
	Risk: complianceChangeRisk(capability.EffectExecute), Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + complianceSiteSchema +
		`},"required":["policy"],"additionalProperties":false}`),
	OutputSchema: complianceSettingsOutput,
	Arguments:    complianceTargetArguments,
	Fields:       complianceSettingsFields,
	Examples: []capability.Example{{Description: "Enforce the policy for one website",
		Arguments: json.RawMessage(`{"policy":"cnil_v1","idSite":2}`)}},
}

var complianceSetStatus = capability.Descriptor{
	ID: Provider + ".compliance.set-status", Version: 1, Title: "Switch a Matomo compliance policy on or off",
	Description: "Switch the older whole-policy switch of a compliance policy on or off for a website or the whole " +
		"instance (PrivacyManager.setComplianceStatus; deprecated in Matomo 6). While it is on, every setting " +
		"without a state of its own counts as enforced; off for a website under an active instance also turns " +
		"the instance off. Mixing it with matomo.compliance.set-policy-settings gives states that are hard to " +
		"follow. It needs Matomo 5.9.0. The answer only echoes the value sent; read the result with " +
		"matomo.compliance.get." + complianceSetNote + complianceSentOnce,
	Tags: []string{"matomo", "gdpr", "privacy", "compliance", "policy", "update", "instance"},
	Risk: complianceChangeRisk(capability.EffectUpdate), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + complianceSiteSchema + `,"enforce":{"type":"boolean"}},` +
		`"required":["policy","enforce"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"policy":{"type":"string"},"enforce":{"type":"boolean"}},` +
		`"required":["policy","enforce"],"additionalProperties":false}`),
	Arguments: append(slices.Clone(complianceTargetArguments), capability.Argument{Name: "enforce", Required: true,
		Description: "True switches the policy on, false off"}),
	Fields: []capability.Field{
		{Name: "policy", Description: "The policy that was sent"},
		{Name: "enforce", Description: "The value Matomo echoed; it is no proof of the resulting state"},
	},
	Examples: []capability.Example{{Description: "Switch a policy on for the whole instance",
		Arguments: json.RawMessage(`{"policy":"cnil_v1","all_sites":true,"enforce":true}`)}},
}

// ComplianceStatusSet is the answer of compliance.set-status.
type ComplianceStatusSet struct {
	Policy  string `json:"policy"`
	Enforce bool   `json:"enforce"`
}

// complianceTarget checks policy and website of a compliance setter before any secret is read.
func complianceTarget(resolved *config.Resolved, policy *string, idSite *int, allSites bool) (string, string, error) {
	if err := needsInstance(resolved); err != nil {
		return "", "", err
	}
	if policy == nil || !validPolicy(*policy) {
		return "", "", invalidArgument("policy must hold 1 to 64 letters, digits, and the characters _ . -")
	}
	site, err := complianceSite(resolved, idSite, allSites)
	return *policy, site, err
}

func complianceRequirement(tool string, since version) requirement {
	return requirement{tool: tool, plugin: "PrivacyManager", minVersion: since}
}

func invokeComplianceSetPolicySettings(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "set compliance policy settings"
	var args struct {
		Policy        *string          `json:"policy"`
		IDSite        *int             `json:"idSite"`
		AllSites      bool             `json:"all_sites"`
		SettingValues map[string]*bool `json:"settingValues"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	policy, site, err := complianceTarget(resolved, args.Policy, args.IDSite, args.AllSites)
	if err != nil {
		return nil, err
	}
	if len(args.SettingValues) == 0 || len(args.SettingValues) > maxPolicySettingValues {
		return nil, invalidArgument("settingValues must hold 1 to 50 entries")
	}
	params := url.Values{"idSite": {site}, "compliancePolicy": {policy}}
	for id, enforced := range args.SettingValues {
		if enforced == nil || !policySettingID.MatchString(id) {
			return nil, invalidArgument("settingValues needs setting IDs of the form Plugin.Setting with a true or false value")
		}
		params.Set("settingValues["+id+"]", flagValue(enforced))
	}
	return changeComplianceSettings(ctx, resolved, secrets, red, op, "PrivacyManager.setCompliancePolicySettings",
		complianceRequirement(complianceSetPolicySettings.ID, firstGranularCompliance), params)
}

func invokeComplianceEnforce(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "enforce compliance policy"
	var args struct {
		Policy   *string `json:"policy"`
		IDSite   *int    `json:"idSite"`
		AllSites bool    `json:"all_sites"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	policy, site, err := complianceTarget(resolved, args.Policy, args.IDSite, args.AllSites)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {site}, "compliancePolicy": {policy}}
	return changeComplianceSettings(ctx, resolved, secrets, red, op, "PrivacyManager.enforceCompliancePolicySettings",
		complianceRequirement(complianceEnforce.ID, firstGranularCompliance), params)
}

// changeComplianceSettings sends the one request of a granular setter and reads the settings it answers with.
// No password confirmation is sent: Matomo asks for it only when the request is made with a session token.
func changeComplianceSettings(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, op, method string, req requirement, params url.Values) (any, error) {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, req, method, params, complianceSentOnce, &answer); err != nil {
		return nil, err
	}
	var settings row
	if !answerObject(answer, &settings) || len(settings) == 0 {
		return nil, unclearChange(op, "Matomo returned no readable settings after the change", complianceSentOnce)
	}
	return &ComplianceResult{Kind: "settings", Settings: complianceSettingsOf(settings)}, nil
}

func invokeComplianceSetStatus(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "set compliance status"
	var args struct {
		Policy   *string `json:"policy"`
		IDSite   *int    `json:"idSite"`
		AllSites bool    `json:"all_sites"`
		Enforce  *bool   `json:"enforce"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	policy, site, err := complianceTarget(resolved, args.Policy, args.IDSite, args.AllSites)
	if err != nil {
		return nil, err
	}
	if args.Enforce == nil {
		return nil, invalidArgument("enforce is required and must be true or false")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {site}, "complianceType": {policy}, "enforce": {strconv.Itoa(boolInt(*args.Enforce))}}
	var answer json.RawMessage
	if err := client.change(ctx, op, complianceRequirement(complianceSetStatus.ID, firstComplianceStatusSetter),
		"PrivacyManager.setComplianceStatus", params, complianceSentOnce, &answer); err != nil {
		return nil, err
	}
	var echoed bool
	if scalar(answer, &echoed) != nil || echoed != *args.Enforce {
		return nil, unclearChange(op, "Matomo did not echo the value that was sent", complianceSentOnce)
	}
	return &ComplianceStatusSet{Policy: policy, Enforce: echoed}, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
