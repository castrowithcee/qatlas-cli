package cli

// Provider conformance: every provider that defaultRegistry wires runs through the same checks of its
// metadata, of each of its descriptors, of the refusals the application core applies to them, and of the
// CLI and MCP discovery views of the whole catalog. A provider joins these checks through its registration
// in defaultRegistry alone; nothing in this file names a provider. Its own tests keep proving what only it
// knows, such as its HTTP contract, its schemas in detail, and its redaction.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// TestProviderConformance runs every shipped provider and every one of its operations through the shared
// checks. A failure names the provider and the operation, and says which contract it breaks.
func TestProviderConformance(t *testing.T) {
	reg := defaultRegistry()
	providers := reg.ProviderMetadataAll()
	if len(providers) == 0 {
		t.Fatal("the shipped registry has no provider")
	}
	for _, descriptor := range reg.All() {
		if _, ok := reg.ProviderMetadata(descriptor.Provider); !ok {
			t.Errorf("operation %s belongs to provider %q, which registered no metadata", descriptor.ID,
				descriptor.Provider)
		}
	}
	for _, metadata := range providers {
		t.Run(metadata.ID, func(t *testing.T) {
			descriptors := reg.Provider(metadata.ID)
			reportViolations(t, providerViolations(metadata, descriptors))
			for _, descriptor := range descriptors {
				t.Run(descriptor.ID, func(t *testing.T) {
					reportViolations(t, descriptorViolations(descriptor))
					reportViolations(t, routingViolations(metadata, descriptor))
				})
			}
		})
	}
}

// The CLI and the MCP broker publish the same complete catalog: the same namespaces, the same tools of each
// namespace, and the same contract of every tool, all of them equal to what the registry holds. Discovery
// reads no secret.
func TestProviderConformanceDiscoveryParity(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	path := writeConfig(t, "version: 1\ndefaults: {}\n")
	reg := defaultRegistry()
	var reads atomic.Int32
	cliJSON := func(args ...string) []byte {
		t.Helper()
		code, stdout, stderr := runTools(t, &reads, append(args, "--config", path, "--output", "json")...)
		if code != exitOK || stderr != "" {
			t.Fatalf("CLI %v exit=%d stderr=%q", args, code, stderr)
		}
		return []byte(stdout)
	}
	mcpOptions := func() *Options { return &Options{Config: path, Redactor: &redact.Redactor{}} }

	var namespaces struct {
		Providers []application.ProviderSummary `json:"providers"`
	}
	decodeRaw(t, cliJSON("providers"), &namespaces)
	wantNamespaces := []application.ProviderSummary{}
	for _, metadata := range reg.ProviderMetadataAll() {
		wantNamespaces = append(wantNamespaces, application.ProviderSummary{
			Provider: metadata.ID, Tools: len(reg.Provider(metadata.ID)),
		})
	}
	if !reflect.DeepEqual(namespaces.Providers, wantNamespaces) {
		t.Errorf("CLI providers = %+v, want %+v", namespaces.Providers, wantNamespaces)
	}

	// Without a connection nothing is offered, so the default catalog is empty and the complete one names
	// every tool with the same reason on both surfaces.
	noConnection := config.RefusalNoConnection
	for _, metadata := range reg.ProviderMetadataAll() {
		want := []application.ToolSummary{}
		for _, descriptor := range reg.Provider(metadata.ID) {
			want = append(want, application.ToolSummary{
				ID: descriptor.ID, Title: descriptor.Title, Effect: descriptor.Risk.Effect, Reason: &noConnection,
			})
		}
		if indexed := toolSummaries(t, string(cliJSON("tools", metadata.ID, "--all"))); !reflect.DeepEqual(indexed, want) {
			t.Errorf("CLI tools %s = %+v, want %+v", metadata.ID, indexed, want)
		}

		searched := []application.ToolSummary{}
		arguments := `{"provider":"` + metadata.ID + `","all":true}`
		for {
			input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{` + mcpTestMeta +
				`,"name":"qatlas.search","arguments":` + arguments + `}}` + "\n"
			responses, stderr := runMCPWithOptions(t, reg, input, mcpOptions())
			result := toolResultFrom(t, responses["1"])
			if stderr != "" || result.IsError {
				t.Fatalf("MCP search %s: stderr=%q result=%+v", arguments, stderr, result)
			}
			var page application.SearchResponse
			decodeRaw(t, result.Structured, &page)
			for _, hit := range page.Operations {
				reason := hit.Reason
				searched = append(searched, application.ToolSummary{
					ID: hit.ID, Title: hit.Title, Effect: hit.Effect, Connections: strings.Join(hit.Connections, " "),
					Reason: &reason,
				})
			}
			if !page.HasMore {
				break
			}
			arguments = `{"provider":"` + metadata.ID + `","all":true,"cursor":"` + page.NextCursor + `"}`
		}
		if !reflect.DeepEqual(searched, want) {
			t.Errorf("MCP search %s = %+v, want %+v", metadata.ID, searched, want)
		}
	}

	for _, descriptor := range reg.All() {
		registered, err := json.Marshal(descriptor)
		if err != nil {
			t.Fatal(err)
		}
		input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{` + mcpTestMeta +
			`,"name":"qatlas.describe","arguments":{"operation":"` + descriptor.ID + `","version":` +
			strconv.Itoa(descriptor.Version) + `}}}` + "\n"
		described, stderr := runMCPWithOptions(t, reg, input, mcpOptions())
		if stderr != "" {
			t.Fatalf("MCP describe %s stderr = %q", descriptor.ID, stderr)
		}
		byCLI := jsonMember(t, cliJSON("describe", descriptor.ID), "tool")
		byMCP := jsonMember(t, toolResultFrom(t, described["1"]).Structured, "operation")
		if !jsonEqual(byCLI, registered) || !jsonEqual(byMCP, registered) {
			t.Errorf("%s: CLI tool = %s, MCP describe = %s, registered = %s", descriptor.ID, byCLI, byMCP,
				registered)
		}
	}
	if reads.Load() != 0 {
		t.Errorf("discovery read %d secrets, want none", reads.Load())
	}
}

// Every check of this suite must be able to fail. Each case breaks exactly one invariant of an otherwise
// conformant synthetic provider and must be reported by the check that owns it.
func TestProviderConformanceChecksDetectViolations(t *testing.T) {
	// An idempotent change may be classified as needing no confirmation.
	unconfirmedUpdate := conformantDescriptor()
	unconfirmedUpdate.ID = "sample.items.update"
	unconfirmedUpdate.Title, unconfirmedUpdate.Description = "Update a sample item", "Renames one sample item."
	unconfirmedUpdate.Risk.Effect = capability.EffectUpdate
	unconfirmedUpdate.Risk.Idempotency = capability.IdempotencyIdempotent
	unconfirmedUpdate.Risk.Confirmation = capability.ConfirmationNone
	for _, descriptor := range []capability.Descriptor{
		conformantDescriptor(), conformantRead(), conformantAllowListed(), unconfirmedUpdate,
	} {
		if violations := descriptorViolations(descriptor); len(violations) != 0 {
			t.Errorf("%s: descriptor violations = %q, want none", descriptor.ID, violations)
		}
		if violations := routingViolations(conformantMetadata(), descriptor); len(violations) != 0 {
			t.Errorf("%s: routing violations = %q, want none", descriptor.ID, violations)
		}
	}
	if violations := providerViolations(conformantMetadata(), conformantOperations()); len(violations) != 0 {
		t.Errorf("provider violations = %q, want none", violations)
	}

	descriptorCases := []struct {
		name   string
		mutate func(*capability.Descriptor)
		want   string
	}{
		{"an ID of another provider", func(d *capability.Descriptor) { d.ID = "other.items.create" }, "operation ID"},
		{"an ID of two segments", func(d *capability.Descriptor) { d.ID = "sample.create" }, "operation ID"},
		{"an ID with an invalid segment", func(d *capability.Descriptor) { d.ID = "sample.Items.create" }, "operation ID"},
		{"no provider", func(d *capability.Descriptor) { d.Provider = "" }, "operation ID"},
		{"no version", func(d *capability.Descriptor) { d.Version = 0 }, "version"},
		{"no title", func(d *capability.Descriptor) { d.Title = " " }, "title"},
		{"no description", func(d *capability.Descriptor) { d.Description = "" }, "description"},
		{"an input schema that is no object", func(d *capability.Descriptor) {
			d.InputSchema = json.RawMessage(`{"type":"array"}`)
		}, "input schema"},
		{"an input schema that admits undeclared arguments", func(d *capability.Descriptor) {
			d.InputSchema = json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`)
		}, "additionalProperties"},
		{"an input schema that is no JSON", func(d *capability.Descriptor) { d.InputSchema = json.RawMessage(`{`) },
			"input schema"},
		{"an output schema without a type", func(d *capability.Descriptor) { d.OutputSchema = json.RawMessage(`{}`) },
			"output schema"},
		{"an unknown effect", func(d *capability.Descriptor) { d.Risk.Effect = "write" }, "effect"},
		{"no idempotency", func(d *capability.Descriptor) { d.Risk.Idempotency = "" }, "idempotency"},
		{"no confirmation", func(d *capability.Descriptor) { d.Risk.Confirmation = "" }, "confirmation"},
		{"a non-idempotent change without confirmation", func(d *capability.Descriptor) {
			d.Risk.Confirmation = capability.ConfirmationNone
		}, "must require confirmation"},
		{"a change of unknown idempotency without confirmation", func(d *capability.Descriptor) {
			d.Risk.Idempotency = capability.IdempotencyUnknown
			d.Risk.Confirmation = capability.ConfirmationNone
		}, "must require confirmation"},
		{"a delete without confirmation", func(d *capability.Descriptor) {
			d.Risk.Effect = capability.EffectDelete
			d.Risk.Idempotency = capability.IdempotencyIdempotent
			d.Risk.Confirmation = capability.ConfirmationNone
		}, "must require confirmation"},
		{"a mutation declared safe", func(d *capability.Descriptor) {
			d.Risk.Idempotency = capability.IdempotencySafe
		}, "cannot be safe"},
		{"a read that is not safe", func(d *capability.Descriptor) {
			d.Risk = capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencyIdempotent,
				Confirmation: capability.ConfirmationNone, DataSensitivity: "sample-items"}
		}, "must be safe"},
		{"no data classification", func(d *capability.Descriptor) { d.Risk.DataSensitivity = "" },
			"data sensitivity"},
		{"an argument outside the input schema", func(d *capability.Descriptor) {
			d.Arguments = append(d.Arguments, capability.Argument{Name: "color"})
		}, "argument"},
		{"a required argument listed as optional", func(d *capability.Descriptor) { d.Arguments[0].Required = false },
			"argument"},
	}
	for _, tt := range descriptorCases {
		t.Run("descriptor with "+tt.name, func(t *testing.T) {
			descriptor := conformantDescriptor()
			tt.mutate(&descriptor)
			assertViolation(t, descriptorViolations(descriptor), tt.want)
		})
	}

	routingCases := []struct {
		name   string
		mutate func(*capability.Descriptor)
		want   string
	}{
		{"an example outside the input schema", func(d *capability.Descriptor) {
			d.Examples = []capability.Example{{Arguments: json.RawMessage(`{"name":"UPPER"}`)}}
		}, "example 1"},
		{"no example and no derivable arguments", func(d *capability.Descriptor) {
			d.Examples = nil
			d.InputSchema = json.RawMessage(`{"type":"object","properties":{"name":{"type":"string",` +
				`"pattern":"^x{3}$"}},"required":["name"],"additionalProperties":false}`)
		}, "no example"},
	}
	for _, tt := range routingCases {
		t.Run("routing with "+tt.name, func(t *testing.T) {
			descriptor := conformantDescriptor()
			tt.mutate(&descriptor)
			assertViolation(t, routingViolations(conformantMetadata(), descriptor), tt.want)
		})
	}

	providerCases := []struct {
		name       string
		mutate     func(*config.ProviderMetadata)
		operations []capability.Descriptor
		want       string
	}{
		{"no name", func(m *config.ProviderMetadata) { m.Name = "" }, nil, "name"},
		{"no operation", func(*config.ProviderMetadata) {}, []capability.Descriptor{}, "no operation"},
		{"supported permissions without a registered effect", func(m *config.ProviderMetadata) {
			m.SupportedPermissions = []config.Permission{config.PermissionRead}
		}, nil, "supported permissions"},
		{"supported permissions with an unregistered effect", func(m *config.ProviderMetadata) {
			m.SupportedPermissions = append(m.SupportedPermissions, config.PermissionDelete)
		}, nil, "supported permissions"},
		{"a tool list that misses an operation", func(m *config.ProviderMetadata) { m.Tools = m.Tools[:1] }, nil,
			"tools"},
		{"a default permission it does not support", func(m *config.ProviderMetadata) {
			m.DefaultPermissions = []config.Permission{config.PermissionDelete}
		}, nil, "default permission"},
	}
	for _, tt := range providerCases {
		t.Run("provider with "+tt.name, func(t *testing.T) {
			metadata := conformantMetadata()
			tt.mutate(&metadata)
			operations := tt.operations
			if operations == nil {
				operations = conformantOperations()
			}
			assertViolation(t, providerViolations(metadata, operations), tt.want)
		})
	}

	// A connection that holds every supported permission must be offered every operation. Declaring fewer
	// supported permissions than the operations need is caught by that behaviour alone, not only by the
	// comparison with the registered effects.
	metadata := conformantMetadata()
	metadata.SupportedPermissions = []config.Permission{config.PermissionRead}
	assertViolation(t, permissionViolations(metadata, conformantOperations()), "is not offered")
}

// descriptorViolations checks the static contract of one operation: a provider-qualified ID, a positive
// version, the discovery texts, a closed object input schema, a typed output schema, a complete and
// consistent risk, a data classification, and CLI arguments that match the schema.
//
// A risk is consistent when a read is safe and a change is not, and when a change confirms as its risk
// demands: a delete, and a change that is not idempotent or whose idempotency is unknown, requires
// confirmation. An idempotent create, update or execute may be classified as needing none.
func descriptorViolations(d capability.Descriptor) []string {
	var violations []string
	fail := func(format string, args ...any) { violations = append(violations, fmt.Sprintf(format, args...)) }

	segments := strings.Split(d.ID, ".")
	validID := len(segments) == 3 && segments[0] == d.Provider
	for _, segment := range segments {
		validID = validID && operationSegment.MatchString(segment)
	}
	if !validID {
		fail("operation ID %q is not <provider>.<object>.<action> of provider %q", d.ID, d.Provider)
	}
	if d.Version < 1 {
		fail("version %d is not positive", d.Version)
	}
	if strings.TrimSpace(d.Title) == "" {
		fail("title is empty")
	}
	if strings.TrimSpace(d.Description) == "" {
		fail("description is empty")
	}

	var input struct {
		Type                 string                     `json:"type"`
		Properties           map[string]json.RawMessage `json:"properties"`
		Required             []string                   `json:"required"`
		AdditionalProperties *bool                      `json:"additionalProperties"`
	}
	if err := json.Unmarshal(d.InputSchema, &input); err != nil || input.Type != "object" {
		fail("input schema %s is not an object schema", d.InputSchema)
	} else if input.AdditionalProperties == nil || *input.AdditionalProperties {
		fail("input schema must set additionalProperties to false, so no undeclared argument reaches the provider")
	}
	var output struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(d.OutputSchema, &output); err != nil || output.Type == "" {
		fail("output schema %s declares no type", d.OutputSchema)
	}

	risk := d.Risk
	switch risk.Effect {
	case capability.EffectRead, capability.EffectCreate, capability.EffectUpdate, capability.EffectDelete,
		capability.EffectExecute:
	default:
		fail("effect %q is unknown", risk.Effect)
	}
	switch risk.Idempotency {
	case capability.IdempotencySafe, capability.IdempotencyIdempotent, capability.IdempotencyNonIdempotent,
		capability.IdempotencyUnknown:
	default:
		fail("idempotency %q is unknown", risk.Idempotency)
	}
	switch risk.Confirmation {
	case capability.ConfirmationNone, capability.ConfirmationRequired:
	default:
		fail("confirmation %q is unknown", risk.Confirmation)
	}
	switch {
	case risk.Effect == capability.EffectRead && risk.Idempotency != capability.IdempotencySafe:
		fail("a read must be safe, not %q", risk.Idempotency)
	case risk.Effect != capability.EffectRead && risk.Idempotency == capability.IdempotencySafe:
		fail("effect %s cannot be safe", risk.Effect)
	}
	risky := risk.Effect == capability.EffectDelete || risk.Idempotency == capability.IdempotencyNonIdempotent ||
		risk.Idempotency == capability.IdempotencyUnknown
	if risk.Effect != capability.EffectRead && risky && risk.Confirmation != capability.ConfirmationRequired {
		fail("a %s that is %s must require confirmation, not %q", risk.Effect, risk.Idempotency, risk.Confirmation)
	}
	if strings.TrimSpace(risk.DataSensitivity) == "" {
		fail("data sensitivity is empty")
	}

	required := map[string]bool{}
	for _, name := range input.Required {
		required[name] = true
	}
	for _, argument := range d.Arguments {
		if _, ok := input.Properties[argument.Name]; !ok {
			fail("argument %q is not a property of the input schema", argument.Name)
		} else if argument.Required != required[argument.Name] {
			fail("argument %q is required=%t, the input schema says %t", argument.Name, argument.Required,
				required[argument.Name])
		}
	}
	return violations
}

var operationSegment = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

// providerViolations checks the configuration contract of one provider against its registered operations:
// a provider offers at least one operation, its supported permissions are exactly the effects of those
// operations, its tool list is exactly those operations, its default permissions are supported ones, and a
// connection's permissions offer what the supported permissions promise.
func providerViolations(metadata config.ProviderMetadata, descriptors []capability.Descriptor) []string {
	var violations []string
	fail := func(format string, args ...any) { violations = append(violations, fmt.Sprintf(format, args...)) }

	if strings.TrimSpace(metadata.Name) == "" {
		fail("provider %q has no display name", metadata.ID)
	}
	if len(descriptors) == 0 {
		fail("provider %q registers no operation and is missing from discovery", metadata.ID)
	}
	effects := map[config.Permission]bool{}
	tools := []config.ToolMetadata{}
	for _, descriptor := range descriptors {
		effects[config.Permission(descriptor.Risk.Effect)] = true
		tools = append(tools, descriptor.Tool())
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].ID < tools[j].ID })
	supported := []config.Permission{}
	for _, permission := range config.Permissions() {
		if effects[permission] {
			supported = append(supported, permission)
		}
	}
	if !reflect.DeepEqual(append([]config.Permission{}, metadata.SupportedPermissions...), supported) {
		fail("supported permissions %v, want the registered effects %v", metadata.SupportedPermissions, supported)
	}
	if !reflect.DeepEqual(append([]config.ToolMetadata{}, metadata.Tools...), tools) {
		fail("tools %+v, want the registered operations %+v", metadata.Tools, tools)
	}
	for _, permission := range metadata.DefaultPermissions {
		if !effects[permission] {
			fail("default permission %q is not a registered effect", permission)
		}
	}
	return append(violations, permissionViolations(metadata, descriptors)...)
}

// permissionViolations asks the application core which operations two connections are offered: one that
// holds exactly the supported permissions and one that holds every other permission. Both list every
// operation in their tools list, so only the permissions decide. The first must be offered every
// operation, the second none.
func permissionViolations(metadata config.ProviderMetadata, descriptors []capability.Descriptor) []string {
	if len(descriptors) == 0 {
		return nil
	}
	ids := make([]string, len(descriptors))
	for i, descriptor := range descriptors {
		ids[i] = descriptor.ID
	}
	var unsupported []config.Permission
	for _, permission := range config.Permissions() {
		if !slices.Contains(metadata.SupportedPermissions, permission) {
			unsupported = append(unsupported, permission)
		}
	}
	harness, err := newConformanceHarness(metadata, descriptors, map[string]config.Connection{
		"supported":   {Permissions: append([]config.Permission{}, metadata.SupportedPermissions...), Tools: ids},
		"unsupported": {Permissions: append([]config.Permission{}, unsupported...), Tools: ids},
	})
	if err != nil {
		return []string{err.Error()}
	}
	var violations []string
	for _, connection := range []string{"supported", "unsupported"} {
		response, err := harness.core.Tools(application.SearchRequest{Connection: connection})
		if err != nil {
			violations = append(violations, fmt.Sprintf("tools of connection %s: %v", connection, err))
			continue
		}
		offered := map[string]bool{}
		for _, tool := range response.Tools {
			offered[tool.ID] = true
		}
		for _, descriptor := range descriptors {
			switch {
			case connection == "supported" && !offered[descriptor.ID]:
				violations = append(violations, fmt.Sprintf("%s %s is not offered to a connection with the "+
					"supported permissions %v", descriptor.Risk.Effect, descriptor.ID, metadata.SupportedPermissions))
			case connection == "unsupported" && offered[descriptor.ID]:
				violations = append(violations, fmt.Sprintf("%s %s is offered to a connection with only the "+
					"unsupported permissions %v", descriptor.Risk.Effect, descriptor.ID, unsupported))
			}
		}
	}
	return violations
}

// routingViolations checks what the application core does with one descriptor. The descriptor is
// registered in a registry of its own, where a counting handler stands in for the provider, and the core
// is asked through four connections that differ only in their local allow-lists:
//
//   - listed names the operation in its tools list and holds its effect, so it is offered the operation;
//   - unlisted has an explicit empty tools list, so it is not;
//   - denied names the operation but holds every other effect, so it is not;
//   - open has no tools list, so it is offered the operation unless the operation requires an allow-list.
//
// An operation that requires confirmation is refused without it before the handler runs, and one that
// requires none runs without it. Every other refusal never reaches the handler either, the core itself
// never reads a secret, discovery offers the operation on exactly the connections that invoke accepts,
// and every example satisfies the input schema.
func routingViolations(metadata config.ProviderMetadata, d capability.Descriptor) []string {
	effect := config.Permission(d.Risk.Effect)
	others := []config.Permission{}
	for _, permission := range config.Permissions() {
		if permission != effect {
			others = append(others, permission)
		}
	}
	offers := map[string]bool{"listed": true, "unlisted": false, "denied": false, "open": !d.RequiresToolAllowList}
	harness, err := newConformanceHarness(metadata, []capability.Descriptor{d}, map[string]config.Connection{
		"listed":   {Permissions: []config.Permission{effect}, Tools: []string{d.ID}},
		"unlisted": {Permissions: []config.Permission{effect}, Tools: []string{}},
		"denied":   {Permissions: others, Tools: []string{d.ID}},
		"open":     {Permissions: []config.Permission{effect}},
	})
	if err != nil {
		return []string{err.Error()}
	}
	var violations []string
	fail := func(format string, args ...any) { violations = append(violations, fmt.Sprintf(format, args...)) }

	for i, example := range d.Examples {
		_, calls, err := harness.invoke(d, "denied", example.Arguments, true)
		var unsupported *capability.UnsupportedError
		if !errors.As(err, &unsupported) || calls != 0 {
			fail("example %d: %v, want it to satisfy the input schema", i+1, err)
		}
	}
	arguments, ok := conformanceArguments(d)
	if !ok {
		fail("operation has no example and no argument value can be derived from its input schema")
		return violations
	}

	for _, connection := range []string{"listed", "unlisted", "denied", "open"} {
		if offers[connection] {
			_, calls, err := harness.invoke(d, connection, arguments, false)
			var confirmation *application.ConfirmationRequiredError
			switch required := d.Risk.Confirmation == capability.ConfirmationRequired; {
			case required && (!errors.As(err, &confirmation) || calls != 0):
				fail("unconfirmed %s on connection %s: error %v after %d handler calls, want "+
					"confirmation-required before the handler", d.Risk.Effect, connection, err, calls)
			case !required && calls != 1:
				fail("unconfirmed %s on connection %s that needs no confirmation reached the handler %d "+
					"times, want once: %v", d.Risk.Effect, connection, calls, err)
			}
			if _, calls, err := harness.invoke(d, connection, arguments, true); calls != 1 {
				fail("confirmed request on connection %s reached the handler %d times, want once: %v",
					connection, calls, err)
			}
		} else {
			_, calls, err := harness.invoke(d, connection, arguments, true)
			var unsupported *capability.UnsupportedError
			if !errors.As(err, &unsupported) || calls != 0 {
				fail("request on connection %s: error %v after %d handler calls, want unsupported-capability "+
					"before the handler", connection, err, calls)
			}
		}

		_, err := harness.core.Describe(application.DescribeRequest{
			Operation: d.ID, Version: d.Version, Connection: connection,
		})
		if (err == nil) != offers[connection] {
			fail("describe on connection %s: error %v, want offered=%t", connection, err, offers[connection])
		}
		response, err := harness.core.Search(application.SearchRequest{Connection: connection})
		found := err == nil && len(response.Operations) == 1 && response.Operations[0].ID == d.ID
		if found != offers[connection] {
			fail("search on connection %s: %+v, %v, want offered=%t", connection, response.Operations, err,
				offers[connection])
		}
	}
	if reads := harness.secretReads.Load(); reads != 0 {
		fail("the application core read %d secrets, want none", reads)
	}
	return violations
}

// conformanceHarness is an application core over a registry that holds only the given operations of one
// provider. Its handlers count their calls instead of contacting the provider, and its credential resolver
// counts every secret lookup.
type conformanceHarness struct {
	core         *application.Core
	handlerCalls atomic.Int32
	secretReads  atomic.Int32
}

func newConformanceHarness(metadata config.ProviderMetadata, descriptors []capability.Descriptor,
	connections map[string]config.Connection) (*conformanceHarness, error) {
	harness := &conformanceHarness{}
	registry := capability.NewRegistry()
	if err := registry.RegisterProvider(metadata, nil); err != nil {
		return nil, fmt.Errorf("register provider metadata: %w", err)
	}
	handler := func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor,
		json.RawMessage) (any, error) {
		harness.handlerCalls.Add(1)
		return map[string]any{}, nil
	}
	for _, descriptor := range descriptors {
		if err := registry.Register(metadata.ID, capability.Operation{Descriptor: descriptor, Handler: handler}); err != nil {
			return nil, fmt.Errorf("register operation: %w", err)
		}
	}
	values := map[string]string{}
	for _, role := range metadata.SecretRoles {
		values[role.Name] = "QATLAS_CONFORMANCE_" + strings.ToUpper(strings.ReplaceAll(role.Name, "-", "_"))
	}
	cfg := &config.Config{
		Version:     1,
		Services:    map[string]config.Service{"service": {Provider: metadata.ID, BaseURL: "https://provider.example.invalid"}},
		Credentials: map[string]config.Credential{"credential": {Type: config.CredentialTypeEnv, Values: values}},
		Connections: map[string]config.Connection{},
	}
	for name, connection := range connections {
		connection.Service, connection.Credential = "service", "credential"
		cfg.Connections[name] = connection
	}
	redactor := &redact.Redactor{}
	lookup := func(string) string {
		harness.secretReads.Add(1)
		return ""
	}
	harness.core = application.New(registry, cfg, secret.NewWith(lookup, nil, nil, redactor), redactor)
	return harness, nil
}

// invoke runs one explicit-connection request and returns how often it reached the handler.
func (h *conformanceHarness) invoke(d capability.Descriptor, connection string, arguments json.RawMessage,
	confirmed bool) (application.InvokeResponse, int32, error) {
	h.handlerCalls.Store(0)
	response, err := h.core.Invoke(context.Background(), application.InvokeRequest{
		Operation: d.ID, Version: d.Version, Connection: connection, Arguments: arguments, Confirmed: confirmed,
	})
	return response, h.handlerCalls.Load(), err
}

// conformanceArguments returns arguments the input schema of d accepts: its first example, or else the
// smallest value built from the schema's required members.
func conformanceArguments(d capability.Descriptor) (json.RawMessage, bool) {
	if len(d.Examples) > 0 {
		return d.Examples[0].Arguments, true
	}
	var schema map[string]any
	if json.Unmarshal(d.InputSchema, &schema) != nil {
		return nil, false
	}
	value, ok := sampleValue(schema)
	if !ok {
		return nil, false
	}
	encoded, err := json.Marshal(value)
	return encoded, err == nil
}

// sampleValue builds a value that satisfies the schema keywords the application core validates. A string
// is the first of a few common forms that fits its length and pattern; an operation whose input none of
// them fits declares an example instead.
func sampleValue(schema map[string]any) (any, bool) {
	if enum, ok := schema["enum"].([]any); ok && len(enum) > 0 {
		return enum[0], true
	}
	number := func(keyword string, fallback float64) float64 {
		if value, ok := schema[keyword].(float64); ok {
			return value
		}
		return fallback
	}
	switch schema["type"] {
	case nil:
		return nil, true
	case "object":
		properties, _ := schema["properties"].(map[string]any)
		required, _ := schema["required"].([]any)
		object := map[string]any{}
		for _, raw := range required {
			name, _ := raw.(string)
			property, _ := properties[name].(map[string]any)
			value, ok := sampleValue(property)
			if !ok {
				return nil, false
			}
			object[name] = value
		}
		return object, true
	case "array":
		items, _ := schema["items"].(map[string]any)
		array := []any{}
		for len(array) < int(number("minItems", 0)) {
			item, ok := sampleValue(items)
			if !ok {
				return nil, false
			}
			array = append(array, item)
		}
		return array, true
	case "string":
		pattern, _ := schema["pattern"].(string)
		expression, err := regexp.Compile(pattern)
		if err != nil {
			return nil, false
		}
		length := max(int(number("minLength", 1)), 1)
		for _, candidate := range []string{
			strings.Repeat("a", length), strings.Repeat("0", length), "00000000-0000-0000-0000-000000000000",
			"2026-01-01", "owner/repo", ".github/workflows/ci.yml",
		} {
			size := float64(len([]rune(candidate)))
			if size >= number("minLength", 0) && size <= number("maxLength", size) && expression.MatchString(candidate) {
				return candidate, true
			}
		}
		return nil, false
	case "integer", "number":
		value := number("minimum", 1)
		return min(value, number("maximum", value)), true
	case "boolean":
		return false, true
	case "null":
		return nil, true
	}
	return nil, false
}

func reportViolations(t *testing.T, violations []string) {
	t.Helper()
	for _, violation := range violations {
		t.Error(violation)
	}
}

func assertViolation(t *testing.T, violations []string, want string) {
	t.Helper()
	for _, violation := range violations {
		if strings.Contains(violation, want) {
			return
		}
	}
	t.Errorf("violations = %q, want one containing %q", violations, want)
}

// conformantMetadata and the descriptors below form a synthetic provider that satisfies every check, so a
// negative case can break exactly one invariant.
func conformantMetadata() config.ProviderMetadata {
	metadata := config.ProviderMetadata{
		ID: "sample", Name: "Sample",
		DefaultPermissions:   []config.Permission{config.PermissionRead},
		SupportedPermissions: []config.Permission{config.PermissionRead, config.PermissionCreate},
		SecretRoles:          []config.SecretRole{{Name: "api-token"}},
	}
	for _, descriptor := range conformantOperations() {
		metadata.Tools = append(metadata.Tools, descriptor.Tool())
	}
	return metadata
}

// conformantOperations are sorted by ID, the order the registry answers in.
func conformantOperations() []capability.Descriptor {
	return []capability.Descriptor{conformantDescriptor(), conformantRead(), conformantAllowListed()}
}

func conformantDescriptor() capability.Descriptor {
	return capability.Descriptor{
		ID: "sample.items.create", Version: 1, Provider: "sample",
		Title: "Create a sample item", Description: "Creates one sample item.",
		Risk: capability.Risk{
			Effect: capability.EffectCreate, Idempotency: capability.IdempotencyNonIdempotent,
			Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: "sample-items",
		},
		RequiresExplicitConnection: true,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minLength":1,` +
			`"pattern":"^[a-z]+$"}},"required":["name"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		Arguments:    []capability.Argument{{Name: "name", Description: "Item name", Required: true}},
		Examples:     []capability.Example{{Arguments: json.RawMessage(`{"name":"first"}`)}},
	}
}

// conformantRead has no example, so its arguments are derived from the input schema.
func conformantRead() capability.Descriptor {
	return capability.Descriptor{
		ID: "sample.items.get", Version: 1, Provider: "sample",
		Title: "Get a sample item", Description: "Reads one sample item.",
		Risk: capability.Risk{
			Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
			Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: "sample-items",
		},
		InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string","minLength":22,` +
			`"maxLength":22,"pattern":"^[A-Za-z0-9_-]{22}$"},"tags":{"type":"array","minItems":1,"items":` +
			`{"type":"string","enum":["x"]}},"limit":{"type":"integer","minimum":1,"maximum":10}},` +
			`"required":["id","tags","limit"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
	}
}

// conformantAllowListed is offered only by a connection whose tools list names it.
func conformantAllowListed() capability.Descriptor {
	return capability.Descriptor{
		ID: "sample.settings.get", Version: 1, Provider: "sample",
		Title: "Get the sample settings", Description: "Reads the settings of the sample account.",
		Risk: capability.Risk{
			Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
			Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: "sample-settings",
		},
		RequiresExplicitConnection: true,
		RequiresToolAllowList:      true,
		InputSchema:                json.RawMessage(`{"type":"object","additionalProperties":false}`),
		OutputSchema:               json.RawMessage(`{"type":"object"}`),
	}
}
