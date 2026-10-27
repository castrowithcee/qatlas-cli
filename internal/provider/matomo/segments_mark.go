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

const (
	segmentMarkOutput = `{"type":"object","properties":{"idSegment":{"type":"integer"},"starred":{"type":"boolean"},` +
		`"starred_by":{"type":"string"}},"required":["idSegment","starred"],"additionalProperties":false}`
	segmentDeleteOutput = `{"type":"object","properties":{"idSegment":{"type":"integer"},"name":{"type":"string"},` +
		`"deleted":{"type":"boolean"}},"required":["idSegment","deleted"],"additionalProperties":false}`
	segmentIDOnlyInput = `{"type":"object","properties":{` + idSegmentSchema + `},"required":["idSegment"],` +
		`"additionalProperties":false}`
)

func segmentIDArgument(verb string) []capability.Argument {
	return []capability.Argument{{Name: "idSegment", Required: true,
		Description: "ID of the segment to " + verb + ", from matomo.segments.list"}}
}

var segmentsDelete = capability.Descriptor{
	ID: Provider + ".segments.delete", Version: 1, Title: "Delete a Matomo segment",
	Description: "Delete one saved segment. Matomo only marks the segment as deleted: it disappears for every " +
		"user and from every report and link that uses it, and its stored data is cleaned up later by Matomo; " +
		"the tool cannot restore it, and a segment created again gets a new ID. The segment is read once before " +
		"the deletion and must lie inside the connection's targets, otherwise it is not found; only its creator " +
		"or a Super User may delete it, and only a Super User a segment for all websites; sent once and never " +
		"repeated; offered only where a connection's tools list names it",
	Tags: []string{"matomo", "segments", "saved", "delete"}, Risk: segmentChangeRisk(capability.EffectDelete,
		capability.IdempotencyIdempotent), Provider: Provider, RequiresToolAllowList: true,
	InputSchema:  json.RawMessage(segmentIDOnlyInput),
	OutputSchema: json.RawMessage(segmentDeleteOutput),
	Arguments:    segmentIDArgument("delete"),
	Fields: []capability.Field{
		{Name: "idSegment", Description: "ID of the deleted segment"},
		{Name: "name", Description: "Name the segment had, untrusted"},
		{Name: "deleted", Description: "True once Matomo marked the segment as deleted"},
	},
	Examples: []capability.Example{{Description: "Delete segment 4", Arguments: json.RawMessage(`{"idSegment":4}`)}},
}

func segmentMarkDescriptor(action, title, effectNote string) capability.Descriptor {
	return capability.Descriptor{
		ID: Provider + ".segments." + action, Version: 1, Title: title,
		Description: effectNote + " The mark belongs to the segment, not to a user: Matomo stores one flag and " +
			"the login that set it, so everyone sees it. The segment is read once before the change and must lie " +
			"inside the connection's targets; only its creator or a Super User may change it, and only a Super " +
			"User a segment for all websites; idempotent; sent once and never repeated",
		Tags: []string{"matomo", "segments", "saved", action}, Risk: segmentChangeRisk(capability.EffectUpdate,
			capability.IdempotencyIdempotent), Provider: Provider,
		InputSchema:  json.RawMessage(segmentIDOnlyInput),
		OutputSchema: json.RawMessage(segmentMarkOutput),
		Arguments:    segmentIDArgument(action),
		Fields: []capability.Field{
			{Name: "idSegment", Description: "ID of the changed segment"},
			{Name: "starred", Description: "The star state of the segment now"},
			{Name: "starred_by", Description: "Login of the user who set the star, untrusted; absent once removed"},
		},
		Examples: []capability.Example{{Description: "Segment 4", Arguments: json.RawMessage(`{"idSegment":4}`)}},
	}
}

var (
	segmentsStar   = segmentMarkDescriptor("star", "Star a Matomo segment", "Mark one saved segment with a star.")
	segmentsUnstar = segmentMarkDescriptor("unstar", "Unstar a Matomo segment", "Remove the star from one saved segment.")
)

// SegmentDeleted is the answer of segments.delete.
type SegmentDeleted struct {
	IDSegment int    `json:"idSegment"`
	Name      string `json:"name,omitempty"`
	Deleted   bool   `json:"deleted"`
}

// SegmentMarked is the answer of segments.star and segments.unstar.
type SegmentMarked struct {
	IDSegment int    `json:"idSegment"`
	Starred   bool   `json:"starred"`
	StarredBy string `json:"starred_by,omitempty"`
}

// provenSegmentID reads the argument and proves the segment against the targets with one read.
func provenSegmentID(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	raw json.RawMessage, op string, req requirement) (*Client, Segment, error) {
	var args struct {
		IDSegment *int `json:"idSegment"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, Segment{}, err
	}
	id, err := segmentIDOf(args.IDSegment)
	if err != nil {
		return nil, Segment{}, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, Segment{}, err
	}
	if !bound.reachesSites() {
		return nil, Segment{}, invalidArgument("this connection is bound to no website")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, Segment{}, err
	}
	segment, err := client.fetchSegment(ctx, op, req, id, true)
	return client, segment, err
}

func invokeSegmentsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete segment"
	req := segmentsRequirement(segmentsDelete.ID)
	client, segment, err := provenSegmentID(ctx, resolved, secrets, red, raw, op, req)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSegment": {strconv.Itoa(segment.ID)}}
	var answer json.RawMessage
	if err := client.change(ctx, op, req, "SegmentEditor.delete", params, segmentSentOnce, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the deletion", segmentSentOnce)
	}
	return &SegmentDeleted{IDSegment: segment.ID, Name: segment.Name, Deleted: true}, nil
}

func invokeSegmentsMark(star bool) capability.Handler {
	method, op, desc := "SegmentEditor.unstar", "unstar segment", segmentsUnstar
	if star {
		method, op, desc = "SegmentEditor.star", "star segment", segmentsStar
	}
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		req := segmentsRequirement(desc.ID)
		client, segment, err := provenSegmentID(ctx, resolved, secrets, red, raw, op, req)
		if err != nil {
			return nil, err
		}
		params := url.Values{"idSegment": {strconv.Itoa(segment.ID)}}
		var answer struct {
			Result    *bool  `json:"result"`
			Starred   *loose `json:"starred"`
			StarredBy *loose `json:"starred_by"`
		}
		if err := client.change(ctx, op, req, method, params, segmentSentOnce, &answer); err != nil {
			return nil, err
		}
		state := answer.Starred.number()
		if answer.Result == nil || !*answer.Result || state == nil || (*state == 1) != star || *state > 1 || *state < 0 {
			return nil, unclearChange(op, "Matomo returned no confirmation of the change", segmentSentOnce)
		}
		marked := &SegmentMarked{IDSegment: segment.ID, Starred: star}
		if star {
			marked.StarredBy = answer.StarredBy.str(maxStringLength)
		}
		return marked, nil
	}
}
