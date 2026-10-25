package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxOptions        = 50
	maxColumns        = 200
	maxControlledList = 100
	maxPrivacyText    = maxUrlLength
)

var privacySections = []string{"settings", "visit-columns", "action-columns"}

var privacySettingsGet = capability.Descriptor{
	ID: Provider + ".privacy-settings.get", Version: 1, Title: "Get the Matomo privacy settings",
	Description: "Read the anonymization and privacy settings, or the columns of the visit log that can be anonymized. " +
		"section settings (the default) reads the settings of one website with idSite (PrivacyManager." +
		"getAnonymisationSettings, an admin right on the website) or the global settings without idSite (Super User; " +
		"needs a connection bound to the instance target); section visit-columns " +
		"(getAvailableVisitColumnsToAnonymize) and action-columns (getAvailableLinkVisitActionColumnsToAnonymize) " +
		"list the columns that manual anonymization can clear, need Super User rights and the instance target; " +
		"names and texts are translated by Matomo and untrusted",
	Tags: []string{"matomo", "gdpr", "privacy", "anonymization", "settings", "get"}, Risk: readRisk(instanceSensitive),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"section":{"type":"string","enum":[` +
		quotedList(privacySections) + `]},` + siteKey + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"section":{"type":"string"},` +
		`"id_site":{"type":"integer"},"settings":{"type":"object"},"visit_columns":{"type":"array","items":` +
		`{"type":"object"}},"action_columns":{"type":"array","items":{"type":"object"}}},` +
		`"required":["section"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "section", Description: "What to read: settings, visit-columns, or action-columns; settings when omitted"},
		{Name: "idSite", Description: "Only for settings: the website whose settings are read; must lie inside the " +
			"connection's targets. Without it the global settings are read and the instance target is needed",
			Target: "site"},
	},
	Fields: []capability.Field{
		{Name: "section", Description: "The section read"},
		{Name: "id_site", Description: "The website of the settings, absent for the global settings"},
		{Name: "settings", Description: "For settings: the privacy settings; with a website they are the effective values " +
			"of that website, which fall back to the global ones unless the website has its own"},
		{Name: "settings.ip_anonymizer_enabled", Description: "True when IP addresses are anonymized"},
		{Name: "settings.ip_address_mask_length", Description: "Number of bytes of an IP address that are masked, 1 to 4"},
		{Name: "settings.use_anonymized_ip_for_visit_enrichment", Description: "True when the anonymized IP address is " +
			"used for location and provider lookups"},
		{Name: "settings.do_not_track_enabled", Description: "True when Do Not Track requests are respected"},
		{Name: "settings.force_cookieless_tracking", Description: "True when tracking without cookies is forced"},
		{Name: "settings.anonymize_user_id", Description: "True when user IDs are pseudonymized"},
		{Name: "settings.anonymize_order_id", Description: "True when ecommerce order IDs are anonymized"},
		{Name: "settings.anonymize_referrer", Description: "How much of a referrer is anonymized, as Matomo's option key"},
		{Name: "settings.randomize_config_id", Description: "True when a new random device fingerprint is made per request"},
		{Name: "settings.use_site_specific_settings", Description: "True when the website has settings of its own"},
		{Name: "settings.mask_length_options", Description: "The choices for the mask length that the compliance " +
			"policies allow, with key, value, and description, untrusted"},
		{Name: "settings.use_anonymized_ip_for_visit_enrichment_options", Description: "The choices for the enrichment " +
			"setting, with key, value, and description, untrusted"},
		{Name: "settings.referrer_anonymization_options", Description: "The choices for the referrer anonymization that " +
			"the compliance policies allow, with key and value, untrusted"},
		{Name: "settings.tracker_file_name", Description: "File name of the JavaScript tracker; shown to Super Users only"},
		{Name: "settings.tracker_writable", Description: "True when Matomo can write the JavaScript tracker file; " +
			"Super Users only"},
		{Name: "settings.extra_metadata", Description: "Settings that a compliance policy controls, ordered by name, " +
			"each with its website and the policies; present only when a policy controls a setting"},
		{Name: "settings.extra_metadata[].setting", Description: "Name of the controlled setting"},
		{Name: "settings.extra_metadata[].id_site", Description: "Website of the control, absent when global"},
		{Name: "settings.extra_metadata[].policies", Description: "The policies that control the setting with their " +
			"required and effective value, constraint type, scope, and title, untrusted"},
		{Name: "visit_columns", Description: "For visit-columns: the columns that can be anonymized, ordered by name, " +
			"each with column_name and default_value"},
		{Name: "action_columns", Description: "For action-columns: the columns of the actions that can be anonymized, " +
			"ordered by name, each with column_name and default_value"},
	},
	Examples: []capability.Example{
		{Description: "Read the privacy settings of website 1", Arguments: json.RawMessage(`{"idSite":1}`)},
		{Description: "List the visit columns that can be anonymized", Arguments: json.RawMessage(`{"section":"visit-columns"}`)},
	},
}

// Option is one choice of a privacy setting.
type Option struct {
	Key         string `json:"key"`
	Value       string `json:"value,omitempty"`
	Description string `json:"description,omitempty"`
}

// ControllingPolicy is a compliance policy that controls a privacy setting.
type ControllingPolicy struct {
	Policy         string `json:"policy"`
	RequiredValue  any    `json:"required_value,omitempty"`
	EffectiveValue any    `json:"effective_value,omitempty"`
	ConstraintType string `json:"constraint_type,omitempty"`
	Scope          string `json:"scope,omitempty"`
	PolicyTitle    string `json:"policy_title,omitempty"`
}

// ControlledSetting is a setting that compliance policies control.
type ControlledSetting struct {
	Setting  string              `json:"setting"`
	IDSite   *int                `json:"id_site,omitempty"`
	Policies []ControllingPolicy `json:"policies,omitempty"`
}

// AnonymisationSettings is the privacy settings as far as a result shows them: an allow-list of what
// getAnonymisationSettings documents.
type AnonymisationSettings struct {
	IPAnonymizerEnabled                      *bool               `json:"ip_anonymizer_enabled,omitempty"`
	IPAddressMaskLength                      *int                `json:"ip_address_mask_length,omitempty"`
	UseAnonymizedIPForVisitEnrichment        *bool               `json:"use_anonymized_ip_for_visit_enrichment,omitempty"`
	DoNotTrackEnabled                        *bool               `json:"do_not_track_enabled,omitempty"`
	ForceCookielessTracking                  *bool               `json:"force_cookieless_tracking,omitempty"`
	AnonymizeUserID                          *bool               `json:"anonymize_user_id,omitempty"`
	AnonymizeOrderID                         *bool               `json:"anonymize_order_id,omitempty"`
	AnonymizeReferrer                        *string             `json:"anonymize_referrer,omitempty"`
	RandomizeConfigID                        *bool               `json:"randomize_config_id,omitempty"`
	UseSiteSpecificSettings                  *bool               `json:"use_site_specific_settings,omitempty"`
	MaskLengthOptions                        []Option            `json:"mask_length_options,omitempty"`
	UseAnonymizedIPForVisitEnrichmentOptions []Option            `json:"use_anonymized_ip_for_visit_enrichment_options,omitempty"`
	ReferrerAnonymizationOptions             []Option            `json:"referrer_anonymization_options,omitempty"`
	TrackerFileName                          string              `json:"tracker_file_name,omitempty"`
	TrackerWritable                          *bool               `json:"tracker_writable,omitempty"`
	ExtraMetadata                            []ControlledSetting `json:"extra_metadata,omitempty"`
}

// Column is a column that can be anonymized.
type Column struct {
	ColumnName   string `json:"column_name"`
	DefaultValue any    `json:"default_value"`
}

// PrivacySettingsResult is the answer of privacy-settings.get.
type PrivacySettingsResult struct {
	Section       string                 `json:"section"`
	IDSite        *int                   `json:"id_site,omitempty"`
	Settings      *AnonymisationSettings `json:"settings,omitempty"`
	VisitColumns  []Column               `json:"visit_columns,omitempty"`
	ActionColumns []Column               `json:"action_columns,omitempty"`
}

func invokePrivacySettingsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get privacy settings"
	var args struct {
		Section *string `json:"section"`
		IDSite  *int    `json:"idSite"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	section := "settings"
	if args.Section != nil {
		section = *args.Section
	}
	if !slices.Contains(privacySections, section) {
		return nil, invalidArgument("section must be one of settings, visit-columns, action-columns")
	}
	if section != "settings" && args.IDSite != nil {
		return nil, invalidArgument("idSite applies only to the settings section")
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	params := url.Values{}
	result := &PrivacySettingsResult{Section: section}
	if args.IDSite != nil {
		id, err := selectSite(resolved, args.IDSite)
		if err != nil {
			return nil, err
		}
		params.Set("idSiteSpecific", strconv.Itoa(id))
		result.IDSite = &id
	} else if err := requireInstance(bound); err != nil {
		// Without a website every mode is a Super User read of the instance.
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := requirement{tool: privacySettingsGet.ID, plugin: "PrivacyManager"}
	if section != "settings" {
		method := "PrivacyManager.getAvailableVisitColumnsToAnonymize"
		if section == "action-columns" {
			method = "PrivacyManager.getAvailableLinkVisitActionColumnsToAnonymize"
		}
		var rows []row
		if err := client.call(ctx, op, req, method, params, &rows); err != nil {
			return nil, err
		}
		columns := columnsOf(rows)
		if section == "visit-columns" {
			result.VisitColumns = columns
		} else {
			result.ActionColumns = columns
		}
		return result, nil
	}
	var answer row
	if err := client.call(ctx, op, req, "PrivacyManager.getAnonymisationSettings", params, &answer); err != nil {
		return nil, err
	}
	result.Settings = settingsOf(answer)
	return result, nil
}

func columnsOf(rows []row) []Column {
	columns := make([]Column, 0, len(rows))
	for _, one := range rows {
		name := one.text("column_name", maxStringLength)
		if name == "" {
			continue
		}
		if len(columns) >= maxColumns {
			break
		}
		columns = append(columns, Column{ColumnName: name, DefaultValue: scalarValue(one["default_value"])})
	}
	slices.SortStableFunc(columns, func(a, b Column) int { return strings.Compare(a.ColumnName, b.ColumnName) })
	return columns
}

// scalarValue passes a number, flag, or text on and shortens it; anything nested or empty reads as absent.
func scalarValue(raw json.RawMessage) any {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] == '{' || trimmed[0] == '[' {
		return nil
	}
	var value loose
	if json.Unmarshal(trimmed, &value) != nil {
		return nil
	}
	if trimmed[0] == '"' {
		return value.str(maxStringLength)
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	return json.RawMessage(trimmed)
}

func settingsOf(r row) *AnonymisationSettings {
	text := func(key string) *string {
		if _, found := r[key]; !found {
			return nil
		}
		value := r.text(key, maxStringLength)
		return &value
	}
	return &AnonymisationSettings{
		IPAnonymizerEnabled:                      r.get("ipAnonymizerEnabled").flag(),
		IPAddressMaskLength:                      r.integer("ipAddressMaskLength"),
		UseAnonymizedIPForVisitEnrichment:        r.get("useAnonymizedIpForVisitEnrichment").flag(),
		DoNotTrackEnabled:                        r.get("doNotTrackEnabled").flag(),
		ForceCookielessTracking:                  r.get("forceCookielessTracking").flag(),
		AnonymizeUserID:                          r.get("anonymizeUserId").flag(),
		AnonymizeOrderID:                         r.get("anonymizeOrderId").flag(),
		AnonymizeReferrer:                        text("anonymizeReferrer"),
		RandomizeConfigID:                        r.get("randomizeConfigId").flag(),
		UseSiteSpecificSettings:                  r.get("useSiteSpecificSettings").flag(),
		MaskLengthOptions:                        optionsOf(r["maskLengthOptions"]),
		UseAnonymizedIPForVisitEnrichmentOptions: optionsOf(r["useAnonymizedIpForVisitEnrichmentOptions"]),
		ReferrerAnonymizationOptions:             optionsOf(r["referrerAnonymizationOptions"]),
		TrackerFileName:                          r.text("trackerFileName", maxStringLength),
		TrackerWritable:                          r.get("trackerWritable").flag(),
		ExtraMetadata:                            controlledOf(r["extraMetadata"]),
	}
}

// optionsOf reads the two shapes Matomo uses for the choices of a setting: a list of key, value, and
// description, or an object from key to label.
func optionsOf(raw json.RawMessage) []Option {
	var listed []row
	if json.Unmarshal(raw, &listed) == nil {
		options := make([]Option, 0, len(listed))
		for _, one := range listed {
			if len(options) >= maxOptions {
				break
			}
			options = append(options, Option{Key: one.text("key", maxStringLength),
				Value: one.text("value", maxStringLength), Description: one.text("description", maxPrivacyText)})
		}
		return options
	}
	var labelled map[string]*loose
	if json.Unmarshal(raw, &labelled) != nil {
		return nil
	}
	keys := make([]string, 0, len(labelled))
	for key := range labelled {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	options := make([]Option, 0, len(keys))
	for _, key := range keys {
		if len(options) >= maxOptions {
			break
		}
		options = append(options, Option{Key: bounded(key), Value: labelled[key].str(maxStringLength)})
	}
	return options
}

func controlledOf(raw json.RawMessage) []ControlledSetting {
	var named map[string]row
	if json.Unmarshal(raw, &named) != nil {
		return nil
	}
	names := make([]string, 0, len(named))
	for name := range named {
		names = append(names, name)
	}
	slices.Sort(names)
	var out []ControlledSetting
	for _, name := range names {
		if len(out) >= maxControlledList {
			break
		}
		entry := named[name]
		setting := ControlledSetting{Setting: bounded(name), IDSite: entry.integer("idSite")}
		var policies map[string]row
		if json.Unmarshal(entry["compliancePolicyControlled"], &policies) == nil {
			policyNames := make([]string, 0, len(policies))
			for policy := range policies {
				policyNames = append(policyNames, policy)
			}
			slices.Sort(policyNames)
			for _, policy := range policyNames {
				if len(setting.Policies) >= maxControlledList {
					break
				}
				one := policies[policy]
				setting.Policies = append(setting.Policies, ControllingPolicy{Policy: bounded(policy),
					RequiredValue: boundedJSON(one["requiredValue"]), EffectiveValue: boundedJSON(one["effectiveValue"]),
					ConstraintType: one.text("constraintType", maxStringLength), Scope: one.text("scope", maxStringLength),
					PolicyTitle: one.text("policyTitle", maxStringLength)})
			}
		}
		out = append(out, setting)
	}
	return out
}
