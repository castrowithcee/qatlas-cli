package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// previewEnvironment is reserved by the plugin for its preview mode: publishContainerVersion only accepts the
// environments of the instance setting, which never holds it.
const previewEnvironment = "preview"

var publishSentOnce = sentOnceFor("releases",
	"matomo.tag-manager-versions.get or matomo.tag-manager-containers.get")

// The allowlists of a publication. Any plugin can add tag, trigger, and variable templates with code of their
// own (TagManager.addTags, addTriggers, addVariables), so a type outside these lists cannot be checked and is
// refused. The variable and trigger lists are the plugin's own templates without CustomJsFunction and
// CustomRequestProcessing, the two that run code from their parameters.
var (
	publishableTagTypes      = map[string]bool{"Matomo": true, "ThemeColor": true}
	publishableTriggerTypes  = setOf("AllDownloadsClick", "AllElementsClick", "AllLinksClick", "CustomEvent", "DomReady", "ElementVisibility", "FormSubmit", "Fullscreen", "HistoryChange", "JavaScriptError", "PageView", "ScrollReach", "Timer", "UserInteraction", "WindowLeave", "WindowLoaded", "WindowUnload")
	publishableVariableTypes = setOf("ClickDataAttribute", "ClickHtmlAttribute", "Constant", "Cookie", "DataLayer", "DomElement", "EtrackerConfiguration", "JavaScript", "MatomoConfiguration", "MetaContent", "ReferrerUrl", "TimeSinceLoad", "Url", "UrlParameter")
	// codeTypes are the plugin's templates that run code from their parameters; a refusal may name them.
	codeTagTypes      = setOf("CustomHtml", "CustomImage")
	codeVariableTypes = setOf("CustomJsFunction", "CustomRequestProcessing")
	// The tracker files a Matomo configuration may load and send to below its address; custom names are refused.
	trackerScripts   = setOf("matomo.js", "piwik.js", "js/", "js/tracker.php")
	trackerEndpoints = setOf("matomo.php", "piwik.php", "js/", "js/tracker.php")
)

func setOf(values ...string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

var tagManagerVersionsPublish = capability.Descriptor{
	ID: Provider + ".tag-manager-versions.publish", Version: 1, Title: "Publish a Matomo Tag Manager container version",
	Description: "Release one saved version of a Tag Manager container to an environment: Matomo rewrites the " +
		"container script of that environment, and the visitors of every website that embeds it run the new script " +
		"on their next page load. Before the one write request the export of the version is read and checked, and " +
		"nothing is published for the draft, an export that cannot be checked completely, a tag type other than " +
		"Matomo and ThemeColor, a variable or trigger type outside the plugin's own ones without code, or a Matomo " +
		"configuration that loads from or sends to another address than this connection's base URL, sets an API " +
		"URL or request processing, or tracks another website. Storing versions does not make them immutable: " +
		"another user with write access can change the version between that check and the release, and the " +
		"check is no protection against it. After the release the export of the version is read once more and " +
		"compared with the checked one; a difference or a failed re-read is reported as an error that says the " +
		"version was published, and the re-read only makes a change visible, it neither prevents it nor undoes " +
		"the release. The container and the version must belong to the website; needs the plugin TagManager, a write " +
		"right, and for live the right to publish there, for another environment that right or the right to use " +
		"custom templates; idempotent in effect, sent once and never repeated; offered only where a connection's " +
		"tools list names it",
	Tags: []string{"matomo", "tag-manager", "versions", "publish"},
	Risk: tagManagerChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + containerIDSchema + `,` +
		versionIDSchema + `,` + environmentSchema + `},"required":["idContainer","idContainerVersion","environment"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idContainer":{"type":"string"},"idContainerVersion":{"type":"integer"},"environment":{"type":"string"},` +
		`"idContainerRelease":{"type":"integer"},"published":{"type":"boolean"},"verified":{"type":"boolean"}},` +
		`"required":["idSite","idContainer","idContainerVersion","environment","idContainerRelease","published",` +
		`"verified"],"additionalProperties":false}`),
	Arguments: versionChangeArguments(
		capability.Argument{Name: "idContainerVersion", Required: true, Description: "Version ID from " +
			"matomo.tag-manager-versions.list; the draft is refused"},
		capability.Argument{Name: "environment", Required: true, Description: "Environment ID from " +
			"matomo.tag-manager-catalog.list kind environments or publishable-environments, such as live; preview is " +
			"refused"}),
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the container belongs to"},
		{Name: "idContainer", Description: "The container the version belongs to"},
		{Name: "idContainerVersion", Description: "ID of the published version"},
		{Name: "environment", Description: "The environment the version was released to"},
		{Name: "idContainerRelease", Description: "ID of the new release"},
		{Name: "published", Description: "True once Matomo released the version"},
		{Name: "verified", Description: "True when the export read after the release equals the one checked before it"},
	},
	Examples: []capability.Example{{Description: "Publish version 7 of container 6OMh6taM of website 1 to live",
		Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7,"environment":"live"}`)}},
}

// VersionPublished is the answer of tag-manager-versions.publish.
type VersionPublished struct {
	IDSite             int    `json:"idSite"`
	IDContainer        string `json:"idContainer"`
	IDContainerVersion int    `json:"idContainerVersion"`
	Environment        string `json:"environment"`
	IDContainerRelease int    `json:"idContainerRelease"`
	Published          bool   `json:"published"`
	Verified           bool   `json:"verified"`
}

func publishRefused(reason string) error {
	return &provider.InvalidRequestError{Message: "the version was not published: " + reason}
}

// checkPublishable decides from the export whether a version may reach visitors. The refusals quote no provider
// text; a type is named only when it is one of the plugin's own code templates.
func checkPublishable(export *VersionExportResult, origin string, site int) error {
	if export.Truncated {
		return publishRefused("its export exceeds the limits of the check, so it cannot be checked completely")
	}
	if export.inexactTypes {
		return publishRefused("an entry names its type in a form that cannot be checked")
	}
	if export.Revision == nil || export.Version == nil || export.Version.Revision == nil ||
		*export.Version.Revision != *export.Revision {
		return publishRefused("Matomo named no revision for it, so it cannot be checked")
	}
	if *export.Revision == 0 {
		return publishRefused("it is the draft, which can change between the check and the release; save it with " +
			"matomo.tag-manager-versions.create and publish that version")
	}
	for _, tag := range export.Tags {
		if codeTagTypes[tag.Type] {
			return publishRefused("it holds a tag of type " + tag.Type + ", which runs code of its own at visitors")
		}
		if !publishableTagTypes[tag.Type] {
			return publishRefused("it holds a tag type other than Matomo and ThemeColor")
		}
	}
	for _, trigger := range export.Triggers {
		if !publishableTriggerTypes[trigger.Type] {
			return publishRefused("it holds a trigger type outside the plugin's own ones")
		}
	}
	for _, variable := range export.Variables {
		if codeVariableTypes[variable.Type] {
			return publishRefused("it holds a variable of type " + variable.Type + ", which runs code of its own at visitors")
		}
		if !publishableVariableTypes[variable.Type] {
			return publishRefused("it holds a variable type outside the plugin's own ones without code")
		}
		if variable.Type == "MatomoConfiguration" {
			if !trackerConfigAllowed(variable.Parameters, origin, site) {
				return publishRefused("it holds a Matomo configuration whose address is not this connection's base URL, " +
					"that tracks another website than idSite, or that cannot be checked")
			}
			// The rules of the variable tools apply as well: no API URL or request processing, plain endpoints.
			params, _ := variable.Parameters.(map[string]any)
			if checkConfigurationVariable(origin, site, params) != nil {
				return publishRefused("it holds a Matomo configuration with an API URL or request processing set, or " +
					"with an endpoint that is no plain path")
			}
		}
	}
	for _, tag := range export.Tags {
		if tag.Type == "Matomo" && !trackerConfigOf(tag.Parameters, export.Variables) {
			return publishRefused("it holds a Matomo tag whose configuration is not exactly one Matomo configuration " +
				"variable of the version")
		}
	}
	return nil
}

// trackerConfigOf reports whether a Matomo tag names exactly one variable of type MatomoConfiguration. Matomo
// resolves the reference by name in the database, whose collation may ignore case, so a name that matches more
// than one variable that way is refused.
func trackerConfigOf(parameters any, variables []ExportEntry) bool {
	params, _ := parameters.(map[string]any)
	reference, _ := params["matomoConfig"].(string)
	reference = strings.TrimSpace(reference)
	name, opened := strings.CutPrefix(reference, "{{")
	name, closed := strings.CutSuffix(name, "}}")
	if !opened || !closed || strings.Contains(name, "{") || strings.Contains(name, "}") {
		return false
	}
	name = strings.TrimSpace(name)
	matches := 0
	allowed := false
	for _, variable := range variables {
		if name != "" && strings.EqualFold(strings.TrimSpace(variable.Name), name) {
			matches++
			allowed = variable.Type == "MatomoConfiguration"
		}
	}
	return matches == 1 && allowed
}

// trackerConfigAllowed checks every member of a Matomo configuration that decides where the tracker is loaded
// from and sent to, and the website it tracks. A value that refers to another variable cannot be checked.
func trackerConfigAllowed(parameters any, origin string, site int) bool {
	params, _ := parameters.(map[string]any)
	address, _ := params["matomoUrl"].(string)
	if !sameAddress(address, origin) || scalarText(params["idSite"]) != strconv.Itoa(site) {
		return false
	}
	for name, allowed := range map[string]map[string]bool{"jsEndpoint": trackerScripts, "trackingEndpoint": trackerEndpoints} {
		if value, present := params[name]; present && value != nil {
			text, isText := value.(string)
			if !isText || !allowed[strings.TrimSpace(text)] {
				return false
			}
		}
	}
	// customRequestProcessing names a variable whose code rewrites every tracking request.
	if value, present := params["customRequestProcessing"]; present && value != nil {
		if text, isText := value.(string); !isText || strings.TrimSpace(text) != "" {
			return false
		}
	}
	return true
}

// sameAddress compares the address of a Matomo configuration with the base URL of the connection: the same https
// host and installation path, with or without a closing slash. A protocol-relative address is refused, as the
// visitor's page would choose the scheme.
func sameAddress(raw, origin string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.Contains(raw, "{{") || strings.ContainsAny(raw, "\\ \t\r\n?#@") {
		return false
	}
	parsed, err := url.Parse(raw)
	base, baseErr := url.Parse(origin)
	if err != nil || baseErr != nil || parsed.Scheme != "https" || parsed.Opaque != "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return false
	}
	return strings.EqualFold(parsed.Host, base.Host) &&
		strings.TrimSuffix(parsed.EscapedPath(), "/") == strings.TrimSuffix(base.EscapedPath(), "/")
}

// scalarText is a parameter that is a text or a number, as text; anything else is empty.
func scalarText(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	}
	return ""
}

// The plugin binds the version to the website and the container (checkContainerVersionExists) before it releases
// anything; the export that is checked first is bound the same way.
func invokeTagManagerVersionsPublish(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "publish Tag Manager container version"
	var args struct {
		IDSite             *int    `json:"idSite"`
		IDContainer        *string `json:"idContainer"`
		IDContainerVersion *int    `json:"idContainerVersion"`
		Environment        string  `json:"environment"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	containerID, err := containerIDOf(args.IDContainer)
	if err != nil {
		return nil, err
	}
	versionID, err := versionIDOf(args.IDContainerVersion, true)
	if err != nil {
		return nil, err
	}
	if !environmentPattern.MatchString(args.Environment) {
		return nil, invalidArgument("environment must be an environment ID of 2 to 40 letters, digits, and underscores")
	}
	if strings.EqualFold(args.Environment, previewEnvironment) {
		return nil, invalidArgument("environment preview is reserved for Matomo's preview mode and cannot be published to")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := tagManagerRequirement(tagManagerVersionsPublish.ID)
	export, err := client.exportVersion(ctx, op, req, id, containerID, versionID)
	if err != nil {
		return nil, err
	}
	if err := checkPublishable(export, client.origin, id); err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(id)}, "idContainer": {containerID},
		"idContainerVersion": {strconv.Itoa(versionID)}, "environment": {args.Environment}}
	var answer json.RawMessage
	if err := client.change(ctx, op, req, "TagManager.publishContainerVersion", params, publishSentOnce, &answer); err != nil {
		return nil, err
	}
	var release int
	if scalar(answer, &release) != nil || release < 1 {
		return nil, unclearChange(op, "Matomo returned no usable ID for the release", publishSentOnce)
	}
	// The one re-read; neither a failure nor a difference repeats the release or the read.
	after, err := client.exportVersion(ctx, op, req, id, containerID, versionID)
	if err != nil || after.Truncated || after.inexactTypes {
		return nil, provider.InvalidResponse(op, "the version was published, but the check afterwards failed; "+
			"review the release with matomo.tag-manager-versions.export")
	}
	if !reflect.DeepEqual(export, after) {
		return nil, provider.InvalidResponse(op, "the version was published, but it changed since the check; "+
			"review the release with matomo.tag-manager-versions.export")
	}
	return &VersionPublished{IDSite: id, IDContainer: containerID, IDContainerVersion: versionID,
		Environment: args.Environment, IDContainerRelease: release, Published: true, Verified: true}, nil
}
