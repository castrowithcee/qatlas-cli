package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"slices"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	aiProvidersSensitive = "matomo-ai-providers"
	maxAIProviders       = 100
	maxAIText            = 512
)

var (
	// firstAIProviders is the release that bundled the AIProviders plugin.
	firstAIProviders   = version{5, 13, 0}
	aiProviderIDRegexp = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	aiProviderSentOnce = sentOnceFor("AI provider settings", "matomo.ai-providers.get-settings")
)

func aiProvidersRequirement(tool string) requirement {
	return requirement{tool: tool, plugin: "AIProviders", minVersion: firstAIProviders}
}

const aiProviderIDSchema = `"providerId":{"type":"string","pattern":"^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$","maxLength":64}`

const aiProviderProperties = `"id":{"type":"string"},"name":{"type":"string"},"description":{"type":"string"},` +
	`"supportsCustomEndpoint":{"type":"boolean"},"supportsFipsEndpoint":{"type":"boolean"},` +
	`"defaultEndpointUrl":{"type":"string"},"defaultModel":{"type":"string"},"api_key_set":{"type":"boolean"},` +
	`"endpointUrl":{"type":"string"},"model":{"type":"string"},"useFipsEndpoint":{"type":"boolean"},` +
	`"isUsable":{"type":"boolean"}`

var aiProvidersGetSettings = capability.Descriptor{
	ID: Provider + ".ai-providers.get-settings", Version: 1, Title: "Get the Matomo AI provider settings",
	Description: "Read which external AI providers the AIProviders plugin offers, which one is the default, and how " +
		"each is connected. It needs Super User rights and a connection whose targets name the instance. An API " +
		"key never appears, not even in part: only api_key_set tells whether one is stored. Texts come from " +
		"Matomo and its plugins and are untrusted and cut at a limit; an endpoint address is shown without " +
		"credentials, query, or fragment",
	Tags: []string{"matomo", "ai-providers", "settings", "instance"}, Risk: readRisk(aiProvidersSensitive),
	Provider:    Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"defaultProviderId":{"type":"string"},` +
		`"defaultCapabilityLevel":{"type":"string"},"canEditProviderConfiguration":{"type":"boolean"},` +
		`"canEditCapabilityLevel":{"type":"boolean"},"capabilityLevels":{"type":"array","items":{"type":"string"}},` +
		`"providers":{"type":"array","items":{"type":"object","properties":{` + aiProviderProperties + `},` +
		`"required":["id","api_key_set"],"additionalProperties":false}}},"required":["providers"],` +
		`"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "defaultProviderId", Description: "The provider used by default; empty when none is set"},
		{Name: "defaultCapabilityLevel", Description: "The default model capability level"},
		{Name: "canEditProviderConfiguration", Description: "False when the instance forces the provider from its configuration"},
		{Name: "canEditCapabilityLevel", Description: "False when the instance forces the capability level"},
		{Name: "capabilityLevels", Description: "The capability levels Matomo knows"},
		{Name: "providers", Description: "One entry per selectable provider, at most 100"},
		{Name: "providers[].id", Description: "Provider ID, used as providerId by matomo.ai-providers.disconnect"},
		{Name: "providers[].name", Description: "Provider name, untrusted"},
		{Name: "providers[].description", Description: "Provider description, untrusted"},
		{Name: "providers[].supportsCustomEndpoint", Description: "Whether the provider accepts its own endpoint address"},
		{Name: "providers[].supportsFipsEndpoint", Description: "Whether the provider has a FIPS endpoint"},
		{Name: "providers[].defaultEndpointUrl", Description: "The provider's standard endpoint address, untrusted, never fetched"},
		{Name: "providers[].defaultModel", Description: "The provider's standard model, untrusted"},
		{Name: "providers[].api_key_set", Description: "True when an API key is stored; the key itself is never returned"},
		{Name: "providers[].endpointUrl", Description: "The configured endpoint address without credentials, query, or fragment, untrusted, never fetched"},
		{Name: "providers[].model", Description: "The configured model, untrusted"},
		{Name: "providers[].useFipsEndpoint", Description: "Whether the FIPS endpoint is used"},
		{Name: "providers[].isUsable", Description: "Whether Matomo counts the provider as configured"},
	},
	Examples: []capability.Example{{Description: "Read the AI provider settings", Arguments: json.RawMessage(`{}`)}},
}

var aiProvidersDisconnect = capability.Descriptor{
	ID: Provider + ".ai-providers.disconnect", Version: 1, Title: "Disconnect a Matomo AI provider",
	Description: "Remove the stored connection of one external AI provider, its API key and endpoint, for good; it " +
		"cannot be undone and the key has to be entered again to reconnect. It needs Super User rights and a " +
		"connection whose targets name the instance; an instance that forces its provider from configuration may " +
		"keep the forced values. Idempotent in effect" + aiProviderSentOnce + "; offered only where a " +
		"connection's tools list names it",
	Tags: []string{"matomo", "ai-providers", "disconnect", "delete", "instance"},
	Risk: capability.Risk{Effect: capability.EffectDelete, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: aiProvidersSensitive},
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + aiProviderIDSchema + `},"required":["providerId"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"providerId":{"type":"string"},` +
		`"disconnected":{"type":"boolean"}},"required":["providerId","disconnected"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "providerId", Required: true, Description: "ID of the provider, from " +
		"matomo.ai-providers.get-settings: letters, digits, dot, underscore, and hyphen, up to 64 characters"}},
	Fields: []capability.Field{
		{Name: "providerId", Description: "The provider that was disconnected"},
		{Name: "disconnected", Description: "True once Matomo accepted the removal"},
	},
	Examples: []capability.Example{{Description: "Disconnect a provider", Arguments: json.RawMessage(`{"providerId":"openai"}`)}},
}

// AIProviderConfiguration is the connection state of one provider. The API key is only a flag.
type AIProviderConfiguration struct {
	APIKeySet       bool   `json:"api_key_set"`
	EndpointURL     string `json:"endpointUrl,omitempty"`
	Model           string `json:"model,omitempty"`
	UseFipsEndpoint bool   `json:"useFipsEndpoint,omitempty"`
	IsUsable        bool   `json:"isUsable,omitempty"`
}

// AIProvider is one provider of the AIProviders plugin. All text is untrusted provider data.
type AIProvider struct {
	ID                     string `json:"id"`
	Name                   string `json:"name,omitempty"`
	Description            string `json:"description,omitempty"`
	SupportsCustomEndpoint bool   `json:"supportsCustomEndpoint,omitempty"`
	SupportsFipsEndpoint   bool   `json:"supportsFipsEndpoint,omitempty"`
	DefaultEndpointURL     string `json:"defaultEndpointUrl,omitempty"`
	DefaultModel           string `json:"defaultModel,omitempty"`
	AIProviderConfiguration
}

// AIProvidersSettings is the answer of ai-providers.get-settings.
type AIProvidersSettings struct {
	DefaultProviderID            string       `json:"defaultProviderId,omitempty"`
	DefaultCapabilityLevel       string       `json:"defaultCapabilityLevel,omitempty"`
	CanEditProviderConfiguration bool         `json:"canEditProviderConfiguration"`
	CanEditCapabilityLevel       bool         `json:"canEditCapabilityLevel"`
	CapabilityLevels             []string     `json:"capabilityLevels,omitempty"`
	Providers                    []AIProvider `json:"providers"`
}

// AIProviderDisconnected is the answer of ai-providers.disconnect.
type AIProviderDisconnected struct {
	ProviderID   string `json:"providerId"`
	Disconnected bool   `json:"disconnected"`
}

// aiSettingsJSON is the answer of AIProviders.getSettings. Matomo sends only hasApiKey; apiKey is read so a
// value that appears anyway is registered for redaction and never copied.
type aiSettingsJSON struct {
	DefaultProviderID            *loose           `json:"defaultProviderId"`
	DefaultCapabilityLevel       *loose           `json:"defaultCapabilityLevel"`
	CanEditProviderConfiguration *loose           `json:"canEditProviderConfiguration"`
	CanEditCapabilityLevel       *loose           `json:"canEditCapabilityLevel"`
	CapabilityLevels             json.RawMessage  `json:"capabilityLevels"`
	Providers                    []aiProviderJSON `json:"providers"`
}

type aiProviderJSON struct {
	ID                     *loose `json:"id"`
	Name                   *loose `json:"name"`
	Description            *loose `json:"description"`
	SupportsCustomEndpoint *loose `json:"supportsCustomEndpoint"`
	SupportsFipsEndpoint   *loose `json:"supportsFipsEndpoint"`
	DefaultEndpointURL     *loose `json:"defaultEndpointUrl"`
	DefaultModel           *loose `json:"defaultModel"`
	Configuration          struct {
		HasAPIKey       *loose `json:"hasApiKey"`
		APIKey          *loose `json:"apiKey"`
		EndpointURL     *loose `json:"endpointUrl"`
		Model           *loose `json:"model"`
		UseFipsEndpoint *loose `json:"useFipsEndpoint"`
		IsUsable        *loose `json:"isUsable"`
	} `json:"configuration"`
}

func looseFlag(l *loose) bool {
	set := l.flag()
	return set != nil && *set
}

// safeEndpoint keeps an address readable but drops anything that could carry a credential.
func safeEndpoint(l *loose) string {
	text := l.str(maxUrlLength)
	if text == "" {
		return ""
	}
	parsed, err := url.Parse(text)
	if err != nil || parsed.Host == "" || parsed.Scheme != "https" && parsed.Scheme != "http" {
		return ""
	}
	parsed.User, parsed.RawQuery, parsed.Fragment = nil, "", ""
	return parsed.String()
}

func invokeAIProvidersGetSettings(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get AI provider settings"
	var args struct{}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if !bound.instance {
		return nil, invalidArgument("this tool needs a connection bound to the instance")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer aiSettingsJSON
	if err := client.call(ctx, op, aiProvidersRequirement(aiProvidersGetSettings.ID), "AIProviders.getSettings",
		url.Values{}, &answer); err != nil {
		return nil, err
	}
	// A key in the answer is registered before anything else is read from it.
	for _, row := range answer.Providers {
		if key := row.Configuration.APIKey.str(maxStringLength); key != "" {
			red.Add(key)
		}
	}
	out := &AIProvidersSettings{DefaultProviderID: answer.DefaultProviderID.str(maxAIText),
		DefaultCapabilityLevel:       answer.DefaultCapabilityLevel.str(maxAIText),
		CanEditProviderConfiguration: looseFlag(answer.CanEditProviderConfiguration),
		CanEditCapabilityLevel:       looseFlag(answer.CanEditCapabilityLevel), Providers: []AIProvider{}}
	var levels map[string]json.RawMessage
	if json.Unmarshal(answer.CapabilityLevels, &levels) == nil {
		for level := range levels {
			out.CapabilityLevels = append(out.CapabilityLevels, boundString(level, maxAIText))
		}
		slices.Sort(out.CapabilityLevels)
	}
	for _, row := range answer.Providers {
		if len(out.Providers) == maxAIProviders {
			break
		}
		id := row.ID.str(maxAIText)
		if id == "" {
			return nil, provider.InvalidResponse(op, "Matomo returned an invalid AI provider")
		}
		c := row.Configuration
		out.Providers = append(out.Providers, AIProvider{ID: id, Name: row.Name.str(maxAIText),
			Description: row.Description.str(maxAIText), SupportsCustomEndpoint: looseFlag(row.SupportsCustomEndpoint),
			SupportsFipsEndpoint: looseFlag(row.SupportsFipsEndpoint), DefaultEndpointURL: safeEndpoint(row.DefaultEndpointURL),
			DefaultModel: row.DefaultModel.str(maxAIText),
			AIProviderConfiguration: AIProviderConfiguration{APIKeySet: looseFlag(c.HasAPIKey),
				EndpointURL: safeEndpoint(c.EndpointURL), Model: c.Model.str(maxAIText),
				UseFipsEndpoint: looseFlag(c.UseFipsEndpoint), IsUsable: looseFlag(c.IsUsable)}})
	}
	return out, nil
}

func invokeAIProvidersDisconnect(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "disconnect AI provider"
	var args struct {
		ProviderID string `json:"providerId"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if !aiProviderIDRegexp.MatchString(args.ProviderID) {
		return nil, invalidArgument("providerId must be 1 to 64 letters, digits, dots, underscores, or hyphens")
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if !bound.instance {
		return nil, invalidArgument("this tool needs a connection bound to the instance")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, aiProvidersRequirement(aiProvidersDisconnect.ID), "AIProviders.disconnectProvider",
		url.Values{"providerId": {args.ProviderID}}, aiProviderSentOnce, &answer); err != nil {
		return nil, err
	}
	// Matomo answers with the settings; they are not read, only their shape proves the call went through.
	var settings struct {
		Providers json.RawMessage `json:"providers"`
	}
	if json.Unmarshal(answer, &settings) != nil || settings.Providers == nil {
		return nil, unclearChange(op, "Matomo returned no confirmation of the removal", aiProviderSentOnce)
	}
	return &AIProviderDisconnected{ProviderID: args.ProviderID, Disconnected: true}, nil
}
