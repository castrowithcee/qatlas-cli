package matomo

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const contentPropertiesSchema = `"contentName":{"type":"string","minLength":1,"maxLength":512},` +
	`"contentPiece":{"type":"string","minLength":1,"maxLength":512},` +
	`"contentTarget":{"type":"string","minLength":1,"maxLength":2048}`

func contentArguments() []capability.Argument {
	return []capability.Argument{
		{Name: "contentName", Required: true, Description: "Name of the content block (c_n), up to 512 characters"},
		{Name: "contentPiece", Description: "The piece of content itself (c_p), such as an image path or a text, up to 512 characters"},
		{Name: "contentTarget", Description: "Where the content leads (c_t), up to 2048 characters; passed on as text and never fetched"},
	}
}

var trackingRecordContentImpression = capability.Descriptor{
	ID: Provider + ".tracking.record-content-impression", Version: 1, Title: "Record a content impression in Matomo",
	Description: "Record one impression of a content block (name, optional piece and target) of a website. " +
		trackingNote + trackingSentOnce,
	Tags: []string{"matomo", "tracking", "content", "impression", "create"}, Risk: trackingRisk(), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + trackingContextSchema + `,` +
		contentPropertiesSchema + `},"required":["url","contentName"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(trackingOutputSchema),
	Arguments:    append(trackingContextArguments(), contentArguments()...),
	Fields:       trackingResultFields,
	Examples: []capability.Example{{Description: "Record an impression of a banner on website 1",
		Arguments: json.RawMessage(`{"idSite":1,"url":"https://example.org/","contentName":"Summer banner",` +
			`"contentPiece":"/img/summer.png","contentTarget":"https://example.org/sale"}`)}},
}

var trackingRecordContentInteraction = capability.Descriptor{
	ID: Provider + ".tracking.record-content-interaction", Version: 1, Title: "Record a content interaction in Matomo",
	Description: "Record one interaction (such as a click) with a content block (name, optional piece and target) of a " +
		"website. " + trackingNote + trackingSentOnce,
	Tags: []string{"matomo", "tracking", "content", "interaction", "create"}, Risk: trackingRisk(), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + trackingContextSchema + `,` +
		`"interaction":{"type":"string","minLength":1,"maxLength":512},` + contentPropertiesSchema + `},` +
		`"required":["url","interaction","contentName"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(trackingOutputSchema),
	Arguments: append(append(trackingContextArguments(),
		capability.Argument{Name: "interaction", Required: true, Description: "Kind of interaction (c_i), a free text such as click, up to 512 characters"}),
		contentArguments()...),
	Fields: trackingResultFields,
	Examples: []capability.Example{{Description: "Record a click on a banner on website 1",
		Arguments: json.RawMessage(`{"idSite":1,"url":"https://example.org/","interaction":"click","contentName":"Summer banner",` +
			`"contentPiece":"/img/summer.png","contentTarget":"https://example.org/sale"}`)}},
}

type contentFields struct {
	ContentName   *string `json:"contentName"`
	ContentPiece  *string `json:"contentPiece"`
	ContentTarget *string `json:"contentTarget"`
}

func (c *contentFields) store(form url.Values) error {
	if err := trackingRequired(form, "c_n", "contentName", c.ContentName, maxTrackingText); err != nil {
		return err
	}
	if err := trackingText(form, "c_p", "contentPiece", c.ContentPiece, maxTrackingText); err != nil {
		return err
	}
	return trackingText(form, "c_t", "contentTarget", c.ContentTarget, maxTrackingURL)
}

func contentImpressionEntry() (any, *trackingContext, func(url.Values) error) {
	var args struct {
		trackingContext
		contentFields
	}
	return &args, &args.trackingContext, args.store
}

func invokeTrackingRecordContentImpression(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	args, shared, extend := contentImpressionEntry()
	return record(ctx, resolved, secrets, red, "record content impression", raw, args,
		func() *trackingContext { return shared }, extend)
}

func contentInteractionEntry() (any, *trackingContext, func(url.Values) error) {
	var args struct {
		trackingContext
		contentFields
		Interaction *string `json:"interaction"`
	}
	return &args, &args.trackingContext, func(form url.Values) error {
		// Without c_i Matomo would count the request as an impression.
		if err := trackingRequired(form, "c_i", "interaction", args.Interaction, maxTrackingText); err != nil {
			return err
		}
		return args.store(form)
	}
}

func invokeTrackingRecordContentInteraction(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	args, shared, extend := contentInteractionEntry()
	return record(ctx, resolved, secrets, red, "record content interaction", raw, args,
		func() *trackingContext { return shared }, extend)
}

func trackingContentOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: trackingRecordContentImpression, Handler: capability.Handler(invokeTrackingRecordContentImpression)},
		{Descriptor: trackingRecordContentInteraction, Handler: capability.Handler(invokeTrackingRecordContentInteraction)},
	}
}
