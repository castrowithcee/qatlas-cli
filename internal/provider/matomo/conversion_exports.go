package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	conversionExportsPlugin    = "AdvertisingConversionExport"
	conversionExportsSensitive = "matomo-conversion-exports"
	maxConversionExports       = 100
	maxExportID                = 999999999
	maxSecretDepth             = 6
)

var conversionExportSentOnce = sentOnceFor("conversion export", "matomo.conversion-exports.list with idExport")

func conversionExportsRequirement(tool string) requirement {
	return requirement{tool: tool, plugin: conversionExportsPlugin}
}

const (
	idExportSchema = `"idExport":{"type":"integer","minimum":1,"maximum":` + "999999999" + `}`
	exportProps    = `"idExport":{"type":"integer"},"idSite":{"type":"integer"},"name":{"type":"string"},` +
		`"type":{"type":"string"},"enabled":{"type":"boolean"}`
)

var conversionExportsList = capability.Descriptor{
	ID: Provider + ".conversion-exports.list", Version: 1, Title: "List Matomo conversion exports",
	Description: "Read the advertising conversion exports of the instance " +
		"(AdvertisingConversionExport.getConversionExports), or with idExport one export " +
		"(getConversionExport). Without idSite the exports of every website are asked for and only those whose " +
		"website lies inside the connection's targets are returned; with idSite only the exports of that " +
		"website. One export is bound to its website by the answer first: its website must lie inside the " +
		"targets and, when idSite is given, equal it, otherwise it is not found. At most 100 exports are " +
		"returned, with has_more when Matomo answered more. Only the fields idExport, idSite, name, type, and " +
		"enabled are returned: an export carries an access token and an export address with it, and neither " +
		"is ever returned. Names are untrusted. The answer of Matomo is not verified against a reference. Needs " +
		"the premium plugin AdvertisingConversionExport and, expected, a write right on at least one website",
	Tags: []string{"matomo", "conversion-exports", "advertising", "premium"}, Risk: readRisk(conversionExportsSensitive),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idExportSchema + `},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"exports":{"type":"array","items":{"type":"object",` +
		`"properties":{` + exportProps + `},"required":["idExport","idSite"],"additionalProperties":false}},` +
		`"has_more":{"type":"boolean"}},"required":["exports","has_more"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "idSite", Description: "Website ID from matomo.sites.list; must lie inside the connection's " +
			"targets; all websites inside the targets when omitted", Target: "site"},
		{Name: "idExport", Description: "ID of one export to read instead of the list"}},
	Fields: []capability.Field{
		{Name: "exports", Description: "The exports, at most 100; one entry with idExport"},
		{Name: "exports[].idExport", Description: "Export ID, used as idExport by matomo.conversion-exports.delete"},
		{Name: "exports[].idSite", Description: "The website the export belongs to"},
		{Name: "exports[].name", Description: "Export name, untrusted; absent when Matomo gives none"},
		{Name: "exports[].type", Description: "Kind of the export as Matomo names it, untrusted; absent when none"},
		{Name: "exports[].enabled", Description: "Whether the export is switched on; absent when Matomo gives none"},
		{Name: "has_more", Description: "True when Matomo answered more exports than returned"}},
	Examples: []capability.Example{
		{Description: "The exports of website 1", Arguments: json.RawMessage(`{"idSite":1}`)},
		{Description: "Export 5", Arguments: json.RawMessage(`{"idExport":5}`)}},
}

var conversionExportsDelete = capability.Descriptor{
	ID: Provider + ".conversion-exports.delete", Version: 1, Title: "Delete a Matomo conversion export",
	Description: "Delete an advertising conversion export of a website " +
		"(AdvertisingConversionExport.deleteConversionExport); Qatlas offers no way to restore or recreate it. " +
		"The export is read once first and must belong to the website, otherwise it is not found and nothing is " +
		"deleted. Needs the premium plugin AdvertisingConversionExport and, expected, a write right on the " +
		"website; idempotent in effect" + conversionExportSentOnce + "; offered only where a connection's tools " +
		"list names it",
	Tags: []string{"matomo", "conversion-exports", "advertising", "delete"}, Provider: Provider,
	RequiresToolAllowList: true,
	Risk: capability.Risk{Effect: capability.EffectDelete, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: conversionExportsSensitive},
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idExportSchema + `},` +
		`"required":["idExport"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idExport":{"type":"integer"},"deleted":{"type":"boolean"}},` +
		`"required":["idSite","idExport","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(),
		{Name: "idExport", Required: true, Description: "ID of the export to delete, from matomo.conversion-exports.list"}},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the export belonged to"},
		{Name: "idExport", Description: "The export that was deleted"},
		{Name: "deleted", Description: "True once Matomo accepted the deletion"}},
	Examples: []capability.Example{{Description: "Delete export 5 of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"idExport":5}`)}},
}

// ConversionExport is the allowlisted part of an export. Nothing else of the answer is ever passed on: the
// export holds an access token and an address that carries it.
type ConversionExport struct {
	IDExport int    `json:"idExport"`
	IDSite   int    `json:"idSite"`
	Name     string `json:"name,omitempty"`
	Type     string `json:"type,omitempty"`
	Enabled  *bool  `json:"enabled,omitempty"`
}

// ConversionExportsResult is the answer of conversion-exports.list.
type ConversionExportsResult struct {
	Exports []ConversionExport `json:"exports"`
	HasMore bool               `json:"has_more"`
}

// ConversionExportDeleted is the answer of conversion-exports.delete.
type ConversionExportDeleted struct {
	IDSite   int  `json:"idSite"`
	IDExport int  `json:"idExport"`
	Deleted  bool `json:"deleted"`
}

func checkExportID(id *int) error {
	if id == nil || *id < 1 || *id > maxExportID {
		return invalidArgument("idExport must be a positive integer export ID from matomo.conversion-exports.list")
	}
	return nil
}

func exportNotFound(op string) error {
	return &provider.Error{Class: provider.ClassNotFound, Op: op,
		Message: "Matomo has no such conversion export for this website inside the targets of this connection"}
}

// secretName reports whether a member name suggests a value that must not leave: tokens, keys, and the like.
func secretName(name string) bool {
	name = strings.ToLower(name)
	for _, part := range []string{"token", "secret", "password", "passwd", "apikey", "api_key", "auth", "signature",
		"credential"} {
		if strings.Contains(name, part) {
			return true
		}
	}
	return false
}

// addressName reports whether a member name suggests an address, which may carry a token.
func addressName(name string) bool {
	name = strings.ToLower(name)
	for _, part := range []string{"url", "link", "href", "uri", "endpoint"} {
		if strings.Contains(name, part) {
			return true
		}
	}
	return false
}

// opaqueValue reports whether a query value or path segment looks like a token: long, without spaces.
func opaqueValue(value string) bool {
	return len(value) >= 16 && !strings.ContainsAny(value, " \t")
}

// collectSecrets walks the answer for token values: strings under token-like names, addresses, and the
// token-like parameters inside any address.
func collectSecrets(name string, value any, depth int, found *[]string) {
	if depth > maxSecretDepth {
		return
	}
	switch v := value.(type) {
	case map[string]any:
		for key, member := range v {
			collectSecrets(key, member, depth+1, found)
		}
	case []any:
		for _, member := range v {
			collectSecrets(name, member, depth+1, found)
		}
	case string:
		if v == "" {
			return
		}
		isAddress := addressName(name) || strings.Contains(v, "://")
		if secretName(name) || isAddress {
			*found = append(*found, v)
		}
		if isAddress {
			if parsed, err := url.Parse(v); err == nil {
				for param, values := range parsed.Query() {
					for _, value := range values {
						if secretName(param) || opaqueValue(value) {
							*found = append(*found, value)
						}
					}
				}
				for _, segment := range strings.Split(parsed.Path, "/") {
					if opaqueValue(segment) {
						*found = append(*found, segment)
					}
				}
			}
		}
	}
}

// registerExportSecrets hands every token-like value of an answer to the redactor before the answer is read any
// further, and returns them so that no allowlisted field carries one.
func registerExportSecrets(red *redact.Redactor, answer json.RawMessage) []string {
	var parsed any
	if json.Unmarshal(answer, &parsed) != nil {
		return nil
	}
	var found []string
	collectSecrets("", parsed, 0, &found)
	if red != nil && len(found) > 0 {
		red.Add(found...)
	}
	return found
}

func cleanExportText(l *loose, secrets []string) string {
	text := l.str(maxStringLength)
	for _, secretValue := range secrets {
		if len(secretValue) >= 4 && strings.Contains(text, secretValue) {
			return ""
		}
	}
	return text
}

// exportFrom reads the allowlisted fields of one row. The member names follow the Matomo core convention (lower
// case idsite) and are matched without regard to case, as the reference gives no example. A row without a
// readable export ID and website is not an export this tool can attribute.
func exportFrom(row map[string]json.RawMessage, secrets []string) (ConversionExport, bool) {
	members := make(map[string]json.RawMessage, len(row))
	for key, value := range row {
		members[strings.ToLower(key)] = value
	}
	number := func(names ...string) int {
		for _, name := range names {
			var l *loose
			if json.Unmarshal(members[name], &l) == nil && l.number() != nil {
				return *l.number()
			}
		}
		return 0
	}
	export := ConversionExport{IDExport: number("idexport", "id"), IDSite: number("idsite")}
	if export.IDExport < 1 || export.IDSite < 1 {
		return ConversionExport{}, false
	}
	var l *loose
	if json.Unmarshal(members["name"], &l) == nil {
		export.Name = cleanExportText(l, secrets)
	}
	l = nil
	if json.Unmarshal(members["type"], &l) == nil {
		export.Type = cleanExportText(l, secrets)
	}
	l = nil
	if json.Unmarshal(members["enabled"], &l) == nil {
		export.Enabled = l.flag()
	}
	return export, true
}

// fetchConversionExport is the one read that both proves an export belongs to the website and delivers it. The
// export ID counts across websites, so the website of the answer must lie inside the targets and equal the
// website asked for when there is one (site > 0).
func (c *Client) fetchConversionExport(ctx context.Context, op string, req requirement, red *redact.Redactor,
	bound scope, site, id int) (ConversionExport, error) {
	var answer json.RawMessage
	if err := c.call(ctx, op, req, "AdvertisingConversionExport.getConversionExport",
		url.Values{"idExport": {strconv.Itoa(id)}}, &answer); err != nil {
		return ConversionExport{}, err
	}
	secrets := registerExportSecrets(red, answer)
	if emptyAnswer(answer) {
		return ConversionExport{}, exportNotFound(op)
	}
	trimmed := bytes.TrimSpace(answer)
	var row map[string]json.RawMessage
	switch {
	case bytes.HasPrefix(trimmed, []byte("{")):
		if json.Unmarshal(trimmed, &row) != nil {
			return ConversionExport{}, provider.InvalidResponse(op, "Matomo returned an invalid conversion export")
		}
	case bytes.HasPrefix(trimmed, []byte("[")):
		var rows []map[string]json.RawMessage
		if json.Unmarshal(trimmed, &rows) != nil || len(rows) != 1 {
			return ConversionExport{}, provider.InvalidResponse(op, "Matomo returned an invalid conversion export")
		}
		row = rows[0]
	default:
		return ConversionExport{}, provider.InvalidResponse(op, "Matomo returned an invalid conversion export")
	}
	export, valid := exportFrom(row, secrets)
	if !valid {
		return ConversionExport{}, provider.InvalidResponse(op, "Matomo returned an invalid conversion export")
	}
	if export.IDExport != id {
		return ConversionExport{}, provider.InvalidResponse(op, "Matomo returned another conversion export than asked for")
	}
	if !bound.allows(export.IDSite) || site > 0 && export.IDSite != site {
		return ConversionExport{}, exportNotFound(op)
	}
	return export, nil
}

func invokeConversionExportsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "read conversion exports"
	var args struct {
		IDSite   *int `json:"idSite"`
		IDExport *int `json:"idExport"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	site := 0
	if args.IDSite != nil {
		if site, err = selectSite(resolved, args.IDSite); err != nil {
			return nil, err
		}
	} else if !bound.reachesSites() {
		return nil, invalidArgument("this connection is bound to no website")
	}
	if args.IDExport != nil {
		if err := checkExportID(args.IDExport); err != nil {
			return nil, err
		}
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := conversionExportsRequirement(conversionExportsList.ID)
	if args.IDExport != nil {
		export, err := client.fetchConversionExport(ctx, op, req, red, bound, site, *args.IDExport)
		if err != nil {
			return nil, err
		}
		return &ConversionExportsResult{Exports: []ConversionExport{export}}, nil
	}
	params := url.Values{}
	if site > 0 {
		params.Set("idSite", strconv.Itoa(site))
	}
	var answer json.RawMessage
	if err := client.call(ctx, op, req, "AdvertisingConversionExport.getConversionExports", params, &answer); err != nil {
		return nil, err
	}
	found := registerExportSecrets(red, answer)
	rows, valid := readRows[map[string]json.RawMessage](answer)
	if !valid {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid conversion export list")
	}
	result := &ConversionExportsResult{Exports: []ConversionExport{}}
	for _, row := range rows {
		export, readable := exportFrom(row, found)
		// A row whose website cannot be read, or lies outside the targets, is never shown.
		if !readable || !bound.allows(export.IDSite) || site > 0 && export.IDSite != site {
			continue
		}
		if len(result.Exports) == maxConversionExports {
			result.HasMore = true
			break
		}
		result.Exports = append(result.Exports, export)
	}
	return result, nil
}

func invokeConversionExportsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete conversion export"
	var args struct {
		IDSite   *int `json:"idSite"`
		IDExport *int `json:"idExport"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := checkExportID(args.IDExport); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := conversionExportsRequirement(conversionExportsDelete.ID)
	// An ID alone proves nothing for the website: one read binds the export to it before anything is deleted.
	if _, err := client.fetchConversionExport(ctx, op, req, red, bound, site, *args.IDExport); err != nil {
		return nil, err
	}
	params := url.Values{"idExport": {strconv.Itoa(*args.IDExport)}, "idSite": {strconv.Itoa(site)}}
	var answer json.RawMessage
	if err := client.change(ctx, op, req, "AdvertisingConversionExport.deleteConversionExport", params,
		conversionExportSentOnce, &answer); err != nil {
		return nil, err
	}
	var confirmed bool
	if !isSuccessAnswer(answer) && (scalar(answer, &confirmed) != nil || !confirmed) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the deletion", conversionExportSentOnce)
	}
	return &ConversionExportDeleted{IDSite: site, IDExport: *args.IDExport, Deleted: true}, nil
}

func conversionExportsOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: conversionExportsList, Handler: capability.Handler(invokeConversionExportsList)},
		{Descriptor: conversionExportsDelete, Handler: capability.Handler(invokeConversionExportsDelete)}}
}
