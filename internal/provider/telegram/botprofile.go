package telegram

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Bot API limits in Unicode characters.
const (
	maxBotNameRunes             = 64
	maxBotDescriptionRunes      = 512
	maxBotShortDescriptionRunes = 120
)

const languageCodeSchema = `"language_code":{"type":"string","pattern":"^([a-z]{2})?$"}`

var languageCodeArgument = capability.Argument{Name: "language_code",
	Description: "Optional two-letter lowercase ISO 639-1 language code; empty or absent means the default text"}

func botProfileRisk() capability.Risk {
	return chatAdminRisk(capability.EffectUpdate, capability.IdempotencyIdempotent)
}

var profileGet = capability.Descriptor{
	ID:          Provider + ".profile.get",
	Version:     1,
	Title:       "Get the Telegram bot profile texts",
	Description: "Read the name, description, and short description of the bot for an optional language",
	Tags:        []string{"telegram", "profile", "read"},
	Risk: capability.Risk{
		Effect:          capability.EffectRead,
		Idempotency:     capability.IdempotencySafe,
		Confirmation:    capability.ConfirmationNone,
		OpenWorld:       true,
		DataSensitivity: dataSensitivity,
	},
	Provider: Provider,
	Group:    groupBot,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + languageCodeSchema +
		`},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},` +
		`"description":{"type":"string"},"short_description":{"type":"string"}},` +
		`"required":["name","description","short_description"],"additionalProperties":false}`),
	Arguments: []capability.Argument{languageCodeArgument},
	Fields: []capability.Field{
		{Name: "name", Description: "Name of the bot in the language"},
		{Name: "description", Description: "Description shown in an empty chat with the bot"},
		{Name: "short_description", Description: "Short description shown on the profile and when sharing the bot"},
	},
	Examples: []capability.Example{{Description: "Read the German texts", Arguments: json.RawMessage(`{"language_code":"de"}`)}},
}

func profileSetter(name, title, desc, field string, limit int, fieldDesc string) capability.Descriptor {
	return capability.Descriptor{
		ID:          Provider + ".profile." + name,
		Version:     1,
		Title:       title,
		Description: desc,
		Tags:        []string{"telegram", "profile", field},
		Risk:        botProfileRisk(),
		Provider:    Provider,
		Group:       groupBot,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"` + field + `":{"type":"string","maxLength":` +
			strconv.Itoa(limit) + `},` + languageCodeSchema + `},"required":["` + field + `"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"updated":{"type":"boolean"}},` +
			`"required":["updated"],"additionalProperties":false}`),
		Arguments: []capability.Argument{
			{Name: field, Required: true, Description: fieldDesc},
			languageCodeArgument,
		},
		Fields:   []capability.Field{{Name: "updated", Description: "True when Telegram accepted the change"}},
		Examples: []capability.Example{{Description: title, Arguments: json.RawMessage(`{"` + field + `":"Example"}`)}},
	}
}

var (
	profileSetName = profileSetter("set-name", "Set the Telegram bot name",
		"Change or remove the name of the bot for a language; requires the bot target", "name", maxBotNameRunes,
		"New name, up to 64 characters; an empty string removes the language-specific name")
	profileSetDescription = profileSetter("set-description", "Set the Telegram bot description",
		"Change or remove the description of the bot for a language; requires the bot target", "description",
		maxBotDescriptionRunes, "New description, up to 512 characters; an empty string removes the language-specific one")
	profileSetShortDescription = profileSetter("set-short-description", "Set the Telegram bot short description",
		"Change or remove the short description of the bot for a language; requires the bot target",
		"short_description", maxBotShortDescriptionRunes,
		"New short description, up to 120 characters; an empty string removes the language-specific one")
)

type profileOut struct {
	Name             string `json:"name"`
	Description      string `json:"description"`
	ShortDescription string `json:"short_description"`
}

func checkLanguage(op, code string) error {
	if !validLanguageCode(code) {
		return providerError(op, "language_code must be empty or two lowercase letters")
	}
	return nil
}

func invokeProfileGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get bot profile"
	var arguments struct {
		LanguageCode string `json:"language_code"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	client, err := openBotProfile(ctx, resolved, secrets, red, op, arguments.LanguageCode)
	if err != nil {
		return nil, err
	}
	return client.GetProfile(ctx, arguments.LanguageCode)
}

func openBotProfile(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	op, language string) (*Client, error) {
	if resolved == nil {
		return nil, providerError(op, "no connection was selected")
	}
	if err := checkLanguage(op, language); err != nil {
		return nil, err
	}
	if err := requireBotScope(resolved); err != nil {
		return nil, err
	}
	return Open(ctx, resolved, secrets, red)
}

func invokeProfileSet(op, method, field string, limit int) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
		red *redact.Redactor, raw json.RawMessage) (any, error) {
		var arguments map[string]*string
		if err := decodeStrict(raw, &arguments); err != nil {
			return nil, providerError(op, "the validated arguments could not be read")
		}
		value, language := arguments[field], arguments["language_code"]
		// A missing value is refused: it would silently remove the current text.
		if value == nil {
			return nil, providerError(op, field+" is required; use an empty string to remove it")
		}
		code := ""
		if language != nil {
			code = *language
		}
		if err := checkRunes(op, field, *value, 0, limit); err != nil {
			return nil, err
		}
		client, err := openBotProfile(ctx, resolved, secrets, red, op, code)
		if err != nil {
			return nil, err
		}
		return client.setProfileText(ctx, op, method, field, *value, code)
	}
}

// setProfileText performs exactly one request. The text is always sent, since an empty one removes the
// language-specific version; language_code is omitted when empty to address the default text.
func (c *Client) setProfileText(ctx context.Context, op, method, field, value, language string) (map[string]any, error) {
	payload := map[string]string{field: value}
	if language != "" {
		payload["language_code"] = language
	}
	if err := c.confirmed(ctx, spec{op: op, method: method, limit: defaultResponseBytes}, payload); err != nil {
		return nil, err
	}
	return map[string]any{"updated": true}, nil
}

// GetProfile reads the three profile texts with three requests; any failure fails the whole read, so no
// partial profile is shown.
func (c *Client) GetProfile(ctx context.Context, language string) (profileOut, error) {
	const op = "get bot profile"
	if err := checkLanguage(op, language); err != nil {
		return profileOut{}, err
	}
	payload := struct {
		LanguageCode string `json:"language_code,omitempty"`
	}{language}
	read := func(method string, dst any) error {
		raw, err := c.call(ctx, spec{op: op, method: method, limit: defaultResponseBytes, readOnly: true}, payload)
		if err != nil {
			return err
		}
		if json.Unmarshal(raw, dst) != nil {
			return invalidResponse(op)
		}
		return nil
	}
	var name struct {
		Name string `json:"name"`
	}
	var description struct {
		Description string `json:"description"`
	}
	var short struct {
		ShortDescription string `json:"short_description"`
	}
	for _, step := range []struct {
		method string
		dst    any
	}{{"getMyName", &name}, {"getMyDescription", &description}, {"getMyShortDescription", &short}} {
		if err := read(step.method, step.dst); err != nil {
			return profileOut{}, err
		}
	}
	return profileOut{
		Name:             capText(name.Name, maxBotNameRunes),
		Description:      capText(description.Description, maxBotDescriptionRunes),
		ShortDescription: capText(short.ShortDescription, maxBotShortDescriptionRunes),
	}, nil
}
