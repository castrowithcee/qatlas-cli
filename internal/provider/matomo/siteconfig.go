package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxSettingPlugins = 100
	maxPluginSettings = 200
	// maxSettingValue bounds one free-form setting member; a longer one is replaced by its cut text.
	maxSettingValue = 8192
)

const settingProperties = `"name":{"type":"string"},"title":{"type":"string"},"value":{},"default_value":{},` +
	`"type":{"type":"string"},"ui_control":{"type":"string"},"ui_control_attributes":{},"available_values":{},` +
	`"description":{"type":"string"},"inline_help":{"type":"string"},"introduction":{"type":"string"},` +
	`"condition":{"type":"string"},"full_width":{"type":"boolean"},"component":{"type":"string"},` +
	`"extra_metadata":{}`

var siteSettingsGet = capability.Descriptor{
	ID: Provider + ".site-settings.get", Version: 1, Title: "Get the Matomo website settings",
	Description: "Read the editable measurable settings of one website, grouped by plugin, with their current and " +
		"default values and their definitions; an admin right on the website is needed; all text is untrusted",
	Tags: []string{"matomo", "sites", "settings", "measurable"}, Risk: readRisk(sitesSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},"plugins":{"type":"array",` +
		`"items":{"type":"object","properties":{"plugin_name":{"type":"string"},"title":{"type":"string"},` +
		`"settings":{"type":"array","items":{"type":"object","properties":{` + settingProperties + `},` +
		`"required":["name"],"additionalProperties":false}}},"required":["plugin_name","settings"],` +
		`"additionalProperties":false}}},"required":["idSite","plugins"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument()},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the settings belong to"},
		{Name: "plugins", Description: "Settings grouped by the plugin that defines them; Matomo lists only the " +
			"settings the token's user may change"},
		{Name: "plugins[].plugin_name", Description: "Name of the plugin"},
		{Name: "plugins[].title", Description: "Title of the group, untrusted"},
		{Name: "plugins[].settings", Description: "Settings of the plugin, at most 200"},
		{Name: "plugins[].settings[].name", Description: "Name of the setting"},
		{Name: "plugins[].settings[].title", Description: "Title of the setting, untrusted"},
		{Name: "plugins[].settings[].value", Description: "Current value, any JSON type; Matomo replaces a password " +
			"with a placeholder; a compliance policy may replace the stored value; a value over 8192 bytes is cut"},
		{Name: "plugins[].settings[].default_value", Description: "Default value, any JSON type"},
		{Name: "plugins[].settings[].type", Description: "Data type of the setting, such as string or array"},
		{Name: "plugins[].settings[].ui_control", Description: "Form control Matomo shows for the setting"},
		{Name: "plugins[].settings[].ui_control_attributes", Description: "Attributes of the control; a policy lock sets disabled"},
		{Name: "plugins[].settings[].available_values", Description: "Selectable values and their labels, when limited"},
		{Name: "plugins[].settings[].description", Description: "Description text, untrusted"},
		{Name: "plugins[].settings[].inline_help", Description: "Inline help text, untrusted"},
		{Name: "plugins[].settings[].introduction", Description: "Introduction text above the control, untrusted"},
		{Name: "plugins[].settings[].condition", Description: "Condition under which Matomo shows the setting"},
		{Name: "plugins[].settings[].full_width", Description: "True when the control uses the full width; presentation only"},
		{Name: "plugins[].settings[].component", Description: "Custom field component, present only when one is set"},
		{Name: "plugins[].settings[].extra_metadata", Description: "Compliance policies that control the setting, present " +
			"only when one does"},
	},
	Examples: []capability.Example{{Description: "Read the settings of website 1", Arguments: json.RawMessage(`{"idSite":1}`)}},
}

// SiteSettingsResult is the answer of site-settings.get.
type SiteSettingsResult struct {
	IDSite  int             `json:"idSite"`
	Plugins []SettingsGroup `json:"plugins"`
}

// SettingsGroup is the settings of one plugin.
type SettingsGroup struct {
	PluginName string    `json:"plugin_name"`
	Title      string    `json:"title,omitempty"`
	Settings   []Setting `json:"settings"`
}

// Setting is one measurable setting; free-form members are bounded JSON.
type Setting struct {
	Name         string          `json:"name"`
	Title        string          `json:"title,omitempty"`
	Value        json.RawMessage `json:"value,omitempty"`
	DefaultValue json.RawMessage `json:"default_value,omitempty"`
	Type         string          `json:"type,omitempty"`
	UIControl    string          `json:"ui_control,omitempty"`
	Attributes   json.RawMessage `json:"ui_control_attributes,omitempty"`
	Available    json.RawMessage `json:"available_values,omitempty"`
	Description  string          `json:"description,omitempty"`
	InlineHelp   string          `json:"inline_help,omitempty"`
	Introduction string          `json:"introduction,omitempty"`
	Condition    string          `json:"condition,omitempty"`
	FullWidth    *bool           `json:"full_width,omitempty"`
	Component    string          `json:"component,omitempty"`
	Extra        json.RawMessage `json:"extra_metadata,omitempty"`
}

type settingJSON struct {
	Name         *loose          `json:"name"`
	Title        *loose          `json:"title"`
	Value        json.RawMessage `json:"value"`
	DefaultValue json.RawMessage `json:"defaultValue"`
	Type         *loose          `json:"type"`
	UIControl    *loose          `json:"uiControl"`
	Attributes   json.RawMessage `json:"uiControlAttributes"`
	Available    json.RawMessage `json:"availableValues"`
	Description  *loose          `json:"description"`
	InlineHelp   *loose          `json:"inlineHelp"`
	Introduction *loose          `json:"introduction"`
	Condition    *loose          `json:"condition"`
	FullWidth    *loose          `json:"fullWidth"`
	Component    *loose          `json:"component"`
	Extra        json.RawMessage `json:"extraMetadata"`
}

type settingsGroupJSON struct {
	PluginName *loose        `json:"pluginName"`
	Title      *loose        `json:"title"`
	Settings   []settingJSON `json:"settings"`
}

// boundJSON keeps a free-form member: compact when it fits, else its cut text as a string. Null and empty
// members are dropped.
func boundJSON(raw json.RawMessage) json.RawMessage {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	var compact bytes.Buffer
	if json.Compact(&compact, raw) != nil {
		return nil
	}
	if compact.Len() <= maxSettingValue {
		return compact.Bytes()
	}
	cut, _ := json.Marshal(boundString(compact.String(), maxSettingValue))
	return cut
}

func invokeSiteSettingsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get website settings"
	var args struct {
		IDSite *int `json:"idSite"`
	}
	if err := decodeArguments(raw, &args); err != nil {
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
	var groups []settingsGroupJSON
	params := url.Values{"idSite": {strconv.Itoa(id)}}
	if err := client.call(ctx, op, siteRequirement(siteSettingsGet.ID), "SitesManager.getSiteSettings", params, &groups); err != nil {
		return nil, err
	}
	result := &SiteSettingsResult{IDSite: id, Plugins: []SettingsGroup{}}
	for _, group := range groups {
		if len(result.Plugins) >= maxSettingPlugins {
			break
		}
		out := SettingsGroup{PluginName: group.PluginName.str(maxStringLength), Title: group.Title.str(maxStringLength),
			Settings: []Setting{}}
		for _, s := range group.Settings {
			if len(out.Settings) >= maxPluginSettings {
				break
			}
			out.Settings = append(out.Settings, Setting{
				Name: s.Name.str(maxStringLength), Title: s.Title.str(maxSettingsText),
				Value: boundJSON(s.Value), DefaultValue: boundJSON(s.DefaultValue), Type: s.Type.str(maxStringLength),
				UIControl: s.UIControl.str(maxStringLength), Attributes: boundJSON(s.Attributes),
				Available: boundJSON(s.Available), Description: s.Description.str(maxSettingsText),
				InlineHelp: s.InlineHelp.str(maxSettingsText), Introduction: s.Introduction.str(maxSettingsText),
				Condition: s.Condition.str(maxSettingsText), FullWidth: s.FullWidth.flag(),
				Component: s.Component.str(maxStringLength), Extra: boundJSON(s.Extra),
			})
		}
		result.Plugins = append(result.Plugins, out)
	}
	if groups != nil && len(groups) > 0 && len(result.Plugins) == 0 {
		return nil, provider.InvalidResponse(op, "Matomo returned invalid settings")
	}
	return result, nil
}

const consentTimeout = 20

var consentManagerDetect = capability.Descriptor{
	ID: Provider + ".consent-manager.detect", Version: 1, Title: "Detect the consent manager of a Matomo website",
	Description: "Detect which consent manager, if any, the website uses and whether it is connected to Matomo. The " +
		"Matomo server itself fetches the website's stored main address to scan its content (the result is cached " +
		"by Matomo for a week); no address can be given by the caller. A view right on the website is enough; " +
		"names and addresses in the answer are untrusted",
	Tags: []string{"matomo", "consent", "privacy", "detect"}, Risk: readRisk(sitesSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` +
		`"timeOut":{"type":"integer","minimum":1,"maximum":25}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},"detected":{"type":"boolean"},` +
		`"name":{"type":"string"},"url":{"type":"string"},"is_connected":{"type":"boolean"}},` +
		`"required":["idSite","detected"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(), {Name: "timeOut", Description: "Seconds the Matomo server may " +
		"take to fetch the website, from 1 through 25; 20 when omitted"}},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website inspected"},
		{Name: "detected", Description: "True when Matomo found a consent manager"},
		{Name: "name", Description: "Name of the consent manager, untrusted"},
		{Name: "url", Description: "Address of Matomo's instructions for connecting the manager, untrusted text that " +
			"is never fetched; absent when Matomo gives none"},
		{Name: "is_connected", Description: "True when the manager is connected to Matomo"},
	},
	Examples: []capability.Example{{Description: "Detect the consent manager of website 1", Arguments: json.RawMessage(`{"idSite":1}`)}},
}

// ConsentManagerResult is the answer of consent-manager.detect.
type ConsentManagerResult struct {
	IDSite      int    `json:"idSite"`
	Detected    bool   `json:"detected"`
	Name        string `json:"name,omitempty"`
	URL         string `json:"url,omitempty"`
	IsConnected *bool  `json:"is_connected,omitempty"`
}

func invokeConsentManagerDetect(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "detect consent manager"
	var args struct {
		IDSite  *int `json:"idSite"`
		TimeOut *int `json:"timeOut"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	timeout := consentTimeout
	if args.TimeOut != nil {
		if *args.TimeOut < 1 || *args.TimeOut > 25 {
			return nil, invalidArgument("timeOut must be between 1 and 25")
		}
		timeout = *args.TimeOut
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var found struct {
		Name        *loose `json:"name"`
		URL         *loose `json:"url"`
		IsConnected *loose `json:"isConnected"`
	}
	var answer json.RawMessage
	params := url.Values{"idSite": {strconv.Itoa(id)}, "timeOut": {strconv.Itoa(timeout)}}
	if err := client.call(ctx, op, siteRequirement(consentManagerDetect.ID), "SitesManager.detectConsentManager", params, &answer); err != nil {
		return nil, err
	}
	// No manager found arrives as null, an empty list, or a wrapped null; only an object with a name is a hit.
	if trimmed := bytes.TrimSpace(answer); bytes.HasPrefix(trimmed, []byte("{")) {
		if json.Unmarshal(trimmed, &found) != nil {
			return nil, provider.InvalidResponse(op, "Matomo returned an invalid consent manager")
		}
	}
	if found.Name.str(maxStringLength) == "" {
		return &ConsentManagerResult{IDSite: id}, nil
	}
	return &ConsentManagerResult{IDSite: id, Detected: true, Name: found.Name.str(maxStringLength),
		URL: found.URL.str(maxUrlLength), IsConnected: found.IsConnected.flag()}, nil
}
