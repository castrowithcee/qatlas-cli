package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

// recordingDeletion is one of the three deletions around a session recording. Each has a fixed Matomo method.
// A deletion with a sub-ID names a recorded session or page view, which hold personal data.
type recordingDeletion struct {
	tool     string
	method   string
	op       string
	title    string
	summary  string
	subID    string // "", "idVisit", or "idLogHsr"
	subHint  string
	personal bool
}

var recordingDeletions = []recordingDeletion{
	{tool: "session-recordings.delete", method: "HeatmapSessionRecording.deleteSessionRecording",
		op: "delete session recording", title: "Delete a Matomo session recording",
		summary: "Delete a session recording; Qatlas Connect offers no way to restore it. Matomo does not document what " +
			"happens to its recorded sessions"},
	{tool: "recorded-sessions.delete-session", method: "HeatmapSessionRecording.deleteRecordedSession",
		op: "delete recorded session", title: "Delete a recorded Matomo session",
		summary: "Delete all recorded page views within one recorded session of a visit; Qatlas Connect offers no way to " +
			"restore them", subID: "idVisit", subHint: "ID of the visit, from matomo.recorded-sessions.list", personal: true},
	{tool: "recorded-sessions.delete-pageview", method: "HeatmapSessionRecording.deleteRecordedPageview",
		op: "delete recorded page view", title: "Delete a recorded page view of a Matomo session",
		summary: "Delete one recorded page view within a recorded session; Qatlas Connect offers no way to restore it",
		subID:   "idLogHsr", subHint: "ID of the recorded page view, from matomo.recorded-sessions.list", personal: true},
}

func (d recordingDeletion) id() string { return Provider + "." + d.tool }

func (d recordingDeletion) risk() capability.Risk {
	risk := recordingChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent)
	if d.personal {
		risk.DataSensitivity = recordedSessionsSensitive
	}
	return risk
}

func (d recordingDeletion) descriptor() capability.Descriptor {
	arguments := []capability.Argument{idSiteArgument(),
		{Name: "idSiteHsr", Required: true, Description: "ID of the recording, from matomo.session-recordings.list"}}
	properties := idSiteSchema + `,` + idHeatmapSchema
	required := `"idSiteHsr"`
	example := `{"idSite":1,"idSiteHsr":13}`
	fields := []capability.Field{
		{Name: "idSite", Description: "The website the recording belongs to"},
		{Name: "idSiteHsr", Description: "The recording that was changed"}}
	output := `"idSite":{"type":"integer"},"idSiteHsr":{"type":"integer"}`
	outputRequired := `"idSite","idSiteHsr"`
	if d.subID != "" {
		schema := idRecordedSessionSchema
		if d.subID == "idVisit" {
			schema = idVisitSchema
		}
		arguments = append(arguments, capability.Argument{Name: d.subID, Required: true, Description: d.subHint})
		properties += `,` + schema
		required += `,"` + d.subID + `"`
		example = `{"idSite":1,"idSiteHsr":13,"` + d.subID + `":2001}`
		fields = append(fields, capability.Field{Name: d.subID, Description: "The " + d.subID + " that was deleted"})
		output += `,"` + d.subID + `":{"type":"integer"}`
		outputRequired += `,"` + d.subID + `"`
	}
	fields = append(fields, capability.Field{Name: "deleted", Description: "True once Matomo accepted the deletion"})
	description := d.summary + ". The recording is read first and must belong to the website, otherwise it is not found."
	if d.subID != "" {
		description += " " + d.subID + " is sent with the recording; the binding of that ID rests on the read and on " +
			"Matomo resolving it within the recording."
	}
	if d.personal {
		description += " Personal data: the deleted recordings show what a person did on the website."
	}
	return capability.Descriptor{
		ID: d.id(), Version: 1, Title: d.title,
		Description: description + " Needs the premium plugin HeatmapSessionRecording and, expected, a write right " +
			"on the website; idempotent in effect" + recordingSentOnceGet + "; offered only where a connection's " +
			"tools list names it",
		Tags: []string{"matomo", "session-recordings", "behavior", "delete"}, Provider: Provider,
		RequiresToolAllowList: true, Risk: d.risk(),
		InputSchema: json.RawMessage(`{"type":"object","properties":{` + properties + `},` +
			`"required":[` + required + `],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{` + output + `,"deleted":{"type":"boolean"}},` +
			`"required":[` + outputRequired + `,"deleted"],"additionalProperties":false}`),
		Arguments: arguments, Fields: fields,
		Examples: []capability.Example{{Description: d.title + ", recording 13 of website 1",
			Arguments: json.RawMessage(example)}},
	}
}

// SessionRecordingDeleted is the answer of session-recordings.delete and the two recorded-sessions deletions;
// the visit or page view ID is present only for the tool that took it.
type SessionRecordingDeleted struct {
	IDSite    int  `json:"idSite"`
	IDSiteHsr int  `json:"idSiteHsr"`
	IDVisit   *int `json:"idVisit,omitempty"`
	IDLogHsr  *int `json:"idLogHsr,omitempty"`
	Deleted   bool `json:"deleted"`
}

func (d recordingDeletion) invoke(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var args struct {
		IDSite    *int `json:"idSite"`
		IDSiteHsr *int `json:"idSiteHsr"`
		IDVisit   *int `json:"idVisit"`
		IDLogHsr  *int `json:"idLogHsr"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := checkRecordingID(args.IDSiteHsr); err != nil {
		return nil, err
	}
	sub := map[string]*int{"": nil, "idVisit": args.IDVisit, "idLogHsr": args.IDLogHsr}
	// An argument this tool does not take is refused, not silently dropped.
	for name, value := range map[string]*int{"idVisit": args.IDVisit, "idLogHsr": args.IDLogHsr} {
		if name != d.subID && value != nil {
			return nil, invalidArgument(name + " is not an argument of this tool")
		}
	}
	if d.subID != "" {
		if err := checkRecordedID(d.subID, sub[d.subID], "matomo.recorded-sessions.list"); err != nil {
			return nil, err
		}
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idSiteHsr": {strconv.Itoa(*args.IDSiteHsr)}}
	if d.subID != "" {
		params.Set(d.subID, strconv.Itoa(*sub[d.subID]))
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := heatmapsRequirement(d.id())
	// The recording ID counts across websites: one read must show it on this website before anything is deleted.
	if _, err := client.fetchSessionRecording(ctx, d.op, req, site, *args.IDSiteHsr); err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, d.op, req, d.method, params, recordingSentOnceGet, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(d.op, "Matomo returned no confirmation of the deletion", recordingSentOnceGet)
	}
	return &SessionRecordingDeleted{IDSite: site, IDSiteHsr: *args.IDSiteHsr, IDVisit: args.IDVisit,
		IDLogHsr: args.IDLogHsr, Deleted: true}, nil
}

func sessionRecordingDeleteOperations() []capability.Operation {
	ops := make([]capability.Operation, 0, len(recordingDeletions))
	for _, d := range recordingDeletions {
		ops = append(ops, capability.Operation{Descriptor: d.descriptor(), Handler: capability.Handler(d.invoke)})
	}
	return ops
}
