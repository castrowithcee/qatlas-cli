package endpoints

import (
	"errors"
	"fmt"
	"sort"
)

// ErrUnmappedService is a registration error: a service model has no entry in the segment table, so no tool
// ID can be built for it.
var ErrUnmappedService = errors.New("service model has no tool-ID segment; add it to the segment table")

// Segment maps a service model to the second part of a tool ID (aws.<segment>.<object>.<action>).
type Segment struct {
	// Model is the directory name in api-models-aws.
	Model string
	// Segment is the tool-ID segment: the AWS service name in lower case, words joined by hyphens, a single
	// word only for one-word brands.
	Segment string
	// Reserved marks a segment set aside for a future service. It has no endpoint entry yet.
	Reserved bool
}

// segments is the fixed table of the foundation. Reserved entries keep the names of later services stable.
var segments = []Segment{
	{Model: "sts", Segment: "sts"},
	{Model: "iam", Segment: "iam"},
	{Model: "s3", Segment: "s3"},
	// SES v2; there is no SES v1 here.
	{Model: "sesv2", Segment: "ses"},
	{Model: "route-53", Segment: "route53"},
	{Model: "account", Segment: "account"},
	{Model: "route-53-domains", Segment: "route53-domains", Reserved: true},
	{Model: "sfn", Segment: "step-functions", Reserved: true},
	{Model: "secrets-manager", Segment: "secrets-manager", Reserved: true},
	{Model: "cost-explorer", Segment: "cost-explorer", Reserved: true},
}

// Segments returns a copy of the table, in table order.
func Segments() []Segment { return append([]Segment(nil), segments...) }

// SegmentOf returns the table entry of a service model. A model without an entry is a registration error.
func SegmentOf(model string) (Segment, error) {
	for _, s := range segments {
		if s.Model == model {
			return s, nil
		}
	}
	return Segment{}, fmt.Errorf("%w: %q", ErrUnmappedService, model)
}

// ModelOf returns the table entry of a tool-ID segment.
func ModelOf(segment string) (Segment, error) {
	for _, s := range segments {
		if s.Segment == segment {
			return s, nil
		}
	}
	return Segment{}, fmt.Errorf("%w: segment %q", ErrUnmappedService, segment)
}

// Models lists the mapped model names, sorted.
func Models() []string {
	list := make([]string, len(segments))
	for i, s := range segments {
		list[i] = s.Model
	}
	sort.Strings(list)
	return list
}
