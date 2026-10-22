package forge

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func textResponse(status int, body string) *http.Response {
	response := jsonResponse(status, body)
	response.Header.Set("Content-Type", "text/plain")
	return response
}

const (
	apiSettingsForgejo = `{"default_git_trees_per_page":1000,"default_max_blob_size":10485760,` +
		`"default_paging_num":30,"max_response_items":50,"unknown":1}`
	apiSettingsGitea = `{"default_git_trees_per_page":1000,"default_max_blob_size":10485760,` +
		`"default_max_response_size":104857600,"default_paging_num":30,"max_response_items":50}`
)

func TestSettingsGetReadsEachSectionFromItsFixedPath(t *testing.T) {
	answers := map[string]string{
		"/api/v1/settings/repository": `{"forks_disabled":true,"http_git_disabled":false,"lfs_disabled":false,` +
			`"migrations_disabled":true,"mirrors_disabled":false,"stars_disabled":false,"time_tracking_disabled":true}`,
		"/api/v1/settings/attachment": `{"allowed_types":"image/*","enabled":true,"max_files":5,"max_size":2048}`,
		"/api/v1/settings/ui":         `{"allowed_reactions":["+1","-1"],"custom_emojis":["git"],"default_theme":"auto"}`,
	}
	for _, p := range products {
		answers["/api/v1/settings/api"] = apiSettingsForgejo
		if p == gitea {
			answers["/api/v1/settings/api"] = apiSettingsGitea
		}
		for _, section := range []string{"api", "repository", "attachment", "ui"} {
			e := newEnvironment(t, p, func(c call) *http.Response {
				if body, ok := answers[c.path]; ok {
					return jsonResponse(http.StatusOK, body)
				}
				return notFoundResponse()
			})
			result, err := e.invoke(t, settingsGet, "repo", `{"section":"`+section+`"}`)
			if err != nil || result["section"] != section {
				t.Fatalf("%s %s: %v, %v", p.id, section, result, err)
			}
			settings, _ := result["settings"].(map[string]any)
			if len(*e.calls) != 1 || (*e.calls)[0].method != http.MethodGet ||
				(*e.calls)[0].path != "/api/v1/settings/"+section || len(settings) == 0 || settings["unknown"] != nil {
				t.Errorf("%s %s: calls %+v, settings %v", p.id, section, *e.calls, settings)
			}
			switch section {
			case "api":
				_, hasSize := settings["default_max_response_size"]
				if hasSize != (p == gitea) || settings["max_response_items"] != float64(50) {
					t.Errorf("%s api = %v", p.id, settings)
				}
			case "repository":
				_, hasForks := settings["forks_disabled"]
				if hasForks != (p == forgejo) || settings["time_tracking_disabled"] != true {
					t.Errorf("%s repository = %v", p.id, settings)
				}
			case "ui":
				if reactions, _ := settings["allowed_reactions"].([]any); len(reactions) != 2 {
					t.Errorf("%s ui = %v", p.id, settings)
				}
			}
		}
	}
}

func TestSettingsGetRefusesAnotherSectionBeforeTheToken(t *testing.T) {
	e := newEnvironment(t, gitea, giteaAt("28.2.0", nil))
	for _, arguments := range []string{`{"section":"admin"}`, `{"section":"../user"}`, `{}`} {
		if _, err := e.invoke(t, settingsGet, "open", arguments); !isInvalidRequest(err) {
			t.Errorf("%s: %v", arguments, err)
		}
	}
	if len(*e.calls) != 0 || *e.reads != 0 {
		t.Errorf("calls %d, reads %d", len(*e.calls), *e.reads)
	}
}

const nodeInfoJSON = `{"version":"2.1","openRegistrations":false,"protocols":["activitypub"],` +
	`"services":{"inbound":[],"outbound":["rss2.0"]},"software":{"name":"forgejo","version":"15.0.10",` +
	`"homepage":"https://forgejo.org/","repository":"https://codeberg.org/forgejo/forgejo.git"},` +
	`"usage":{"localComments":1,"localPosts":2,"users":{"activeHalfyear":3,"activeMonth":4,"total":5}},` +
	`"metadata":{"nodeName":"Example","deep":{"a":{"b":{"c":{"d":1}}}},"other":["x"]},"unknown":1}`

func TestNodeInfoIsForgejoOnlyAndReadsEveryMember(t *testing.T) {
	reg := registry(t)
	if _, _, ok := reg.Lookup("gitea.nodeinfo.get"); ok {
		t.Fatal("nodeinfo is registered for Gitea")
	}
	e := newEnvironment(t, forgejo, func(c call) *http.Response {
		if c.path == "/api/v1/nodeinfo" {
			return jsonResponse(http.StatusOK, nodeInfoJSON)
		}
		return notFoundResponse()
	})
	result, err := e.invoke(t, nodeInfoGet, "repo", `{}`)
	if err != nil || result["version"] != "2.1" || result["openRegistrations"] != false || result["unknown"] != nil {
		t.Fatalf("%v, %v", result, err)
	}
	users := result["usage"].(map[string]any)["users"].(map[string]any)
	software := result["software"].(map[string]any)
	metadata := result["metadata"].(map[string]any)
	if users["total"] != float64(5) || software["name"] != "forgejo" || metadata["nodeName"] != "Example" ||
		metadata["other"] == nil {
		t.Errorf("result %v", result)
	}
	if encoded, _ := json.Marshal(metadata["deep"]); strings.Contains(string(encoded), `"d"`) {
		t.Errorf("free-form metadata is not bounded in depth: %s", encoded)
	}
	if !slices.Equal(e.paths(), []string{"/api/v1/nodeinfo"}) || (*e.calls)[0].method != http.MethodGet {
		t.Errorf("calls %+v", *e.calls)
	}
}

const gpgKey = "-----BEGIN PGP PUBLIC KEY BLOCK-----\nabc\n-----END PGP PUBLIC KEY BLOCK-----\n"

func TestSigningKeysUseTheFormatsOfTheirProduct(t *testing.T) {
	paths := map[string]string{"gpg": "/api/v1/signing-key.gpg", "pub": "/api/v1/signing-key.pub",
		"ssh": "/api/v1/signing-key.ssh"}
	for _, tc := range []struct {
		p      *product
		format string
	}{{forgejo, "gpg"}, {forgejo, "ssh"}, {gitea, "gpg"}, {gitea, "pub"}} {
		e := newEnvironment(t, tc.p, func(c call) *http.Response {
			if c.path == paths[tc.format] {
				return textResponse(http.StatusOK, gpgKey)
			}
			return notFoundResponse()
		})
		result, err := e.invoke(t, signingKeysGet, "repo", `{"format":"`+tc.format+`"}`)
		if err != nil || result["format"] != tc.format || !strings.HasPrefix(result["key"].(string), "-----BEGIN") {
			t.Fatalf("%s %s: %v, %v", tc.p.id, tc.format, result, err)
		}
		if !slices.Equal(e.paths(), []string{paths[tc.format]}) || (*e.calls)[0].method != http.MethodGet {
			t.Errorf("%s %s: calls %+v", tc.p.id, tc.format, *e.calls)
		}
	}
	e := newEnvironment(t, gitea, func(c call) *http.Response { return textResponse(http.StatusOK, gpgKey) })
	if result, err := e.invoke(t, signingKeysGet, "repo", `{}`); err != nil || result["format"] != "gpg" {
		t.Errorf("default format: %v, %v", result, err)
	}
}

// The other product's format is refused before the token is read and before any request.
func TestSigningKeysRefuseTheOtherProductsFormatBeforeTheToken(t *testing.T) {
	for _, tc := range []struct {
		p      *product
		format string
	}{{forgejo, "pub"}, {gitea, "ssh"}, {gitea, "other"}} {
		e := newEnvironment(t, tc.p, func(call) *http.Response { return textResponse(http.StatusOK, gpgKey) })
		_, err := e.invoke(t, signingKeysGet, "open", `{"format":"`+tc.format+`"}`)
		if !isInvalidRequest(err) || len(*e.calls) != 0 || *e.reads != 0 {
			t.Errorf("%s %s: %v, calls %d, reads %d", tc.p.id, tc.format, err, len(*e.calls), *e.reads)
		}
		// the handler refuses on its own too, whatever the schema says
		if _, err := signingKeysGet.prepare(tc.p, nil, []byte(`{"format":"`+tc.format+`"}`)); !isInvalidRequest(err) {
			t.Errorf("%s %s prepare: %v", tc.p.id, tc.format, err)
		}
	}
}

func TestSigningKeyWithoutContentIsAnInvalidResponse(t *testing.T) {
	e := newEnvironment(t, forgejo, func(call) *http.Response { return textResponse(http.StatusOK, "  \n") })
	if _, err := e.invoke(t, signingKeysGet, "open", `{"format":"ssh"}`); classOfError(err) != provider.ClassInvalidResponse {
		t.Errorf("err = %v", err)
	}
}

func TestInstanceToolsIgnoreRepositoryTargets(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		reach := reaches(p)
		for _, id := range []string{p.id + ".settings.get", p.id + ".signing-keys.get", p.id + ".markdown.render",
			p.id + ".markup.render", p.id + ".nodeinfo.get"} {
			if _, _, ok := reg.Lookup(id); !ok {
				continue
			}
			if ok, _ := reach(id, []string{"repos/acme/widget"}); !ok {
				t.Errorf("%s is hidden from a repository binding", id)
			}
		}
	}
}
