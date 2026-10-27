package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	tagTypeMatomo     = "Matomo"
	tagTypeThemeColor = "ThemeColor"
	maxTagTriggers    = 50
	// matomoConfigParameter names the parameter of a Matomo tag that refers to the variable holding the tracking
	// address.
	matomoConfigParameter = "matomoConfig"
	// configVariableType is the variable type that holds matomoUrl and idSite.
	configVariableType = "MatomoConfiguration"
)

var (
	// allowedTagTypes is the fixed list of tag types the tools accept; any other type can send data to a third party.
	allowedTagTypes = map[string]bool{tagTypeMatomo: true, tagTypeThemeColor: true}
	tagFireLimits   = map[string]bool{"unlimited": true, "once_page": true, "once_24hours": true, "once_lifetime": true}
	tagStatuses     = map[string]bool{"active": true, "paused": true}
	tagDatePattern  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}( \d{2}:\d{2}:\d{2})?$`)
	configReference = regexp.MustCompile(`^\{\{([^{}]{1,255})\}\}$`)
)

const (
	tagTypeSchema = `"type":{"type":"string","enum":["Matomo","ThemeColor"]}`
	tagListSchema = `{"type":"array","maxItems":50,"items":{"type":"integer","minimum":1,"maximum":2147483647}}`
	tagFireIDs    = `"fireTriggerIds":` + `{"type":"array","minItems":1,"maxItems":50,"items":` +
		`{"type":"integer","minimum":1,"maximum":2147483647}}`
	tagBlockIDs = `"blockTriggerIds":` + tagListSchema
	tagLimit    = `"fireLimit":{"type":"string","enum":["unlimited","once_page","once_24hours","once_lifetime"]}`
	tagDelay    = `"fireDelay":{"type":"integer","minimum":0,"maximum":2147483647}`
	tagPriority = `"priority":{"type":"integer","minimum":0,"maximum":2147483647}`
	tagDates    = `"startDate":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}( \\d{2}:\\d{2}:\\d{2})?$","maxLength":19},` +
		`"endDate":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}( \\d{2}:\\d{2}:\\d{2})?$","maxLength":19}`
	tagStatus      = `"status":{"type":"string","enum":["active","paused"]}`
	tagSettingsDoc = "Tag settings: fireTriggerIds (1 to 50 trigger IDs of the draft that fire the tag), " +
		"blockTriggerIds (up to 50 that block it), fireLimit (unlimited, once_page, once_24hours, or once_lifetime), " +
		"fireDelay (milliseconds), priority (lower fires first), startDate and endDate (UTC, YYYY-MM-DD or " +
		"YYYY-MM-DD HH:MM:SS)"
)

const tagGuard = "Only tags of the types Matomo and ThemeColor are offered; every other type can send data to a " +
	"third party and stays with the Matomo user interface. A Matomo tag must refer in matomoConfig, as {{Name}}, " +
	"to a variable of the draft of the type MatomoConfiguration that meets the rule of the variable tools (matomoUrl " +
	"and idSite name this connection's Matomo and the selected website, setApiUrl and customRequestProcessing are " +
	"empty, endpoints are plain paths); otherwise the tool refuses before it changes anything. " +
	"Only the draft changes: the tool reads the container first, takes the draft from it, and never accepts a " +
	"version, so nothing is published"

func tagParametersArgument(required bool) capability.Argument {
	return capability.Argument{Name: "parameters", Required: required, Description: "Parameter values keyed by " +
		"the parameter names of the tag type in matomo.tag-manager-catalog.list kind tag-types: text, number, true or " +
		"false, or a list of such values or of rows (objects of such values), at most 100 parameters, 100 list " +
		"members, and 50 cells per row; texts up to 16384 bytes and all texts together up to 262144 bytes. A Matomo " +
		"tag needs matomoConfig, a reference such as {{Main configuration}}. Matomo keeps only the parameters the " +
		"type defines and sets the others to their defaults"}
}

func tagChangeArguments(extra ...capability.Argument) []capability.Argument {
	return triggerChangeArguments(extra...)
}

var tagManagerTagsAdd = capability.Descriptor{
	ID: Provider + ".tag-manager-tags.add", Version: 1, Title: "Add a Matomo Tag Manager tag",
	Description: "Create a tag in the draft of a Tag Manager container (addContainerTag). " + tagGuard + ". The " +
		"name must be new among the tags of the draft; the trigger IDs must belong to the draft; needs the plugin " +
		"TagManager and a write right; not idempotent, since a repeated call with another name creates a second " +
		"tag" + tagSentOnce,
	Tags: []string{"matomo", "tag-manager", "tags", "create"},
	Risk: tagManagerChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + containerIDSchema + `,` +
		tagTypeSchema + `,` + triggerNameSchema + `,` + triggerParametersSchema + `,` + tagFireIDs + `,` + tagBlockIDs +
		`,` + tagLimit + `,` + tagDelay + `,` + tagPriority + `,` + tagDates + `,` + triggerDescSchema + `,` + tagStatus +
		`},"required":["idContainer","type","name","fireTriggerIds"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + versionScopeProperties + `,"idTag":{"type":"integer"},` +
		`"created":{"type":"boolean"}},"required":["idSite","idContainer","idContainerVersion","idTag","created"],` +
		`"additionalProperties":false}`),
	Arguments: tagChangeArguments(
		capability.Argument{Name: "type", Required: true, Description: "Tag template ID, Matomo or ThemeColor; it " +
			"cannot be changed later"},
		capability.Argument{Name: "name", Required: true, Description: "Name of the tag, 1 to 255 characters"},
		tagParametersArgument(false),
		capability.Argument{Name: "fireTriggerIds", Required: true, Description: tagSettingsDoc},
		capability.Argument{Name: "blockTriggerIds", Description: "Trigger IDs that block the tag; none when omitted"},
		capability.Argument{Name: "fireLimit", Description: "unlimited when omitted"},
		capability.Argument{Name: "fireDelay", Description: "Delay in milliseconds; 0 when omitted"},
		capability.Argument{Name: "priority", Description: "Priority; Matomo's default when omitted"},
		capability.Argument{Name: "startDate", Description: "Start of the period in which the tag fires; none when omitted"},
		capability.Argument{Name: "endDate", Description: "End of the period in which the tag fires; none when omitted"},
		capability.Argument{Name: "description", Description: "Description, up to 1000 characters; empty when omitted"},
		capability.Argument{Name: "status", Description: "active (default) or paused"}),
	Fields: append(append([]capability.Field{}, tagDraftFields...),
		capability.Field{Name: "idTag", Description: "ID of the new tag"},
		capability.Field{Name: "created", Description: "True once Matomo created the tag"}),
	Examples: []capability.Example{{Description: "Add a theme color tag that fires on trigger 3 of container 6OMh6taM of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM","type":"ThemeColor","name":"Theme color",` +
			`"parameters":{"themeColor":"#336699"},"fireTriggerIds":[3]}`)}},
}

var tagManagerTagsUpdate = capability.Descriptor{
	ID: Provider + ".tag-manager-tags.update", Version: 1, Title: "Update a Matomo Tag Manager tag",
	Description: "Replace name, parameters, trigger IDs, fire settings, and description of a tag in the draft of a " +
		"Tag Manager container (updateContainerTag); the type stays. Everything is replaced as given, so parameters " +
		"that are left out go back to their defaults; read the tag first with matomo.tag-manager-tags.get. The tool " +
		"reads the stored tag first and refuses one of another type than Matomo or ThemeColor. " + tagGuard + ". " +
		"The name must be new among the tags of the draft; needs the plugin TagManager and a write right; " +
		"idempotent in effect" + tagSentOnce,
	Tags: []string{"matomo", "tag-manager", "tags", "update"},
	Risk: tagManagerChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + containerIDSchema + `,` +
		tagIDSchema + `,` + triggerNameSchema + `,` + triggerParametersSchema + `,` + tagFireIDs + `,` + tagBlockIDs +
		`,` + tagLimit + `,` + tagDelay + `,` + tagPriority + `,` + tagDates + `,` + triggerDescSchema +
		`},"required":["idContainer","idTag","name","parameters","fireTriggerIds","blockTriggerIds","fireLimit",` +
		`"fireDelay","priority","description"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + versionScopeProperties + `,"idTag":{"type":"integer"},` +
		`"updated":{"type":"boolean"}},"required":["idSite","idContainer","idContainerVersion","idTag","updated"],` +
		`"additionalProperties":false}`),
	Arguments: tagChangeArguments(
		capability.Argument{Name: "idTag", Required: true, Description: "Tag ID from matomo.tag-manager-tags.list of the draft"},
		capability.Argument{Name: "name", Required: true, Description: "New name, 1 to 255 characters"},
		tagParametersArgument(true),
		capability.Argument{Name: "fireTriggerIds", Required: true, Description: tagSettingsDoc},
		capability.Argument{Name: "blockTriggerIds", Required: true, Description: "Trigger IDs that block the tag; an empty list clears them"},
		capability.Argument{Name: "fireLimit", Required: true, Description: "unlimited, once_page, once_24hours, or once_lifetime"},
		capability.Argument{Name: "fireDelay", Required: true, Description: "Delay in milliseconds"},
		capability.Argument{Name: "priority", Required: true, Description: "Priority; lower fires first"},
		capability.Argument{Name: "startDate", Description: "Start of the period in which the tag fires; none when omitted"},
		capability.Argument{Name: "endDate", Description: "End of the period in which the tag fires; none when omitted"},
		capability.Argument{Name: "description", Required: true, Description: "New description, up to 1000 characters; " +
			"an empty text clears it"}),
	Fields: append(append([]capability.Field{}, tagDraftFields...),
		capability.Field{Name: "idTag", Description: "ID of the tag that was sent"},
		capability.Field{Name: "updated", Description: "True once Matomo accepted the update"}),
	Examples: []capability.Example{{Description: "Rename theme color tag 5 of the draft of container 6OMh6taM of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM","idTag":5,"name":"Theme color","parameters":` +
			`{"themeColor":"#336699"},"fireTriggerIds":[3],"blockTriggerIds":[],"fireLimit":"unlimited","fireDelay":0,` +
			`"priority":999,"description":""}`)}},
}

var tagManagerTagsResume = capability.Descriptor{
	ID: Provider + ".tag-manager-tags.resume", Version: 1, Title: "Resume a Matomo Tag Manager tag",
	Description: "Activate a paused tag in the draft of a Tag Manager container again (resumeContainerTag). The tool " +
		"reads the stored tag first and refuses one of another type than Matomo or ThemeColor; for a Matomo tag the " +
		"stored matomoConfig must meet the rule below. " + tagGuard + ". Matomo ignores an ID the draft does not hold " +
		"without changing anything; needs the plugin TagManager and a write right; idempotent in effect" + tagSentOnce,
	Tags: []string{"matomo", "tag-manager", "tags", "resume"},
	Risk: tagManagerChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + containerIDSchema + `,` +
		tagIDSchema + `},"required":["idContainer","idTag"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + versionScopeProperties + `,"idTag":{"type":"integer"},` +
		`"resumed":{"type":"boolean"}},"required":["idSite","idContainer","idContainerVersion","idTag","resumed"],` +
		`"additionalProperties":false}`),
	Arguments: tagChangeArguments(
		capability.Argument{Name: "idTag", Required: true, Description: "Tag ID from matomo.tag-manager-tags.list of the draft"}),
	Fields: append(append([]capability.Field{}, tagDraftFields...),
		capability.Field{Name: "idTag", Description: "ID of the tag that was sent"},
		capability.Field{Name: "resumed", Description: "True once Matomo accepted the request"}),
	Examples: []capability.Example{{Description: "Resume tag 5 of the draft of container 6OMh6taM of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM","idTag":5}`)}},
}

// TagCreated is the answer of tag-manager-tags.add.
type TagCreated struct {
	IDSite             int    `json:"idSite"`
	IDContainer        string `json:"idContainer"`
	IDContainerVersion int    `json:"idContainerVersion"`
	IDTag              int    `json:"idTag"`
	Created            bool   `json:"created"`
}

// TagUpdated is the answer of tag-manager-tags.update.
type TagUpdated struct {
	IDSite             int    `json:"idSite"`
	IDContainer        string `json:"idContainer"`
	IDContainerVersion int    `json:"idContainerVersion"`
	IDTag              int    `json:"idTag"`
	Updated            bool   `json:"updated"`
}

// TagResumed is the answer of tag-manager-tags.resume.
type TagResumed struct {
	IDSite             int    `json:"idSite"`
	IDContainer        string `json:"idContainer"`
	IDContainerVersion int    `json:"idContainerVersion"`
	IDTag              int    `json:"idTag"`
	Resumed            bool   `json:"resumed"`
}

// tagArguments are the arguments of the add and update tools; resume takes only the first four.
type tagArguments struct {
	IDSite          *int            `json:"idSite"`
	IDContainer     *string         `json:"idContainer"`
	IDTag           *int            `json:"idTag"`
	Type            *string         `json:"type"`
	Name            *string         `json:"name"`
	Parameters      json.RawMessage `json:"parameters"`
	FireTriggerIDs  []int           `json:"fireTriggerIds"`
	BlockTriggerIDs []int           `json:"blockTriggerIds"`
	FireLimit       *string         `json:"fireLimit"`
	FireDelay       *int            `json:"fireDelay"`
	Priority        *int            `json:"priority"`
	StartDate       *string         `json:"startDate"`
	EndDate         *string         `json:"endDate"`
	Description     *string         `json:"description"`
	Status          *string         `json:"status"`
}

func tagTriggerIDs(name string, ids []int, required bool, form url.Values) error {
	if ids == nil {
		if required {
			return invalidArgument(name + " is required")
		}
		return nil
	}
	if len(ids) > maxTagTriggers || required && name == "fireTriggerIds" && len(ids) < 1 {
		return invalidArgument(name + " must hold the IDs of at most 50 triggers, a tag needs at least one fire trigger")
	}
	for i, id := range ids {
		if id < 1 || id > 2147483647 {
			return invalidArgument(name + " must hold trigger IDs between 1 and 2147483647")
		}
		form.Set(name+"["+strconv.Itoa(i)+"]", strconv.Itoa(id))
	}
	return nil
}

// tagForm validates the settings of add (replace false) and update (replace true) and flattens them into form
// fields. params is the decoded parameters object, nil when none was given.
func tagForm(a *tagArguments, replace bool) (url.Values, map[string]any, error) {
	form := url.Values{}
	name, err := triggerNameOf(a.Name)
	if err != nil {
		return nil, nil, err
	}
	form.Set("name", name)
	description, err := triggerDescriptionOf(a.Description, replace)
	if err != nil {
		return nil, nil, err
	}
	form.Set("description", description)
	var params map[string]any
	if len(bytes.TrimSpace(a.Parameters)) == 0 {
		if replace {
			return nil, nil, invalidArgument("parameters are required; an empty object clears them")
		}
	} else if params, err = decodeJSONObject(a.Parameters); err != nil {
		return nil, nil, invalidArgument("parameters must be an object of at most 100 parameters")
	}
	if err := triggerParameters(a.Parameters, &formBudget{remaining: maxTriggerTotalText}, form); err != nil {
		return nil, nil, err
	}
	if err := tagTriggerIDs("fireTriggerIds", a.FireTriggerIDs, true, form); err != nil {
		return nil, nil, err
	}
	if err := tagTriggerIDs("blockTriggerIds", a.BlockTriggerIDs, replace, form); err != nil {
		return nil, nil, err
	}
	if a.FireLimit != nil {
		if !tagFireLimits[*a.FireLimit] {
			return nil, nil, invalidArgument("fireLimit must be unlimited, once_page, once_24hours, or once_lifetime")
		}
		form.Set("fireLimit", *a.FireLimit)
	} else if replace {
		return nil, nil, invalidArgument("fireLimit is required")
	}
	for name, given := range map[string]*int{"fireDelay": a.FireDelay, "priority": a.Priority} {
		if given == nil {
			if replace {
				return nil, nil, invalidArgument(name + " is required")
			}
			continue
		}
		if *given < 0 || *given > 2147483647 {
			return nil, nil, invalidArgument(name + " must be between 0 and 2147483647")
		}
		form.Set(name, strconv.Itoa(*given))
	}
	for name, given := range map[string]*string{"startDate": a.StartDate, "endDate": a.EndDate} {
		if given == nil {
			continue
		}
		if !tagDatePattern.MatchString(*given) {
			return nil, nil, invalidArgument(name + " must be a UTC date as YYYY-MM-DD or YYYY-MM-DD HH:MM:SS")
		}
		form.Set(name, *given)
	}
	if a.Status != nil {
		if replace || !tagStatuses[*a.Status] {
			return nil, nil, invalidArgument("status is an argument of add and must be active or paused")
		}
		form.Set("status", *a.Status)
	}
	return form, params, nil
}

// checkTagParameters requires a Matomo tag to refer to its configuration variable by a well-formed reference.
func checkTagParameters(tagType string, params map[string]any) error {
	if tagType == tagTypeMatomo {
		if _, err := configReferenceOf(params); err != nil {
			return err
		}
	}
	return nil
}

func configReferenceOf(params map[string]any) (string, error) {
	text, isText := params[matomoConfigParameter].(string)
	if match := configReference.FindStringSubmatch(text); isText && match != nil {
		return match[1], nil
	}
	return "", invalidArgument("matomoConfig must be exactly one reference {{Name}} to a variable of the draft of the type " +
		configVariableType)
}

// normalizeInstanceURL reduces an https address to host, port, and path so that two spellings of one address
// compare equal; anything else (other scheme, user info, query, fragment, a reference) has no normal form.
func normalizeInstanceURL(raw string) (string, bool) {
	if strings.Contains(raw, "{") || strings.ContainsAny(raw, "\\ \t\r\n") {
		return "", false
	}
	parsed, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.Opaque != "" {
		return "", false
	}
	port := parsed.Port()
	if port == "443" {
		port = ""
	}
	return strings.ToLower(parsed.Hostname()) + ":" + port + strings.TrimSuffix(parsed.EscapedPath(), "/"), true
}

// literalText reads a variable parameter that must be written out: a text or a number.
func literalText(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case json.Number:
		return v.String(), true
	}
	return "", false
}

// storedTagRow is the part of a stored tag the checks read; the parameters stay untruncated.
type storedTagRow struct {
	typ    string
	params map[string]any
}

func storedParameters(raw json.RawMessage) (map[string]any, bool) {
	trimmed := bytes.TrimSpace(raw)
	switch string(trimmed) {
	case "", "null", "[]", "{}":
		return map[string]any{}, true
	}
	params, err := decodeJSONObject(trimmed)
	return params, err == nil
}

// readTag reads the stored tag of the draft once; it is the evidence for the type and, for resume, the target.
func readTag(ctx context.Context, client *Client, op, tool string, site int, container string, draft, id int) (storedTagRow, error) {
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idContainer": {container},
		"idContainerVersion": {strconv.Itoa(draft)}, "idTag": {strconv.Itoa(id)}}
	var answer json.RawMessage
	if err := client.call(ctx, op, tagManagerRequirement(tool), "TagManager.getContainerTag", params, &answer); err != nil {
		return storedTagRow{}, err
	}
	if missingAnswer(answer) {
		return storedTagRow{}, &provider.Error{Class: provider.ClassNotFound, Op: op,
			Message: "Matomo does not hold this tag in the draft; nothing was changed"}
	}
	invalid := provider.InvalidResponse(op, "Matomo returned an invalid tag; nothing was changed")
	var parsed exportEntryJSON
	answer = bytes.TrimSpace(answer)
	if !bytes.HasPrefix(answer, []byte("{")) || json.Unmarshal(answer, &parsed) != nil || parsed.id() == nil ||
		*parsed.id() != id || !(entryRequest{site: site, version: draft}).bound(parsed) {
		return storedTagRow{}, invalid
	}
	stored, ok := storedParameters(parsed.Parameters)
	if !ok {
		return storedTagRow{}, invalid
	}
	return storedTagRow{typ: strings.TrimSpace(looseText(parsed.Type)), params: stored}, nil
}

// checkTrackingTarget proves with one read of the variables of the draft that the configuration variable named
// by the reference meets the rule of the variable tools for such a variable.
func checkTrackingTarget(ctx context.Context, client *Client, op, tool, baseURL string, site int, container string,
	draft int, name string) error {
	refused := invalidArgument("matomoConfig must refer to exactly one variable of the draft of the type " + configVariableType +
		" that names this connection's Matomo and the selected website in matomoUrl and idSite, keeps setApiUrl " +
		"and customRequestProcessing empty, and has tracker and script endpoints that are plain paths; nothing was changed")
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idContainer": {container},
		"idContainerVersion": {strconv.Itoa(draft)}}
	var answer json.RawMessage
	if err := client.call(ctx, op, tagManagerRequirement(tool), "TagManager.getContainerVariables", params, &answer); err != nil {
		return err
	}
	invalid := provider.InvalidResponse(op, "Matomo returned an invalid list of variables; nothing was changed")
	rows, ok := jsonRows(answer)
	if !ok {
		return invalid
	}
	var found *exportEntryJSON
	for _, row := range rows {
		var parsed exportEntryJSON
		if json.Unmarshal(row, &parsed) != nil {
			return invalid
		}
		if (entryRequest{site: site, version: draft}).bound(parsed) && parsed.Name != nil && parsed.Name.text == name {
			if found != nil {
				return refused
			}
			found = &parsed
		}
	}
	if found == nil || strings.TrimSpace(looseText(found.Type)) != configVariableType {
		return refused
	}
	stored, ok := storedParameters(found.Parameters)
	if !ok {
		return invalid
	}
	if checkConfigurationVariable(baseURL, site, stored) != nil {
		return refused
	}
	return nil
}

// trackingTargetMatches reports whether the parameters of a configuration variable hold the base address and the
// website as written-out values.
func trackingTargetMatches(baseURL string, site int, stored map[string]any) bool {
	want, valid := normalizeInstanceURL(baseURL)
	given, isText := literalText(stored["matomoUrl"])
	got, normal := normalizeInstanceURL(given)
	idSite, isNumber := literalText(stored["idSite"])
	return valid && isText && normal && got == want && isNumber && idSite == strconv.Itoa(site)
}

func looseText(l *loose) string {
	if l == nil {
		return ""
	}
	return l.text
}

// tagPlan says what the shared steps check: idTag is set for update and resume, tagType for add, params holds
// the new parameters of add and update.
type tagPlan struct {
	idTag   int
	tagType string
	params  map[string]any
	resume  bool
}

// tagStoreChange runs the proof reads and the one writing request. Nothing is sent when a read refuses.
func tagStoreChange(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	op, tool, method string, site int, container string, fields url.Values, plan tagPlan) (int, json.RawMessage, error) {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return 0, nil, err
	}
	draft, err := triggerDraft(ctx, client, op, tool, site, container)
	if err != nil {
		return 0, nil, err
	}
	tagType, params := plan.tagType, plan.params
	if plan.idTag > 0 {
		stored, err := readTag(ctx, client, op, tool, site, container, draft, plan.idTag)
		if err != nil {
			return 0, nil, err
		}
		tagType = stored.typ
		if !allowedTagTypes[tagType] {
			return 0, nil, invalidArgument("the stored tag is not of the type Matomo or ThemeColor; nothing was changed")
		}
		if plan.resume {
			params = stored.params
		} else if err := checkTagParameters(tagType, params); err != nil {
			return 0, nil, err
		}
	}
	if tagType == tagTypeMatomo {
		reference, err := configReferenceOf(params)
		if err != nil {
			return 0, nil, err
		}
		if err := checkTrackingTarget(ctx, client, op, tool, resolved.BaseURL, site, container, draft, reference); err != nil {
			return 0, nil, err
		}
	}
	request := url.Values{"idSite": {strconv.Itoa(site)}, "idContainer": {container},
		"idContainerVersion": {strconv.Itoa(draft)}}
	for name, values := range fields {
		request[name] = values
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, tagManagerRequirement(tool), method, request, tagSentOnce, &answer); err != nil {
		return 0, nil, err
	}
	return draft, answer, nil
}

func tagScope(resolved *config.Resolved, a *tagArguments) (int, string, error) {
	id, err := selectSite(resolved, a.IDSite)
	if err != nil {
		return 0, "", err
	}
	container, err := containerIDOf(a.IDContainer)
	if err != nil {
		return 0, "", err
	}
	return id, container, nil
}

func invokeTagManagerTagsAdd(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "add" + tagOpSuffix
	var args tagArguments
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, container, err := tagScope(resolved, &args)
	if err != nil {
		return nil, err
	}
	if args.IDTag != nil {
		return nil, invalidArgument("idTag does not belong to this tool")
	}
	// The type is checked before the secret is read and before any request.
	if args.Type == nil || !allowedTagTypes[*args.Type] {
		return nil, invalidArgument("type must be Matomo or ThemeColor; every other tag type can send data to a third party")
	}
	fields, params, err := tagForm(&args, false)
	if err != nil {
		return nil, err
	}
	if err := checkTagParameters(*args.Type, params); err != nil {
		return nil, err
	}
	fields.Set("type", *args.Type)
	draft, answer, err := tagStoreChange(ctx, resolved, secrets, red, op, tagManagerTagsAdd.ID, "TagManager.addContainerTag",
		id, container, fields, tagPlan{tagType: *args.Type, params: params})
	if err != nil {
		return nil, err
	}
	var created int
	if scalar(answer, &created) != nil || created < 1 {
		return nil, unclearChange(op, "Matomo returned no usable ID for the new tag", tagSentOnce)
	}
	return &TagCreated{IDSite: id, IDContainer: container, IDContainerVersion: draft, IDTag: created, Created: true}, nil
}

func invokeTagManagerTagsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update" + tagOpSuffix
	var args tagArguments
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, container, err := tagScope(resolved, &args)
	if err != nil {
		return nil, err
	}
	tagID, err := tagIDOf(args.IDTag)
	if err != nil {
		return nil, err
	}
	if args.Type != nil {
		return nil, invalidArgument("the type of a tag cannot be changed")
	}
	fields, params, err := tagForm(&args, true)
	if err != nil {
		return nil, err
	}
	fields.Set("idTag", strconv.Itoa(tagID))
	draft, answer, err := tagStoreChange(ctx, resolved, secrets, red, op, tagManagerTagsUpdate.ID, "TagManager.updateContainerTag",
		id, container, fields, tagPlan{idTag: tagID, params: params})
	if err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the update", tagSentOnce)
	}
	return &TagUpdated{IDSite: id, IDContainer: container, IDContainerVersion: draft, IDTag: tagID, Updated: true}, nil
}

func invokeTagManagerTagsResume(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "resume" + tagOpSuffix
	var args tagArguments
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, container, err := tagScope(resolved, &args)
	if err != nil {
		return nil, err
	}
	tagID, err := tagIDOf(args.IDTag)
	if err != nil {
		return nil, err
	}
	fields := url.Values{"idTag": {strconv.Itoa(tagID)}}
	draft, answer, err := tagStoreChange(ctx, resolved, secrets, red, op, tagManagerTagsResume.ID, "TagManager.resumeContainerTag",
		id, container, fields, tagPlan{idTag: tagID, resume: true})
	if err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the request", tagSentOnce)
	}
	return &TagResumed{IDSite: id, IDContainer: container, IDContainerVersion: draft, IDTag: tagID, Resumed: true}, nil
}
