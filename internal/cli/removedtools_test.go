package cli

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
)

// removedToolCases names, per provider, the tools it no longer offers, one tool it still offers, and a
// minimal valid connection setup.
var removedToolCases = []struct {
	provider, role, baseURL, targets, remaining string
	removed                                     []string
}{
	{"penpot", "access-token", "", "[team/00000000-0000-0000-0000-000000000001]", "penpot.teams.list",
		[]string{"penpot.webhooks.create", "penpot.invitations.create"}},
	{"excalidrawplus", "api-key", "", "[\"*\"]", "excalidrawplus.scenes.list",
		[]string{"excalidrawplus.invitations.create"}},
	{"make", "api-token", "https://eu1.make.com", "[team/1]", "make.hooks.list",
		[]string{"make.hooks.create", "make.organization.invite"}},
	{"n8n", "api-key", "https://n8n.example.invalid", "", "n8n.users.list", []string{"n8n.users.invite"}},
	{"infomaniakdrive", "token", "", "[account/1]", "infomaniakdrive.links.get",
		[]string{"infomaniakdrive.links.create", "infomaniakdrive.links.update"}},
}

func removedToolsConfig(c int, tools string) string {
	tc := removedToolCases[c]
	baseURL, targets := "", ""
	if tc.baseURL != "" {
		baseURL = "\n    base_url: " + tc.baseURL
	}
	if tc.targets != "" {
		targets = "\n    targets: " + tc.targets
	}
	return fmt.Sprintf(`version: 1
services:
  svc:
    provider: %s%s
credentials:
  cred:
    type: env
    values:
      %s: REMOVED_TOOLS_SECRET
connections:
  conn:
    service: svc
    credential: cred
    permissions: [read, create, update, delete, execute]%s
    tools: %s
`, tc.provider, baseURL, tc.role, targets, tools)
}

// The tools of the outbound-access cleanup are gone from the registry and from every profile, and a call by
// the old ID ends as an unknown operation before any secret is read.
func TestRemovedToolsAreNotRegisteredOrProfiled(t *testing.T) {
	reg := defaultRegistry()
	for _, tc := range removedToolCases {
		metadata, ok := reg.ProviderMetadata(tc.provider)
		if !ok {
			t.Fatalf("provider %s is not registered", tc.provider)
		}
		for _, id := range tc.removed {
			if _, _, found := reg.Lookup(id); found {
				t.Errorf("%s is still registered", id)
			}
			for _, tool := range metadata.Tools {
				if tool.ID == id {
					t.Errorf("%s is still in the metadata of %s", id, tc.provider)
				}
			}
			for _, profile := range metadata.Profiles {
				for _, profiled := range profile.Tools {
					if profiled == id {
						t.Errorf("profile %s of %s still holds %s", profile.ID, tc.provider, id)
					}
				}
			}
			listed := false
			for _, declared := range metadata.RemovedTools {
				listed = listed || declared == id
			}
			if !listed {
				t.Errorf("%s does not declare %s as removed", tc.provider, id)
			}
		}
	}
}

func TestRemovedToolsEndAsUnknownOperationWithoutSecretAccess(t *testing.T) {
	for c, tc := range removedToolCases {
		cfg := writeConfig(t, removedToolsConfig(c, "["+tc.remaining+"]"))
		for _, id := range tc.removed {
			var reads atomic.Int32
			for _, args := range [][]string{{"describe", id}, {"invoke", id, "--confirm"}} {
				code, stdout, stderr := runTools(t, &reads, append(args, "--config", cfg)...)
				if code != exitUsage || stdout != "" || !strings.HasPrefix(stderr, "qatlas: unknown-operation:") {
					t.Errorf("%v: exit=%d stdout=%q stderr=%q", args, code, stdout, stderr)
				}
				// A suggestion may name another tool, never a removed one.
				rest := strings.ReplaceAll(stderr, id, "")
				for _, other := range tc.removed {
					if strings.Contains(rest, other) {
						t.Errorf("%v offers the removed tool %s: %q", args, other, stderr)
					}
				}
			}
			if reads.Load() != 0 {
				t.Errorf("%s: secret lookups = %d, want none", id, reads.Load())
			}
		}
	}
}

// An older file that still lists a removed tool stays valid: validate warns once per entry with the
// connection and the ID, the remaining tools stay offered, and any other unknown entry stays an error.
func TestConfigValidateWarnsAboutRemovedTools(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	for c, tc := range removedToolCases {
		list := "[" + strings.Join(append(append([]string{}, tc.removed...), tc.remaining), ", ") + "]"
		cfg := writeConfig(t, removedToolsConfig(c, list))
		code, stdout, stderr := runTools(t, nil, "config", "validate", "--config", cfg)
		if code != exitOK || !strings.HasPrefix(stdout, "configuration is valid") {
			t.Errorf("%s: exit=%d stdout=%q stderr=%q", tc.provider, code, stdout, stderr)
		}
		for _, id := range tc.removed {
			want := fmt.Sprintf("qatlas: warning: connections.conn.tools: tool %q was removed from qatlas and is ignored", id)
			if !strings.Contains(stderr, want) {
				t.Errorf("%s: stderr %q lacks %q", tc.provider, stderr, want)
			}
		}
		code, stdout, stderr = runTools(t, nil, "tools", tc.provider, "--connection", "conn", "--config", cfg,
			"--output", "json")
		if code != exitOK || !strings.Contains(stdout, `"`+tc.remaining+`"`) {
			t.Errorf("%s: tools: exit=%d stdout=%q stderr=%q", tc.provider, code, stdout, stderr)
		}
		for _, id := range tc.removed {
			if strings.Contains(stdout, id) {
				t.Errorf("%s: the connection offers the removed tool %s", tc.provider, id)
			}
		}
		other := writeConfig(t, removedToolsConfig(c, "["+tc.removed[0]+", "+tc.provider+".nonexistent.tool]"))
		code, _, stderr = runTools(t, nil, "config", "validate", "--config", other)
		if code == exitOK || !strings.Contains(stderr, "entry 2 is not a registered tool of provider") ||
			strings.Contains(stderr, "nonexistent") {
			t.Errorf("%s: another unknown entry: exit=%d stderr=%q", tc.provider, code, stderr)
		}
	}
}
