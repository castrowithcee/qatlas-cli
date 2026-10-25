package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxPolicies         = 50
	maxPolicyItems      = 200
	policyPattern       = `^[A-Za-z0-9_.-]{1,64}$`
	maxPolicyNameLength = 64
)

var complianceKinds = []string{"policies", "status", "settings"}

var complianceGet = capability.Descriptor{
	ID: Provider + ".compliance.get", Version: 1, Title: "Get the Matomo compliance policies",
	Description: "Read the compliance policies of the instance and how a website or the whole instance stands against one. " +
		"kind policies (the default) lists the available policies (PrivacyManager.getCompliancePolicies); status " +
		"reads the requirements of a policy (getComplianceStatus, deprecated in Matomo 6); settings reads the " +
		"granular settings of a policy (getCompliancePolicySettings, only when Matomo's granular compliance feature " +
		"is enabled). status and settings need policy and either idSite or all_sites. It needs Super User rights " +
		"and a connection bound to the instance target; names and texts are translated by Matomo and untrusted",
	Tags: []string{"matomo", "gdpr", "privacy", "compliance", "policy", "get", "instance"}, Risk: readRisk(instanceSensitive),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"kind":{"type":"string","enum":[` +
		quotedList(complianceKinds) + `]},"policy":{"type":"string","pattern":"` + policyPattern + `","maxLength":64},` +
		siteKey + `,"all_sites":{"type":"boolean"}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"kind":{"type":"string"},"policies":{"type":"array",` +
		`"items":{"type":"object"}},"status":{"type":"object"},"settings":{"type":"object"}},` +
		`"required":["kind"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "kind", Description: "What to read: " + "policies, status, or settings; policies when omitted"},
		{Name: "policy", Description: "Policy ID from kind policies, such as cnil_v1, up to 64 characters; needed for status and settings"},
		{Name: "idSite", Description: "For status and settings: the website to inspect; must lie inside the connection's " +
			"website targets; exclusive with all_sites", Target: "site"},
		{Name: "all_sites", Description: "For status and settings: true inspects the instance-wide state instead of a website"},
	},
	Fields: []capability.Field{
		{Name: "kind", Description: "The kind read"},
		{Name: "policies", Description: "For policies: the available policies"},
		{Name: "policies[].id", Description: "Policy ID, passed as policy"},
		{Name: "policies[].title", Description: "Title of the policy, untrusted"},
		{Name: "policies[].description", Description: "Description of the policy, untrusted"},
		{Name: "status", Description: "For status: the standing against the policy"},
		{Name: "status.enforced", Description: "True when the policy is enforced for the website or the instance"},
		{Name: "status.config_controlled", Description: "True when the policy is controlled by the configuration file"},
		{Name: "status.requirements", Description: "The requirements of the policy, each with name, value " +
			"(compliant, non_compliant, or unknown), and notes, untrusted"},
		{Name: "settings", Description: "For settings: the granular settings of the policy"},
		{Name: "settings.policy", Description: "Policy ID"},
		{Name: "settings.title", Description: "Title of the policy, untrusted"},
		{Name: "settings.description", Description: "Description of the policy, untrusted"},
		{Name: "settings.config_controlled", Description: "True when the policy is controlled by the configuration file"},
		{Name: "settings.policy_enforced", Description: "True when every toggleable setting is enforced"},
		{Name: "settings.settings", Description: "The settings of the policy, each with id, name, what_it_does, " +
			"impact, status (compliant, enforced, non_compliant, and others), enforced (absent when the setting " +
			"cannot be toggled), toggleable, and section, untrusted"},
	},
	Examples: []capability.Example{
		{Description: "List the compliance policies", Arguments: json.RawMessage(`{}`)},
		{Description: "Read the settings of a policy for the whole instance",
			Arguments: json.RawMessage(`{"kind":"settings","policy":"cnil_v1","all_sites":true}`)},
	},
}

// CompliancePolicy is one available policy.
type CompliancePolicy struct {
	ID          string `json:"id"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

// ComplianceRequirement is one requirement of a policy.
type ComplianceRequirement struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
	Notes string `json:"notes,omitempty"`
}

// ComplianceStatus is the answer of getComplianceStatus.
type ComplianceStatus struct {
	Enforced         *bool                   `json:"enforced,omitempty"`
	ConfigControlled *bool                   `json:"config_controlled,omitempty"`
	Requirements     []ComplianceRequirement `json:"requirements,omitempty"`
}

// ComplianceSetting is one granular setting of a policy.
type ComplianceSetting struct {
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	WhatItDoes string `json:"what_it_does,omitempty"`
	Impact     string `json:"impact,omitempty"`
	Status     string `json:"status,omitempty"`
	Enforced   *bool  `json:"enforced,omitempty"`
	Toggleable *bool  `json:"toggleable,omitempty"`
	Section    string `json:"section,omitempty"`
}

// ComplianceSettings is the answer of getCompliancePolicySettings.
type ComplianceSettings struct {
	Policy           string              `json:"policy,omitempty"`
	Title            string              `json:"title,omitempty"`
	Description      string              `json:"description,omitempty"`
	ConfigControlled *bool               `json:"config_controlled,omitempty"`
	PolicyEnforced   *bool               `json:"policy_enforced,omitempty"`
	Settings         []ComplianceSetting `json:"settings,omitempty"`
}

// ComplianceResult is the answer of compliance.get.
type ComplianceResult struct {
	Kind     string              `json:"kind"`
	Policies []CompliancePolicy  `json:"policies,omitempty"`
	Status   *ComplianceStatus   `json:"status,omitempty"`
	Settings *ComplianceSettings `json:"settings,omitempty"`
}

func invokeComplianceGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get compliance"
	var args struct {
		Kind     *string `json:"kind"`
		Policy   *string `json:"policy"`
		IDSite   *int    `json:"idSite"`
		AllSites bool    `json:"all_sites"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	// Checked here as well as in Reaches: the tool needs the instance target, whatever the caller sends.
	if err := requireInstance(bound); err != nil {
		return nil, err
	}
	kind := "policies"
	if args.Kind != nil {
		kind = *args.Kind
	}
	if !slices.Contains(complianceKinds, kind) {
		return nil, invalidArgument("kind must be one of policies, status, settings")
	}
	params := url.Values{}
	if kind == "policies" {
		if args.Policy != nil || args.IDSite != nil || args.AllSites {
			return nil, invalidArgument("policy, idSite, and all_sites apply only to status and settings")
		}
	} else {
		if args.Policy == nil || !validPolicy(*args.Policy) {
			return nil, invalidArgument("policy must hold 1 to 64 letters, digits, and the characters _ . -")
		}
		site, err := complianceSite(resolved, args.IDSite, args.AllSites)
		if err != nil {
			return nil, err
		}
		params.Set("idSite", site)
		if kind == "status" {
			params.Set("complianceType", *args.Policy)
		} else {
			params.Set("compliancePolicy", *args.Policy)
		}
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := requirement{tool: complianceGet.ID, plugin: "PrivacyManager"}
	result := &ComplianceResult{Kind: kind}
	var answer json.RawMessage
	switch kind {
	case "policies":
		if err := client.call(ctx, op, req, "PrivacyManager.getCompliancePolicies", params, &answer); err != nil {
			return nil, err
		}
		policies, ok := policiesOf(answer)
		if !ok {
			return nil, provider.InvalidResponse(op, "Matomo returned invalid compliance policies")
		}
		result.Policies = policies
	case "status":
		var status row
		if err := client.call(ctx, op, req, "PrivacyManager.getComplianceStatus", params, &answer); err != nil {
			return nil, err
		}
		if !answerObject(answer, &status) {
			return nil, provider.InvalidResponse(op, "Matomo returned an invalid compliance status")
		}
		result.Status = statusOf(status)
	default:
		var settings row
		if err := client.call(ctx, op, req, "PrivacyManager.getCompliancePolicySettings", params, &answer); err != nil {
			return nil, err
		}
		if !answerObject(answer, &settings) {
			return nil, provider.InvalidResponse(op, "Matomo returned invalid compliance settings")
		}
		result.Settings = complianceSettingsOf(settings)
	}
	return result, nil
}

// complianceSite settles the website of a compliance call before any secret is read: the checked idSite, or
// all for the instance-wide state.
func complianceSite(resolved *config.Resolved, idSite *int, allSites bool) (string, error) {
	if (idSite == nil) == !allSites {
		return "", invalidArgument("exactly one of idSite and all_sites is required")
	}
	if idSite == nil {
		return "all", nil
	}
	id, err := selectSite(resolved, idSite)
	if err != nil {
		return "", err
	}
	return strconv.Itoa(id), nil
}

func validPolicy(policy string) bool {
	if policy == "" || len(policy) > maxPolicyNameLength {
		return false
	}
	for _, r := range policy {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == '-') {
			return false
		}
	}
	return true
}

// answerObject reads an object answer; an empty list, which is how PHP encodes an empty array, reads as empty.
func answerObject(raw json.RawMessage, out *row) bool {
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte("[]")) {
		*out = row{}
		return true
	}
	return json.Unmarshal(trimmed, out) == nil
}

// policiesOf reads the policies, a list of objects or an object of objects.
func policiesOf(raw json.RawMessage) ([]CompliancePolicy, bool) {
	var listed []row
	if err := json.Unmarshal(raw, &listed); err != nil {
		var named map[string]row
		if json.Unmarshal(raw, &named) != nil {
			return nil, false
		}
		keys := make([]string, 0, len(named))
		for key := range named {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			listed = append(listed, named[key])
		}
	}
	policies := make([]CompliancePolicy, 0, len(listed))
	for _, one := range listed {
		id := one.text("id", maxStringLength)
		if id == "" || len(policies) >= maxPolicies {
			continue
		}
		policies = append(policies, CompliancePolicy{ID: id, Title: one.text("title", maxStringLength),
			Description: one.text("description", maxPrivacyText)})
	}
	return policies, true
}

func statusOf(r row) *ComplianceStatus {
	status := &ComplianceStatus{Enforced: r.get("complianceModeEnforced").flag(),
		ConfigControlled: r.get("complianceConfigControlled").flag()}
	var requirements []row
	if json.Unmarshal(r["complianceRequirements"], &requirements) == nil {
		for _, one := range requirements {
			if len(status.Requirements) >= maxPolicyItems {
				break
			}
			status.Requirements = append(status.Requirements, ComplianceRequirement{Name: one.text("name", maxStringLength),
				Value: one.text("value", maxStringLength), Notes: one.text("notes", maxPrivacyText)})
		}
	}
	return status
}

func complianceSettingsOf(r row) *ComplianceSettings {
	out := &ComplianceSettings{Policy: r.text("policy", maxStringLength), Title: r.text("title", maxStringLength),
		Description: r.text("description", maxPrivacyText), ConfigControlled: r.get("configControlled").flag(),
		PolicyEnforced: r.get("policyEnforced").flag()}
	var settings []row
	if json.Unmarshal(r["settings"], &settings) == nil {
		for _, one := range settings {
			if len(out.Settings) >= maxPolicyItems {
				break
			}
			out.Settings = append(out.Settings, ComplianceSetting{ID: one.text("id", maxStringLength),
				Name: one.text("name", maxStringLength), WhatItDoes: one.text("whatItDoes", maxPrivacyText),
				Impact: one.text("impact", maxPrivacyText), Status: one.text("status", maxStringLength),
				Enforced: nullableFlag(one, "enforced"), Toggleable: one.get("toggleable").flag(),
				Section: one.text("section", maxStringLength)})
		}
	}
	return out
}

// nullableFlag reads a flag that Matomo reports as null when the setting cannot be toggled.
func nullableFlag(r row, key string) *bool {
	if string(bytes.TrimSpace(r[key])) == "null" {
		return nil
	}
	return r.get(key).flag()
}
