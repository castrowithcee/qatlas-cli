package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// recordingState is one of the three state changes of a session recording. Each has a fixed Matomo method.
type recordingState struct {
	action      string
	method      string
	op          string
	title       string
	summary     string
	idempotency capability.Idempotency
}

var recordingStates = []recordingState{
	{"pause", "HeatmapSessionRecording.pauseSessionRecording", "pause session recording", "Pause a Matomo session recording",
		"Pause a session recording so that it records no further sessions; the configuration and the " +
			"recorded sessions stay. Resume it with matomo.session-recordings.resume", capability.IdempotencyIdempotent},
	{"resume", "HeatmapSessionRecording.resumeSessionRecording", "resume session recording", "Resume a Matomo session recording",
		"Resume a paused session recording so that it records sessions again", capability.IdempotencyIdempotent},
	{"end", "HeatmapSessionRecording.endSessionRecording", "end session recording", "End a Matomo session recording",
		"End (finish) a session recording; the configuration and the recorded sessions stay. Matomo does not " +
			"document whether an ended session recording can be resumed", capability.IdempotencyIdempotent},
}

func (s recordingState) descriptor() capability.Descriptor {
	return capability.Descriptor{
		ID: Provider + ".session-recordings." + s.action, Version: 1, Title: s.title,
		Description: s.summary + ". The recording is read first and must belong to the website, otherwise it is not " +
			"found. Needs the premium plugin HeatmapSessionRecording and, expected, a write right on the website; " +
			"idempotent in effect" + recordingSentOnceGet,
		Tags: []string{"matomo", "session-recordings", "behavior", "update"},
		Risk: recordingChangeRisk(capability.EffectUpdate, s.idempotency), Provider: Provider,
		InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idHeatmapSchema + `},` +
			`"required":["idSiteHsr"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
			`"idSiteHsr":{"type":"integer"},"` + s.done() + `":{"type":"boolean"}},` +
			`"required":["idSite","idSiteHsr","` + s.done() + `"],"additionalProperties":false}`),
		Arguments: []capability.Argument{idSiteArgument(),
			{Name: "idSiteHsr", Required: true, Description: "ID of the recording, from matomo.session-recordings.list"}},
		Fields: []capability.Field{
			{Name: "idSite", Description: "The website the recording belongs to"},
			{Name: "idSiteHsr", Description: "The recording that was changed"},
			{Name: s.done(), Description: "True once Matomo accepted the change"}},
		Examples: []capability.Example{{Description: s.title + ", recording 13 of website 1",
			Arguments: json.RawMessage(`{"idSite":1,"idSiteHsr":13}`)}},
	}
}

func (s recordingState) done() string {
	switch s.action {
	case "pause":
		return "paused"
	case "resume":
		return "resumed"
	}
	return "ended"
}

// SessionRecordingStateChanged is the answer of session-recordings.pause, .resume, and .end; exactly one of the
// flags is present, named after the action.
type SessionRecordingStateChanged struct {
	IDSite    int  `json:"idSite"`
	IDSiteHsr int  `json:"idSiteHsr"`
	Paused    bool `json:"paused,omitempty"`
	Resumed   bool `json:"resumed,omitempty"`
	Ended     bool `json:"ended,omitempty"`
}

func (s recordingState) invoke(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var args struct {
		IDSite    *int `json:"idSite"`
		IDSiteHsr *int `json:"idSiteHsr"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := checkRecordingID(args.IDSiteHsr); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idSiteHsr": {strconv.Itoa(*args.IDSiteHsr)}}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := heatmapsRequirement(Provider + ".session-recordings." + s.action)
	// The recording ID counts across websites: one read must show it on this website before its state changes.
	if _, err := client.fetchSessionRecording(ctx, s.op, req, site, *args.IDSiteHsr); err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, s.op, req, s.method, params, recordingSentOnceGet, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(s.op, "Matomo returned no confirmation of the session recording", recordingSentOnceGet)
	}
	out := &SessionRecordingStateChanged{IDSite: site, IDSiteHsr: *args.IDSiteHsr}
	switch s.action {
	case "pause":
		out.Paused = true
	case "resume":
		out.Resumed = true
	default:
		out.Ended = true
	}
	return out, nil
}

func sessionRecordingStateOperations() []capability.Operation {
	ops := make([]capability.Operation, 0, len(recordingStates))
	for _, s := range recordingStates {
		ops = append(ops, capability.Operation{Descriptor: s.descriptor(), Handler: capability.Handler(s.invoke)})
	}
	return ops
}
