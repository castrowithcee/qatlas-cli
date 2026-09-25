package cli

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

func TestCodeFor(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want output.Code
	}{
		{"missing configuration", &config.NotFoundError{Path: "/absent.yaml"}, output.CodeConfigMissing},
		{"invalid configuration", &config.InvalidError{Path: "/c.yaml", Err: errors.New("version")}, output.CodeConfigInvalid},
		{"no connection selected", &config.SelectionError{Domain: "knowledge"}, output.CodeConnectionSelection},
		{"named connection missing", &config.SelectionError{Domain: "knowledge", Name: "absent"}, output.CodeUnknownConnection},
		{"unknown connection", &capability.UnknownConnectionError{Name: "absent"}, output.CodeUnknownConnection},
		{"invalid agent request", &application.InvalidRequestError{Message: "schema"}, output.CodeInvalidRequest},
		{"unknown operation", &application.UnknownOperationError{Operation: "x"}, output.CodeUnknownOperation},
		{"ambiguous connection", &application.ConnectionAmbiguousError{Operation: "x"}, output.CodeConnectionAmbiguous},
		{"no matching connection", &application.ConnectionSelectionError{Operation: "x"}, output.CodeConnectionSelection},
		{"confirmation required", &application.ConfirmationRequiredError{Operation: "x"}, output.CodeConfirmationRequired},
		{"policy denied", &application.PolicyDeniedError{Operation: "x"}, output.CodePolicyDenied},
		{"invalid provider result", &application.InvalidProviderResponseError{Operation: "x"}, output.CodeInvalidProviderResult},
		{"provider permission", &provider.Error{Class: provider.ClassPermission}, output.CodePermission},
		{"provider not found", &provider.Error{Class: provider.ClassNotFound}, output.CodeNotFound},
		{"provider timeout", &provider.Error{Class: provider.ClassTimeout}, output.CodeTimeout},
		{"provider invalid response", &provider.Error{Class: provider.ClassInvalidResponse}, output.CodeInvalidProviderResult},
		{"unsupported capability", &capability.UnsupportedError{Capability: "x"}, output.CodeUnsupportedCapability},
		{"projection", &output.ProjectionError{Field: "x"}, output.CodeUsage},
		{"plain usage error", &UsageError{errors.New("unknown flag")}, output.CodeUsage},
		{"anything else", errors.New("io failure"), output.CodeRuntime},
		{"wrapped specific error", fmt.Errorf("load: %w", &config.NotFoundError{}), output.CodeConfigMissing},
		// A specific error wrapped in a usage error keeps its own code.
		{"usage wrapping a specific error", &UsageError{&capability.UnknownConnectionError{}}, output.CodeUnknownConnection},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := codeFor(tt.err); got != tt.want {
				t.Errorf("codeFor() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Every diagnostic passes the redactor, including an unexpected error nobody anticipated.
func TestErrorsAreRedacted(t *testing.T) {
	const canary = "s3cr3t-canary-9f3a1c"

	var stdout, stderr bytes.Buffer
	opts := &Options{Redactor: &redact.Redactor{}}
	opts.Redactor.Add(canary)

	cmd := newRootCommand(opts, defaultRegistry())
	cmd.RunE = func(*cobra.Command, []string) error {
		return fmt.Errorf("provider rejected Authorization: Token id:%s", canary)
	}

	code := run(cmd, opts, nil, &stdout, &stderr)

	if code != exitRuntime {
		t.Errorf("exit code = %d, want %d", code, exitRuntime)
	}
	if strings.Contains(stderr.String(), canary) {
		t.Errorf("stderr leaks the secret: %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), redact.Marker) {
		t.Errorf("stderr = %q, want the redaction marker", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

// An agent-mode failure carries a machine-readable code and no prose.
func TestAgentErrorIsMachineReadable(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")

	var stdout, stderr bytes.Buffer
	opts := &Options{}
	code := run(newRootCommand(opts, defaultRegistry()), opts,
		[]string{"tools", "bookstack", "--agent", "--config", "/nonexistent/qatlas.yaml"}, &stdout, &stderr)

	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	first, _, _ := strings.Cut(stderr.String(), "\n")
	if !strings.HasPrefix(first, "qatlas: "+string(output.CodeConfigMissing)+": ") {
		t.Errorf("first stderr line = %q, want the code prefix", first)
	}
}

// auth names a person's action, which reads the same on both routes. permission gets no second step: each
// provider names the rights to check in its own message. Codes without a step keep their message as it is.
// unknown-connection points to discovery, the command on the CLI and the tool over MCP, for the provider and
// the tool the request named.
func TestNextStepByCodeAndRoute(t *testing.T) {
	authErr := &provider.Error{Class: provider.ClassAuth}
	for _, r := range []route{routeCLI, routeMCP} {
		if step := nextStep(authErr, r); !strings.Contains(step, "qatlas credential set <credential> <role>") ||
			!strings.Contains(step, "qatlas tui") {
			t.Errorf("route %d: auth step = %q, want the credential commands", r, step)
		}
		for _, class := range []provider.Class{provider.ClassPermission, provider.ClassNotFound,
			provider.ClassProviderError} {
			if step := nextStep(&provider.Error{Class: class}, r); step != "" {
				t.Errorf("route %d: %s step = %q, want none", r, class, step)
			}
		}
	}
	if nextStep(authErr, routeCLI) != nextStep(authErr, routeMCP) {
		t.Error("the auth step differs between the routes")
	}

	named := &capability.UnknownConnectionError{Name: "absent", Provider: "github", Operation: "github.issues.list"}
	for _, tt := range []struct {
		name string
		err  error
		r    route
		want string
	}{
		{"CLI with provider", named, routeCLI, "list the configured connections with 'qatlas connections github'"},
		{"MCP with tool", named, routeMCP, "call qatlas.describe with operation github.issues.list and without " +
			"connection for the connections that can run it"},
		{"CLI without provider", &capability.UnknownConnectionError{Name: "absent"}, routeCLI,
			"list the configured connections with 'qatlas connections'"},
		{"MCP without tool", &capability.UnknownConnectionError{Name: "absent", Provider: "github"}, routeMCP,
			"call qatlas.describe of the tool without connection for the connections that can run it"},
		{"configured name", &config.SelectionError{Name: "absent"}, routeCLI,
			"list the configured connections with 'qatlas connections'"},
	} {
		if got := nextStep(tt.err, tt.r); got != tt.want {
			t.Errorf("%s: step = %q, want %q", tt.name, got, tt.want)
		}
	}

	permission := &provider.Error{Class: provider.ClassPermission, Op: "list issues",
		Message: "this GitHub token may not read repository o/r; check its scopes or permissions"}
	if got := withNextStep(permission, routeCLI); got != error(permission) {
		t.Errorf("permission = %v, want the provider message unchanged", got)
	}
	auth := &provider.Error{Class: provider.ClassAuth, Op: "open", Message: "the GitHub token is unusable"}
	got := withNextStep(auth, routeMCP)
	var providerErr *provider.Error
	if !errors.As(got, &providerErr) || providerErr != auth || codeFor(got) != output.CodeAuth ||
		!strings.HasPrefix(got.Error(), auth.Error()+"; ") {
		t.Errorf("auth = %v, want the provider error kept and the step appended", got)
	}
}
