package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

const (
	maxCustomMetricName    = 255
	maxCustomMetricText    = 2048
	maxCustomMetricID      = 2147483647
	maxCustomMetricDecimal = 10
	// customMetricFormat is the form of formatType. The reference names the default number but no other value, so
	// Matomo stays the authority on the values.
	customMetricFormat = `^[A-Za-z0-9_.-]{1,32}$`
)

var (
	customMetricFormatPattern = regexp.MustCompile(customMetricFormat)
	customMetricSentOnce      = sentOnceFor("custom metric", "matomo.custom-metrics.list")
)

const idCustomMetricSchema = `"idCustomMetric":{"type":"integer","minimum":1,"maximum":2147483647}`

// customMetricChangeRisk is the risk of every custom metric change: confirmed, open to the instance, and about the
// metric formulas of the instance.
func customMetricChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return customReportChangeRisk(effect, idempotency)
}

var customMetricsSave = capability.Descriptor{
	ID: Provider + ".custom-metrics.save", Version: 1, Title: "Create or change a Matomo custom metric",
	Description: "Without idCustomMetric create a custom metric from a name and a formula over metrics " +
		"(addCustomMetric; a repeated call creates a second metric); with idCustomMetric replace that metric as a " +
		"whole (updateCustomMetric): formatType, decimals, and description that are not given are not sent, so " +
		"Matomo applies its own defaults (by its signature: number, 0 decimals, no description) and may reset a " +
		"value the metric had; read the metric with matomo.custom-metrics.list and pass every setting to keep. The " +
		"metric is read first and must exist, otherwise it is not found. Custom metrics belong to the instance, " +
		"not to a website, so the tool needs Super User rights (expected) and a connection whose targets name the " +
		"instance. Names, formulas, and descriptions are untrusted text. Needs the premium plugin CustomReports" +
		customMetricSentOnce,
	Tags: []string{"matomo", "custom-metrics", "superuser", "instance", "create"}, Provider: Provider,
	Risk: customMetricChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idCustomMetricSchema + `,` +
		`"name":{"type":"string","minLength":1,"maxLength":255},"formula":{"type":"string","minLength":1,"maxLength":2048},` +
		`"formatType":{"type":"string","pattern":"` + customMetricFormat + `"},` +
		`"decimals":{"type":"integer","minimum":0,"maximum":10},"description":{"type":"string","maxLength":2048}},` +
		`"required":["name","formula"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idCustomMetric":{"type":"integer"},` +
		`"created":{"type":"boolean"},"saved":{"type":"boolean"}},"required":["idCustomMetric","created","saved"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "idCustomMetric", Description: "ID of the metric to replace, from matomo.custom-metrics.list; a new " +
			"metric is created when omitted"},
		{Name: "name", Required: true, Description: "Name of the metric, 1 to 255 characters"},
		{Name: "formula", Required: true, Description: "Formula of the metric over metrics from " +
			"matomo.custom-report-options.list (custom_metric_metrics), 1 to 2048 characters; Matomo checks it"},
		{Name: "formatType", Description: "How the value is shown, up to 32 letters, digits, underscore, dot, or " +
			"hyphen; Matomo's default is number and it checks the value; reset to the default when omitted on a change"},
		{Name: "decimals", Description: "Number of decimals shown, 0 to 10; Matomo's default is 0; reset to the " +
			"default when omitted on a change"},
		{Name: "description", Description: "Description of the metric, up to 2048 characters; reset to the default " +
			"when omitted on a change"}},
	Fields: []capability.Field{
		{Name: "idCustomMetric", Description: "ID of the new metric, or the metric that was replaced"},
		{Name: "created", Description: "True when a new metric was created, false when one was replaced"},
		{Name: "saved", Description: "True once Matomo accepted the metric"}},
	Examples: []capability.Example{
		{Description: "Create a ratio metric", Arguments: json.RawMessage(`{"name":"Pages per visit","formula":"pageviews / nb_visits","decimals":2}`)},
		{Description: "Change metric 1", Arguments: json.RawMessage(`{"idCustomMetric":1,"name":"Pages per visit","formula":"pageviews / nb_visits"}`)}},
}

var customMetricsDelete = capability.Descriptor{
	ID: Provider + ".custom-metrics.delete", Version: 1, Title: "Delete a Matomo custom metric",
	Description: "Delete a custom metric of the instance; Qatlas Connect offers no way to restore it, and Matomo does not " +
		"document what happens to custom reports that use it. The metric is read first and must exist, otherwise it " +
		"is not found. Custom metrics belong to the instance, not to a website, so the tool needs Super User " +
		"rights (expected) and a connection whose targets name the instance. Needs the premium plugin " +
		"CustomReports; idempotent in effect" + customMetricSentOnce + "; offered only where a connection's tools " +
		"list names it",
	Tags: []string{"matomo", "custom-metrics", "superuser", "instance", "delete"}, Provider: Provider,
	RequiresToolAllowList: true, Risk: customMetricChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idCustomMetricSchema + `},` +
		`"required":["idCustomMetric"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idCustomMetric":{"type":"integer"},` +
		`"deleted":{"type":"boolean"}},"required":["idCustomMetric","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "idCustomMetric", Required: true, Description: "ID of the metric to delete, from matomo.custom-metrics.list"}},
	Fields: []capability.Field{
		{Name: "idCustomMetric", Description: "The metric that was deleted"},
		{Name: "deleted", Description: "True once Matomo accepted the deletion"}},
	Examples: []capability.Example{{Description: "Delete metric 1", Arguments: json.RawMessage(`{"idCustomMetric":1}`)}},
}

// CustomMetricSaved is the answer of custom-metrics.save.
type CustomMetricSaved struct {
	IDCustomMetric int  `json:"idCustomMetric"`
	Created        bool `json:"created"`
	Saved          bool `json:"saved"`
}

// CustomMetricDeleted is the answer of custom-metrics.delete.
type CustomMetricDeleted struct {
	IDCustomMetric int  `json:"idCustomMetric"`
	Deleted        bool `json:"deleted"`
}

func customMetricNotFound(op string) error {
	return &provider.Error{Class: provider.ClassNotFound, Op: op, Message: "Matomo has no such custom metric"}
}

func checkCustomMetricID(id *int) error {
	if id == nil || *id < 1 || *id > maxCustomMetricID {
		return invalidArgument("idCustomMetric must be a positive integer metric ID from matomo.custom-metrics.list")
	}
	return nil
}

// customMetricText accepts a text of at most limit characters without control characters.
func customMetricText(name, value string, limit int, required bool) error {
	if required && strings.TrimSpace(value) == "" {
		return invalidArgument(name + " must not be empty")
	}
	if utf8.RuneCountInString(value) > limit || strings.ContainsFunc(value, unicode.IsControl) {
		return invalidArgument(name + " must hold up to " + strconv.Itoa(limit) + " characters without control characters")
	}
	return nil
}

// provenMetric reads the metric list once so that the metric is proven to exist before a change follows.
func provenMetric(ctx context.Context, client *Client, op string, req requirement, id int) error {
	var answer json.RawMessage
	if err := client.call(ctx, op, req, "CustomReports.getCustomMetrics", url.Values{}, &answer); err != nil {
		return err
	}
	rows, valid := readRows[json.RawMessage](answer)
	if !valid {
		return provider.InvalidResponse(op, "Matomo returned an invalid custom metric list")
	}
	for _, row := range rows {
		var member struct {
			ID *loose `json:"idcustommetric"`
		}
		if json.Unmarshal(row, &member) == nil {
			if found := member.ID.number(); found != nil && *found == id {
				return nil
			}
		}
	}
	return customMetricNotFound(op)
}

func invokeCustomMetricsSave(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var args struct {
		IDCustomMetric *int    `json:"idCustomMetric"`
		Name           string  `json:"name"`
		Formula        string  `json:"formula"`
		FormatType     *string `json:"formatType"`
		Decimals       *int    `json:"decimals"`
		Description    *string `json:"description"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if args.IDCustomMetric != nil {
		if err := checkCustomMetricID(args.IDCustomMetric); err != nil {
			return nil, err
		}
	}
	if err := customMetricText("name", args.Name, maxCustomMetricName, true); err != nil {
		return nil, err
	}
	if err := customMetricText("formula", args.Formula, maxCustomMetricText, true); err != nil {
		return nil, err
	}
	params := url.Values{"name": {args.Name}, "formula": {args.Formula}}
	if args.FormatType != nil {
		if !customMetricFormatPattern.MatchString(*args.FormatType) {
			return nil, invalidArgument("formatType must hold 1 to 32 letters, digits, underscores, dots, or hyphens")
		}
		params.Set("formatType", *args.FormatType)
	}
	if args.Decimals != nil {
		if *args.Decimals < 0 || *args.Decimals > maxCustomMetricDecimal {
			return nil, invalidArgument("decimals must be between 0 and " + strconv.Itoa(maxCustomMetricDecimal))
		}
		params.Set("decimals", strconv.Itoa(*args.Decimals))
	}
	if args.Description != nil {
		if err := customMetricText("description", *args.Description, maxCustomMetricText, false); err != nil {
			return nil, err
		}
		params.Set("description", *args.Description)
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if err := requireInstance(bound); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := customReportsRequirement(customMetricsSave.ID)
	var answer json.RawMessage
	if args.IDCustomMetric == nil {
		const op = "add custom metric"
		if err := client.change(ctx, op, req, "CustomReports.addCustomMetric", params, customMetricSentOnce, &answer); err != nil {
			return nil, err
		}
		var id int
		if scalar(answer, &id) != nil || id < 1 {
			return nil, unclearChange(op, "Matomo returned no ID of the custom metric", customMetricSentOnce)
		}
		return &CustomMetricSaved{IDCustomMetric: id, Created: true, Saved: true}, nil
	}
	const op = "update custom metric"
	if err := provenMetric(ctx, client, op, req, *args.IDCustomMetric); err != nil {
		return nil, err
	}
	params.Set("idCustomMetric", strconv.Itoa(*args.IDCustomMetric))
	if err := client.change(ctx, op, req, "CustomReports.updateCustomMetric", params, customMetricSentOnce, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the custom metric", customMetricSentOnce)
	}
	return &CustomMetricSaved{IDCustomMetric: *args.IDCustomMetric, Saved: true}, nil
}

func invokeCustomMetricsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete custom metric"
	var args struct {
		IDCustomMetric *int `json:"idCustomMetric"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := checkCustomMetricID(args.IDCustomMetric); err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if err := requireInstance(bound); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := customReportsRequirement(customMetricsDelete.ID)
	if err := provenMetric(ctx, client, op, req, *args.IDCustomMetric); err != nil {
		return nil, err
	}
	var answer json.RawMessage
	params := url.Values{"idCustomMetric": {strconv.Itoa(*args.IDCustomMetric)}}
	if err := client.change(ctx, op, req, "CustomReports.deleteCustomMetric", params, customMetricSentOnce, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the deletion", customMetricSentOnce)
	}
	return &CustomMetricDeleted{IDCustomMetric: *args.IDCustomMetric, Deleted: true}, nil
}

func customMetricChangeOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: customMetricsSave, Handler: capability.Handler(invokeCustomMetricsSave)},
		{Descriptor: customMetricsDelete, Handler: capability.Handler(invokeCustomMetricsDelete)},
	}
}
