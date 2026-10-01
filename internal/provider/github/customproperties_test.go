package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// fakeCustomProperties answers the repository property values and the organization property schema routes,
// through the failure hook of fakeGitHub, and keeps what it holds by target.
type fakeCustomProperties struct {
	mu     sync.Mutex
	f      *fakeGitHub
	values map[string][]map[string]any // "repos/octo-org/example" -> [{"property_name":..,"value":..}]
	schema map[string][]map[string]any // "orgs/octo-org" -> [{"property_name":..,"value_type":..,...}]
}

func serveCustomProperties(t *testing.T) (*fakeGitHub, *fakeCustomProperties, string) {
	t.Helper()
	c := &fakeCustomProperties{
		values: map[string][]map[string]any{
			"repos/octo-org/example": {{"property_name": "team", "value": "atlas"}},
		},
		schema: map[string][]map[string]any{
			"orgs/octo-org": {{"property_name": "team", "value_type": "single_select", "required": false,
				"allowed_values": []string{"atlas", "hydra"}}},
		},
	}
	f := &fakeGitHub{}
	c.f = f
	f.failure = c.route
	return f, c, serve(t, f)
}

func (c *fakeCustomProperties) route(w http.ResponseWriter, req *http.Request) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	path := req.URL.Path
	w.Header().Set("Content-Type", "application/json")
	var body map[string]any
	if req.Method != http.MethodGet {
		requests := c.f.recorded()
		if len(requests) > 0 {
			body = requests[len(requests)-1].body
		}
	}
	switch {
	case strings.HasSuffix(path, "/properties/values") && strings.HasPrefix(path, "/api/v3/repos/"):
		repo := "repos/" + strings.TrimSuffix(strings.TrimPrefix(path, "/api/v3/repos/"), "/properties/values")
		switch req.Method {
		case http.MethodGet:
			data, _ := json.Marshal(c.values[repo])
			if data == nil {
				data = []byte("[]")
			}
			w.Write(data)
		case http.MethodPatch:
			properties, _ := body["properties"].([]any)
			entries := make([]map[string]any, 0, len(properties))
			for _, raw := range properties {
				entry, _ := raw.(map[string]any)
				if entry["value"] != nil {
					entries = append(entries, entry)
				}
			}
			c.values[repo] = entries
			w.WriteHeader(http.StatusNoContent)
		default:
			return false
		}
	case strings.HasSuffix(path, "/properties/schema") && strings.HasPrefix(path, "/api/v3/orgs/"):
		org := "orgs/" + strings.TrimSuffix(strings.TrimPrefix(path, "/api/v3/orgs/"), "/properties/schema")
		switch req.Method {
		case http.MethodGet:
			data, _ := json.Marshal(c.schema[org])
			if data == nil {
				data = []byte("[]")
			}
			w.Write(data)
		case http.MethodPatch:
			properties, _ := body["properties"].([]any)
			entries := make([]map[string]any, 0, len(properties))
			for _, raw := range properties {
				entry, _ := raw.(map[string]any)
				entries = append(entries, entry)
			}
			c.schema[org] = entries
			data, _ := json.Marshal(entries)
			w.Write(data)
		default:
			return false
		}
	default:
		return false
	}
	return true
}

// customPropertiesConfig binds every shape of connection the custom properties tools distinguish: a
// repository connection, an organization connection, one that lists no target at all (both are then required
// explicitly), and one that lists the guarded set tool.
func customPropertiesConfig(base string) *config.Config {
	cfg := coreConfig(base)
	all := []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate,
		config.PermissionDelete}
	cfg.Connections["repo"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: repoTarget,
		Permissions: all}
	cfg.Connections["repo-listed"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: repoTarget,
		Permissions: all, Tools: []string{customPropertiesSet.ID}}
	cfg.Connections["org"] = config.Connection{Service: "gh", Credential: "gh-reader",
		Targets: []string{"orgs/octo-org"}, Permissions: all}
	cfg.Connections["org-listed"] = config.Connection{Service: "gh", Credential: "gh-reader",
		Targets: []string{"orgs/octo-org"}, Permissions: all, Tools: []string{customPropertiesSet.ID}}
	cfg.Connections["open"] = config.Connection{Service: "gh", Credential: "gh-reader", Permissions: all}
	return cfg
}

// Both custom properties tools satisfy their output contract through the application core once confirmed,
// address exactly the repository or the organization the caller named, and, listed only, are sent exactly
// once.
func TestCustomPropertiesToolsSatisfyTheirContractThroughTheApplicationCore(t *testing.T) {
	f, _, base := serveCustomProperties(t)
	red := &redact.Redactor{}
	core := application.New(registry(t), customPropertiesConfig(base), resolver(red, nil), red)

	got, err := invoke(t, core, customPropertiesGet.ID, "repo", `{"repository":"octo-org/example"}`, false)
	if err != nil || !strings.Contains(string(got), `"property_name":"team"`) ||
		!strings.Contains(string(got), `"value":"atlas"`) ||
		!strings.Contains(string(got), `"repository":"octo-org/example"`) {
		t.Fatalf("get repository custom property values = %s, %v", got, err)
	}

	gotOrg, err := invoke(t, core, customPropertiesGet.ID, "org", `{"organization":"orgs/octo-org"}`, false)
	if err != nil || !strings.Contains(string(gotOrg), `"value_type":"single_select"`) ||
		!strings.Contains(string(gotOrg), `"organization":"orgs/octo-org"`) {
		t.Fatalf("get organization custom property schema = %s, %v", gotOrg, err)
	}

	before := len(f.recorded())
	set, err := invoke(t, core, customPropertiesSet.ID, "repo-listed",
		`{"repository":"octo-org/example","properties":[{"property_name":"cost_center","value":"eng"},`+
			`{"property_name":"team","value":null}]}`, true)
	if err != nil || !strings.Contains(string(set), `"property_name":"cost_center"`) ||
		!strings.Contains(string(set), `"repository":"octo-org/example"`) {
		t.Fatalf("set repository custom property values = %s, %v", set, err)
	}
	if requests := f.recorded()[before:]; len(requests) != 1 {
		t.Errorf("requests = %+v, want one set, sent once", requests)
	}

	before = len(f.recorded())
	setOrg, err := invoke(t, core, customPropertiesSet.ID, "org-listed",
		`{"organization":"orgs/octo-org","definitions":[{"property_name":"team","value_type":"single_select",`+
			`"allowed_values":["atlas","hydra","forge"]}]}`, true)
	if err != nil || !strings.Contains(string(setOrg), `"forge"`) ||
		!strings.Contains(string(setOrg), `"organization":"orgs/octo-org"`) {
		t.Fatalf("set organization custom property schema = %s, %v", setOrg, err)
	}
	if requests := f.recorded()[before:]; len(requests) != 1 {
		t.Errorf("requests = %+v, want one set, sent once", requests)
	}
}

// repository and organization are exclusive: giving both or neither is refused before a credential is
// resolved, and an organization outside the connection's targets is refused the same way.
func TestCustomPropertiesTargetIsExclusive(t *testing.T) {
	f, _, base := serveCustomProperties(t)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), customPropertiesConfig(base), resolver(red, &reads), red)

	for _, tt := range []struct {
		name, connection, arguments string
	}{
		{"neither", "open", `{}`},
		{"both", "open", `{"repository":"octo-org/example","organization":"orgs/octo-org"}`},
		{"an organization outside the targets", "repo", `{"organization":"orgs/other-org"}`},
		{"a user owner instead of an organization", "open", `{"organization":"users/octocat"}`},
	} {
		reads = 0
		before := len(f.recorded())
		if _, err := invoke(t, core, customPropertiesGet.ID, tt.connection, tt.arguments, false); !isInvalidRequest(err) {
			t.Errorf("%s = %v, want an invalid request", tt.name, err)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s reached the credential or GitHub", tt.name)
		}
	}
}

// github.customproperties.set is offered only where a connection's tools list names it, and refused
// otherwise before a secret is read.
func TestCustomPropertiesSetNeedsItsNameInTheToolsList(t *testing.T) {
	f, _, base := serveCustomProperties(t)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), customPropertiesConfig(base), resolver(red, &reads), red)

	guarded := []struct{ id, arguments string }{
		{customPropertiesSet.ID, `{"repository":"octo-org/example","properties":[{"property_name":"x","value":"y"}]}`},
	}
	for _, connection := range []string{"repo", "org", "open"} {
		for _, tt := range guarded {
			var unsupported *capability.UnsupportedError
			if _, err := core.Describe(application.DescribeRequest{Operation: tt.id, Connection: connection}); !errors.As(err, &unsupported) {
				t.Errorf("describe %s on %s = %v, want unsupported", tt.id, connection, err)
			}
			reads = 0
			before := len(f.recorded())
			if _, err := invoke(t, core, tt.id, connection, tt.arguments, true); !errors.As(err, &unsupported) {
				t.Errorf("invoke %s on %s = %v, want unsupported", tt.id, connection, err)
			}
			if reads != 0 || len(f.recorded()) != before {
				t.Errorf("%s on %s reached the credential or GitHub", tt.id, connection)
			}
		}
	}

	// repo-listed and org-listed name the tool and offer it, unconfirmed refused with confirmation-required
	// before GitHub is reached.
	unconfirmed := &application.ConfirmationRequiredError{}
	for _, connection := range []string{"repo-listed", "org-listed"} {
		arguments := `{"repository":"octo-org/example","properties":[{"property_name":"x","value":"y"}]}`
		if connection == "org-listed" {
			arguments = `{"organization":"orgs/octo-org","definitions":[{"property_name":"x","value_type":"string"}]}`
		}
		before := len(f.recorded())
		if _, err := invoke(t, core, customPropertiesSet.ID, connection, arguments, false); !errors.As(err, &unconfirmed) {
			t.Errorf("%s on %s without --confirm = %v, want confirmation-required", customPropertiesSet.ID, connection, err)
		}
		if len(f.recorded()) != before {
			t.Errorf("%s on %s reached GitHub before confirmation", customPropertiesSet.ID, connection)
		}
	}
}

// Every check of github.customproperties.set runs before a credential is resolved: properties applies only
// to a repository, definitions only to an organization, and each is required with its own target.
func TestCustomPropertiesSetArgumentsAreValidatedBeforeIO(t *testing.T) {
	f, _, base := serveCustomProperties(t)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), customPropertiesConfig(base), resolver(red, &reads), red)

	for _, tt := range []struct {
		name, connection, arguments string
	}{
		{"properties missing for a repository", "repo-listed", `{"repository":"octo-org/example"}`},
		{"definitions given for a repository", "repo-listed",
			`{"repository":"octo-org/example","definitions":[{"property_name":"x","value_type":"string"}]}`},
		{"definitions missing for an organization", "org-listed", `{"organization":"orgs/octo-org"}`},
		{"properties given for an organization", "org-listed",
			`{"organization":"orgs/octo-org","properties":[{"property_name":"x","value":"y"}]}`},
		{"an invalid property name", "repo-listed",
			`{"repository":"octo-org/example","properties":[{"property_name":"","value":"y"}]}`},
	} {
		reads = 0
		before := len(f.recorded())
		if _, err := invoke(t, core, customPropertiesSet.ID, tt.connection, tt.arguments, true); !isInvalidRequest(err) {
			t.Errorf("%s = %v, want an invalid request", tt.name, err)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s reached the credential or GitHub", tt.name)
		}
	}

	manyProperties := make([]string, 101)
	for i := range manyProperties {
		manyProperties[i] = fmt.Sprintf(`{"property_name":"p%d","value":"v"}`, i)
	}
	oversized := `{"repository":"octo-org/example","properties":[` + strings.Join(manyProperties, ",") + `]}`
	reads = 0
	before := len(f.recorded())
	if _, err := invoke(t, core, customPropertiesSet.ID, "repo-listed", oversized, true); !isInvalidRequest(err) {
		t.Errorf("too many properties = %v, want an invalid request", err)
	}
	if reads != 0 || len(f.recorded()) != before {
		t.Error("too many properties reached the credential or GitHub")
	}
}
