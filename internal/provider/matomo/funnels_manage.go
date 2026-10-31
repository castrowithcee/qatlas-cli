package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// Matomo documents 255 characters for a funnel description; the name is held to the same size.
	maxFunnelText = 255
)

var funnelSentOnce = sentOnceFor("funnel", "matomo.funnels.get")

// funnelChangeRisk is the risk of every funnel change: confirmed, open to the instance, and about the funnel
// definitions of a website.
func funnelChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: funnelsSensitive}
}

const (
	funnelStepsHint = "each step holds pattern_type (a value from matomo.funnel-options.list) and pattern, and " +
		"optionally name and required; the position of a step is its place in the list"
	savedFunnelOutput = `{"type":"object","properties":{"idSite":{"type":"integer"},"idFunnel":{"type":"integer"},` +
		`"saved":{"type":"boolean"}},"required":["idSite","saved"],"additionalProperties":false}`
	goalFunnelSavedOutput = `{"type":"object","properties":{"idSite":{"type":"integer"},"idGoal":{"type":"integer"},` +
		`"idFunnel":{"type":"integer"},"saved":{"type":"boolean"}},"required":["idSite","idGoal","saved"],` +
		`"additionalProperties":false}`
)

var funnelsSetForGoal = capability.Descriptor{
	ID: Provider + ".funnels.set-for-goal", Version: 1, Title: "Create or replace the funnel of a Matomo goal",
	Description: "Create the funnel of a goal of a website, or replace its definition and its activation; idGoal 0 " +
		"is the sales funnel. The steps are replaced as a whole. A goal the website does not have is not found. " +
		"Needs the plugin Funnels and, expected, a write right on the website; idempotent in effect" + funnelSentOnce,
	Tags: []string{"matomo", "funnels", "conversions", "update"}, Risk: funnelChangeRisk(capability.EffectUpdate,
		capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idGoalFunnel + `,` +
		`"isActivated":{"type":"boolean"},"steps":` + funnelStepsSchema(0, maxFunnelSteps) + `},` +
		`"required":["idGoal","isActivated"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(goalFunnelSavedOutput),
	Arguments: []capability.Argument{funnelSiteArgument(),
		{Name: "idGoal", Required: true, Description: "ID of the goal whose funnel to save, from matomo.goals.list; 0 " +
			"saves the sales funnel"},
		{Name: "isActivated", Required: true, Description: "True to activate the funnel so that it reports; an " +
			"activated funnel needs at least one step"},
		{Name: "steps", Description: "Up to 100 step definitions that replace the steps of the funnel; " +
			funnelStepsHint + "; none when omitted, which only a deactivated funnel allows"}},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the funnel belongs to"},
		{Name: "idGoal", Description: "The goal the funnel belongs to"},
		{Name: "idFunnel", Description: "ID of the created or updated funnel as Matomo answers it; absent when Matomo " +
			"gave none"},
		{Name: "saved", Description: "True once Matomo accepted the funnel"}},
	Examples: []capability.Example{{Description: "Activate a two-step funnel for goal 4 of website 1", Arguments: json.RawMessage(
		`{"idSite":1,"idGoal":4,"isActivated":true,"steps":[{"pattern_type":"path_equals","pattern":"/jobs"},` +
			`{"pattern_type":"path_equals","pattern":"/jobs/apply"}]}`)}},
}

var funnelsSave = capability.Descriptor{
	ID: Provider + ".funnels.save", Version: 1, Title: "Create or update a Matomo funnel without a goal",
	Description: "Create a funnel that is not tied to a goal, or replace the name, description, and steps of one; " +
		"idFunnel 0 creates a new funnel, so a repeated create makes a second funnel. A funnel of a goal is not " +
		"changed here, and a funnel the website does not have is not found. Needs the plugin Funnels and, " +
		"expected, a write right on the website" + funnelSentOnce,
	Tags: []string{"matomo", "funnels", "conversions", "update"}, Risk: funnelChangeRisk(capability.EffectUpdate,
		capability.IdempotencyNonIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,"idFunnel":{"type":"integer",` +
		`"minimum":0,"maximum":999999999},"funnelName":{"type":"string","minLength":1,"maxLength":255},` +
		`"description":{"type":"string","maxLength":255},"steps":` + funnelStepsSchema(1, maxFunnelSteps) + `},` +
		`"required":["idFunnel","funnelName","steps"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(savedFunnelOutput),
	Arguments: []capability.Argument{funnelSiteArgument(),
		{Name: "idFunnel", Required: true, Description: "ID of the funnel to update, from matomo.funnels.list; 0 " +
			"creates a new funnel"},
		{Name: "funnelName", Required: true, Description: "Display name of the funnel, 1 to 255 characters"},
		{Name: "steps", Required: true, Description: "1 to 100 step definitions that replace the steps of the " +
			"funnel; " + funnelStepsHint},
		{Name: "description", Description: "Description of the funnel, up to 255 characters; none when omitted"}},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the funnel belongs to"},
		{Name: "idFunnel", Description: "ID of the created or updated funnel as Matomo answers it; absent when Matomo " +
			"gave none"},
		{Name: "saved", Description: "True once Matomo accepted the funnel"}},
	Examples: []capability.Example{{Description: "Create a pricing funnel on website 1", Arguments: json.RawMessage(
		`{"idSite":1,"idFunnel":0,"funnelName":"Pricing signup","steps":[{"pattern_type":"path_equals",` +
			`"pattern":"/pricing"},{"pattern_type":"path_equals","pattern":"/signup"}]}`)}},
}

var funnelsDeleteForGoal = capability.Descriptor{
	ID: Provider + ".funnels.delete-for-goal", Version: 1, Title: "Delete the funnel of a Matomo goal",
	Description: "Delete the funnel of a goal of a website for good, with its steps; it cannot be undone. idGoal 0 is " +
		"the sales funnel. The funnel is read first and must belong to the website, otherwise it is not found. Needs " +
		"the plugin Funnels and, expected, a write right on the website; idempotent in effect" + funnelSentOnce +
		"; offered only where a connection's tools list names it",
	Tags: []string{"matomo", "funnels", "conversions", "delete"}, Risk: funnelChangeRisk(capability.EffectDelete,
		capability.IdempotencyIdempotent), Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idGoalFunnel + `},` +
		`"required":["idGoal"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},"idGoal":{"type":"integer"},` +
		`"idFunnel":{"type":"integer"},"name":{"type":"string"},"deleted":{"type":"boolean"}},` +
		`"required":["idSite","idGoal","idFunnel","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{funnelSiteArgument(),
		{Name: "idGoal", Required: true, Description: "ID of the goal whose funnel to delete, from matomo.goals.list; 0 " +
			"deletes the sales funnel"}},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the funnel belonged to"},
		{Name: "idGoal", Description: "The goal the funnel belonged to"},
		{Name: "idFunnel", Description: "ID of the deleted funnel"},
		{Name: "name", Description: "Name of the deleted funnel as it was read, untrusted"},
		{Name: "deleted", Description: "True once Matomo deleted the funnel"}},
	Examples: []capability.Example{{Description: "Delete the funnel of goal 4 of website 1", Arguments: json.RawMessage(`{"idSite":1,"idGoal":4}`)}},
}

var funnelsDelete = capability.Descriptor{
	ID: Provider + ".funnels.delete", Version: 1, Title: "Delete a Matomo funnel without a goal",
	Description: "Delete a funnel that is not tied to a goal for good, with its steps; it cannot be undone. The funnel " +
		"is read first and must belong to the website, otherwise it is not found; the funnel of a goal is not " +
		"deleted here. Needs the plugin Funnels and, expected, a write right on the website; idempotent in effect" +
		funnelSentOnce + "; offered only where a connection's tools list names it",
	Tags: []string{"matomo", "funnels", "conversions", "delete"}, Risk: funnelChangeRisk(capability.EffectDelete,
		capability.IdempotencyIdempotent), Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idFunnelSchema + `},` +
		`"required":["idFunnel"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},"idFunnel":{"type":"integer"},` +
		`"name":{"type":"string"},"deleted":{"type":"boolean"}},"required":["idSite","idFunnel","deleted"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{funnelSiteArgument(),
		{Name: "idFunnel", Required: true, Description: "ID of the funnel to delete, from matomo.funnels.list"}},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the funnel belonged to"},
		{Name: "idFunnel", Description: "ID of the deleted funnel"},
		{Name: "name", Description: "Name of the deleted funnel as it was read, untrusted"},
		{Name: "deleted", Description: "True once Matomo deleted the funnel"}},
	Examples: []capability.Example{{Description: "Delete funnel 7 of website 1", Arguments: json.RawMessage(`{"idSite":1,"idFunnel":7}`)}},
}

// FunnelGoalSaved is the answer of funnels.set-for-goal.
type FunnelGoalSaved struct {
	IDSite   int  `json:"idSite"`
	IDGoal   int  `json:"idGoal"`
	IDFunnel *int `json:"idFunnel,omitempty"`
	Saved    bool `json:"saved"`
}

// FunnelSaved is the answer of funnels.save.
type FunnelSaved struct {
	IDSite   int  `json:"idSite"`
	IDFunnel *int `json:"idFunnel,omitempty"`
	Saved    bool `json:"saved"`
}

// FunnelGoalDeleted is the answer of funnels.delete-for-goal.
type FunnelGoalDeleted struct {
	IDSite   int    `json:"idSite"`
	IDGoal   int    `json:"idGoal"`
	IDFunnel int    `json:"idFunnel"`
	Name     string `json:"name,omitempty"`
	Deleted  bool   `json:"deleted"`
}

// FunnelDeleted is the answer of funnels.delete.
type FunnelDeleted struct {
	IDSite   int    `json:"idSite"`
	IDFunnel int    `json:"idFunnel"`
	Name     string `json:"name,omitempty"`
	Deleted  bool   `json:"deleted"`
}

func funnelGoalIDOf(given *int) (int, error) {
	if given == nil || *given < 0 || *given > maxFunnelID {
		return 0, invalidArgument("idGoal must be a goal ID, or 0 for the sales funnel")
	}
	return *given, nil
}

func funnelIDOf(given *int, allowNew bool) (int, error) {
	if given == nil || *given > maxFunnelID || *given < 1 && !(allowNew && *given == 0) {
		if allowNew {
			return 0, invalidArgument("idFunnel must be a funnel ID from matomo.funnels.list, or 0 for a new funnel")
		}
		return 0, invalidArgument("idFunnel must be a positive integer funnel ID from matomo.funnels.list")
	}
	return *given, nil
}

// savedFunnelID reads what a save answers: the funnel ID, or the generic success form. ok is false for an
// answer that proves neither.
func savedFunnelID(answer json.RawMessage) (id *int, ok bool) {
	var number int
	if scalar(answer, &number) == nil && number >= 1 {
		return &number, true
	}
	return nil, isSuccessAnswer(answer)
}

// goalFunnelRead names the method that reads the funnel of a goal.
func goalFunnelRead(site, goal int) (string, url.Values) {
	params := url.Values{"idSite": {strconv.Itoa(site)}}
	if goal == 0 {
		return "Funnels.getSalesFunnelForSite", params
	}
	params.Set("idGoal", strconv.Itoa(goal))
	return "Funnels.getGoalFunnel", params
}

// fetchPlainFunnel is the proof read of a funnel without a goal: the funnel must belong to the website, and
// a funnel of a goal is refused because it has its own tools.
func (c *Client) fetchPlainFunnel(ctx context.Context, op string, req requirement, site, id int) (Funnel, error) {
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idFunnel": {strconv.Itoa(id)}}
	funnel, err := c.fetchFunnel(ctx, op, req, "Funnels.getFunnel", params, site, &id, nil)
	if err != nil {
		return Funnel{}, err
	}
	if funnel.IDGoal != nil {
		return Funnel{}, invalidArgument("this funnel belongs to a goal; change it with the tools for the funnel of a goal")
	}
	return funnel, nil
}

func invokeFunnelsSetForGoal(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "set funnel for goal"
	var args struct {
		IDSite      *int              `json:"idSite"`
		IDGoal      *int              `json:"idGoal"`
		IsActivated *bool             `json:"isActivated"`
		Steps       []funnelStepInput `json:"steps"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	goal, err := funnelGoalIDOf(args.IDGoal)
	if err != nil {
		return nil, err
	}
	if args.IsActivated == nil {
		return nil, invalidArgument("isActivated must be true or false")
	}
	minSteps := 0
	if *args.IsActivated {
		minSteps = 1
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idGoal": {strconv.Itoa(goal)},
		"isActivated": {flagValue(args.IsActivated)}}
	if err := funnelStepParams(params, args.Steps, minSteps, maxFunnelSteps); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := funnelsRequirement(funnelsSetForGoal.ID)
	// A funnel is keyed by website and goal, and only Matomo knows the goals of a website: one read proves the goal
	// before the funnel is written. The sales funnel (0) is no goal and is bound by the website alone.
	if goal > 0 {
		if _, err := client.fetchGoal(ctx, op, req, site, goal); err != nil {
			return nil, err
		}
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, req, "Funnels.setGoalFunnel", params, funnelSentOnce, &answer); err != nil {
		return nil, err
	}
	id, ok := savedFunnelID(answer)
	if !ok {
		return nil, unclearChange(op, "Matomo returned no confirmation of the funnel", funnelSentOnce)
	}
	return &FunnelGoalSaved{IDSite: site, IDGoal: goal, IDFunnel: id, Saved: true}, nil
}

func invokeFunnelsSave(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "save funnel"
	var args struct {
		IDSite      *int              `json:"idSite"`
		IDFunnel    *int              `json:"idFunnel"`
		FunnelName  string            `json:"funnelName"`
		Description *string           `json:"description"`
		Steps       []funnelStepInput `json:"steps"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	id, err := funnelIDOf(args.IDFunnel, true)
	if err != nil {
		return nil, err
	}
	if args.FunnelName == "" || utf8.RuneCountInString(args.FunnelName) > maxFunnelText ||
		args.Description != nil && utf8.RuneCountInString(*args.Description) > maxFunnelText {
		return nil, invalidArgument("funnelName holds 1 to 255 characters and description at most 255")
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idFunnel": {strconv.Itoa(id)}, "funnelName": {args.FunnelName}}
	if args.Description != nil {
		params.Set("description", *args.Description)
	}
	if err := funnelStepParams(params, args.Steps, 1, maxFunnelSteps); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := funnelsRequirement(funnelsSave.ID)
	// A funnel ID counts across websites. An update is bound by one read that must show the funnel on this website;
	// a new funnel (0) is bound by the website alone.
	if id > 0 {
		if _, err := client.fetchPlainFunnel(ctx, op, req, site, id); err != nil {
			return nil, err
		}
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, req, "Funnels.saveNonGoalFunnel", params, funnelSentOnce, &answer); err != nil {
		return nil, err
	}
	saved, ok := savedFunnelID(answer)
	if !ok {
		return nil, unclearChange(op, "Matomo returned no confirmation of the funnel", funnelSentOnce)
	}
	if saved == nil && id > 0 {
		saved = &id
	}
	return &FunnelSaved{IDSite: site, IDFunnel: saved, Saved: true}, nil
}

func invokeFunnelsDeleteForGoal(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete funnel for goal"
	var args struct {
		IDSite *int `json:"idSite"`
		IDGoal *int `json:"idGoal"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	goal, err := funnelGoalIDOf(args.IDGoal)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := funnelsRequirement(funnelsDeleteForGoal.ID)
	method, readParams := goalFunnelRead(site, goal)
	funnel, err := client.fetchFunnel(ctx, op, req, method, readParams, site, nil, &goal)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idGoal": {strconv.Itoa(goal)}}
	var answer json.RawMessage
	if err := client.change(ctx, op, req, "Funnels.deleteGoalFunnel", params, funnelSentOnce, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the deletion", funnelSentOnce)
	}
	return &FunnelGoalDeleted{IDSite: site, IDGoal: goal, IDFunnel: funnel.IDFunnel, Name: funnel.Name, Deleted: true}, nil
}

func invokeFunnelsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete funnel"
	var args struct {
		IDSite   *int `json:"idSite"`
		IDFunnel *int `json:"idFunnel"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	id, err := funnelIDOf(args.IDFunnel, false)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := funnelsRequirement(funnelsDelete.ID)
	funnel, err := client.fetchPlainFunnel(ctx, op, req, site, id)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idFunnel": {strconv.Itoa(id)}}
	var answer json.RawMessage
	if err := client.change(ctx, op, req, "Funnels.deleteNonGoalFunnel", params, funnelSentOnce, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the deletion", funnelSentOnce)
	}
	return &FunnelDeleted{IDSite: site, IDFunnel: id, Name: funnel.Name, Deleted: true}, nil
}

func funnelsChangeOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: funnelsSetForGoal, Handler: capability.Handler(invokeFunnelsSetForGoal)},
		{Descriptor: funnelsSave, Handler: capability.Handler(invokeFunnelsSave)},
		{Descriptor: funnelsDeleteForGoal, Handler: capability.Handler(invokeFunnelsDeleteForGoal)},
		{Descriptor: funnelsDelete, Handler: capability.Handler(invokeFunnelsDelete)},
	}
}
