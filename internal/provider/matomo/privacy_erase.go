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
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

func eraseRisk(effect capability.Effect, sensitivity string) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: sensitivity}
}

var visitsSentOnce = sentOnceFor("visits", "matomo.data-subjects.find")

var dataSubjectsDelete = capability.Descriptor{
	ID: Provider + ".data-subjects.delete", Version: 1, Title: "Delete the data of visits from Matomo",
	Description: "Delete for good the stored data of the visits that matomo.data-subjects.find returned " +
		"(PrivacyManager.deleteDataSubjects): the rows of each log table of these visits, for a data subject " +
		"request. It cannot be undone and reaches only the visits listed. It needs an admin right on every website " +
		"of the list. At most 401 visits per call. A personal token needs no password confirmation and the tool " +
		"takes no password. The answer counts the rows deleted per storage area; sent once and never repeated; " +
		"offered only where a connection's tools list names it",
	Tags: []string{"matomo", "gdpr", "privacy", "data-subjects", "visitors", "delete", "personal-data"},
	Risk: eraseRisk(capability.EffectDelete, dataSubjectsSensitive), Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"visits":{"type":"array","minItems":1,"maxItems":401,` +
		`"items":{"type":"object","properties":{` + visitIDKeys + `},"required":["idSite","idVisit"],` +
		`"additionalProperties":false}}},"required":["visits"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"visits":{"type":"array","items":{"type":"object",` +
		`"properties":{"id_site":{"type":"integer"},"id_visit":{"type":"integer"}},"additionalProperties":false}},` +
		`"areas":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},` +
		`"deleted_rows":{"type":"integer"}},"required":["name","deleted_rows"],"additionalProperties":false}},` +
		`"truncated":{"type":"boolean"},"deleted":{"type":"boolean"}},` +
		`"required":["visits","areas","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "visits", Required: true, Description: "The visits to delete, 1 to 401 " +
		"entries of idSite and idVisit (id_site and id_visit of matomo.data-subjects.find); every idSite must lie " +
		"inside the connection's targets; one foreign website refuses the whole call"}},
	Fields: []capability.Field{
		{Name: "visits", Description: "The visits sent to Matomo"},
		{Name: "visits[].id_site", Description: "Website ID"},
		{Name: "visits[].id_visit", Description: "Visit ID"},
		{Name: "areas", Description: "One entry per storage area (log table or plugin) with the number of rows " +
			"Matomo deleted, ordered by name; an empty list when Matomo reports none"},
		{Name: "areas[].name", Description: "Name of the storage area, such as log_visit, untrusted"},
		{Name: "areas[].deleted_rows", Description: "Rows deleted in the area"},
		{Name: "truncated", Description: "True when areas were left out because Matomo named more than 100"},
		{Name: "deleted", Description: "True once Matomo accepted the deletion"},
	},
	Examples: []capability.Example{{Description: "Delete two visits found on website 1",
		Arguments: json.RawMessage(`{"visits":[{"idSite":1,"idVisit":4711},{"idSite":1,"idVisit":4712}]}`)}},
}

// DeletedArea is the number of rows Matomo deleted in one storage area.
type DeletedArea struct {
	Name        string `json:"name"`
	DeletedRows int    `json:"deleted_rows"`
}

// SubjectsDeleted is the answer of data-subjects.delete.
type SubjectsDeleted struct {
	Visits    []ExportVisit `json:"visits"`
	Areas     []DeletedArea `json:"areas"`
	Truncated bool          `json:"truncated,omitempty"`
	Deleted   bool          `json:"deleted"`
}

func invokeDataSubjectsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete data subjects"
	var args struct {
		Visits []struct {
			IDSite  *int `json:"idSite"`
			IDVisit *int `json:"idVisit"`
		} `json:"visits"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if !bound.reachesSites() {
		return nil, invalidArgument("this connection is bound to no website")
	}
	if len(args.Visits) < 1 || len(args.Visits) > maxExportVisits {
		return nil, invalidArgument("visits must hold 1 to 401 entries")
	}
	visits := make([]visitKey, 0, len(args.Visits))
	for _, one := range args.Visits {
		if one.IDSite == nil || one.IDVisit == nil || !validID(*one.IDSite) || !validID(*one.IDVisit) {
			return nil, invalidArgument("every visit needs a positive integer idSite and idVisit")
		}
		// The whole list is checked before a secret is read; one foreign website refuses it and is not named.
		if !bound.allows(*one.IDSite) {
			return nil, invalidArgument("every idSite must lie inside the targets of this connection")
		}
		key := visitKey{*one.IDSite, *one.IDVisit}
		if slices.Contains(visits, key) {
			return nil, invalidArgument("visits must not repeat a visit")
		}
		visits = append(visits, key)
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{}
	listed := make([]ExportVisit, 0, len(visits))
	for i, one := range visits {
		prefix := "visits[" + strconv.Itoa(i) + "]"
		params.Set(prefix+"[idsite]", strconv.Itoa(one.Site))
		params.Set(prefix+"[idvisit]", strconv.Itoa(one.Visit))
		listed = append(listed, ExportVisit{IDSite: one.Site, IDVisit: one.Visit})
	}
	var answer json.RawMessage
	req := requirement{tool: dataSubjectsDelete.ID, plugin: "PrivacyManager"}
	if err := client.change(ctx, op, req, "PrivacyManager.deleteDataSubjects", params, visitsSentOnce, &answer); err != nil {
		return nil, err
	}
	areas, truncated, ok := deletedAreas(answer)
	if !ok {
		return nil, unclearChange(op, "Matomo returned no usable count of the deletion", visitsSentOnce)
	}
	return &SubjectsDeleted{Visits: listed, Areas: areas, Truncated: truncated, Deleted: true}, nil
}

// deletedAreas reads the answer of deleteDataSubjects: an object from storage area to the rows deleted, or an
// empty list when nothing was deleted.
func deletedAreas(answer json.RawMessage) ([]DeletedArea, bool, bool) {
	trimmed := bytes.TrimSpace(answer)
	if bytes.Equal(trimmed, []byte("[]")) {
		return []DeletedArea{}, false, true
	}
	var named map[string]json.RawMessage
	if json.Unmarshal(trimmed, &named) != nil {
		return nil, false, false
	}
	names := make([]string, 0, len(named))
	for name := range named {
		names = append(names, name)
	}
	slices.Sort(names)
	var truncated bool
	if len(names) > maxExportTables {
		names, truncated = names[:maxExportTables], true
	}
	areas := make([]DeletedArea, 0, len(names))
	for _, name := range names {
		var count int
		if json.Unmarshal(named[name], &count) != nil || count < 0 {
			return nil, false, false
		}
		areas = append(areas, DeletedArea{Name: bounded(name), DeletedRows: count})
	}
	return areas, truncated, true
}
