package forge

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
)

const (
	forgejoArtifact = `{"id":5,"name":"dist","size_in_bytes":1024,"expired":false,"run_id":42,"created_at":"2030-01-01T00:00:00Z","updated_at":"2030-01-01T00:01:00Z","expires_at":"2030-02-01T00:00:00Z","archive_download_url":"https://forge.example.test/api/v1/repos/acme/widget/actions/artifacts/5/zip"}`
	giteaArtifact   = `{"id":5,"name":"dist","size_in_bytes":1024,"expired":false,"created_at":"2030-01-01T00:00:00Z","updated_at":"2030-01-01T00:01:00Z","expires_at":"2030-02-01T00:00:00Z","url":"https://forge.example.test/api/v1/repos/acme/widget/actions/artifacts/5","archive_download_url":"https://forge.example.test/api/v1/repos/acme/widget/actions/artifacts/5/zip","workflow_run":{"id":42,"repository_id":9,"head_branch":"main","head_sha":"abc1234","repository":{"id":9,"name":"widget","full_name":"acme/widget","owner":{"id":1,"login":"acme"}}}}`
	giteaForeignArt = `{"id":6,"name":"x","workflow_run":{"id":50,"repository":{"id":10,"name":"foo","full_name":"other/foo","owner":{"id":2,"login":"other"}}}}`
)

func artifactVersion(p *product) string {
	return map[string]string{forgejo.id: "16.0.6", gitea.id: "28.2.0"}[p.id]
}

func artifactFixture(p *product) string {
	if p == gitea {
		return giteaArtifact
	}
	return forgejoArtifact
}

func TestWorkflowArtifactsListAnswersBothShapes(t *testing.T) {
	for _, p := range products {
		answer := `[` + artifactFixture(p) + `]`
		if p == gitea {
			answer = `{"total_count":2,"artifacts":[` + giteaArtifact + `,` + giteaForeignArt + `]}`
		}
		e, _ := newActionsEnvironment(t, p, artifactVersion(p), func(c call) *http.Response {
			return jsonResponse(200, answer, "Link", `<https://forge.example.test/next>; rel="next"`)
		})
		arguments := `{"name":"dist"}`
		if p == forgejo {
			arguments = `{"name":"dist","limit":5}`
		}
		result, err := e.invoke(t, workflowArtifactsList, "repo", arguments)
		artifacts := rowsOf(t, result, "artifacts")
		if err != nil || len(artifacts) != 1 || result["repository"] != "acme/widget" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		row := artifacts[0]
		for key, want := range map[string]any{"id": float64(5), "name": "dist", "size_in_bytes": float64(1024),
			"expired": false, "run_id": float64(42)} {
			if row[key] != want {
				t.Errorf("%s: %s = %v, want %v", p.id, key, row[key], want)
			}
		}
		_, hasRun := row["workflow_run"]
		if hasRun != (p == gitea) {
			t.Errorf("%s: workflow_run = %v", p.id, row)
		}
		query := queryOf(t, apiCalls(e)[0])
		if query.Get("name") != "dist" {
			t.Errorf("%s query = %v", p.id, query)
		}
		// Only Forgejo pages; Gitea documents no paging and the list ends with the answer.
		if p == forgejo {
			if query.Get("page") != "1" || query.Get("limit") != "5" || result["has_more"] != true || result["next_cursor"] == nil {
				t.Errorf("forgejo paging: %v, %v", query, result)
			}
		} else {
			if query.Has("page") || query.Has("limit") || result["has_more"] != false || result["total_count"] != float64(2) {
				t.Errorf("gitea paging: %v, %v", query, result)
			}
			if _, err := e.invoke(t, workflowArtifactsList, "repo", `{"limit":5}`); !isInvalidRequest(err) {
				t.Errorf("gitea limit: %v", err)
			}
		}
	}
}

func TestWorkflowArtifactsListOfARunUsesTheRunPath(t *testing.T) {
	for _, p := range products {
		answer := `[]`
		if p == gitea {
			answer = `{"total_count":0,"artifacts":[]}`
		}
		e, _ := newActionsEnvironment(t, p, artifactVersion(p), func(call) *http.Response { return jsonResponse(200, answer) })
		if _, err := e.invoke(t, workflowArtifactsList, "repo", `{"run_id":42}`); err != nil {
			t.Fatalf("%s: %v", p.id, err)
		}
		if got := apiMethods(e); !reflect.DeepEqual(got, []string{"GET " + actionsBase + "/runs/42/artifacts"}) {
			t.Errorf("%s calls = %v", p.id, got)
		}
		reads := *e.reads
		for _, arguments := range []string{`{"run_id":0}`, `{"repository":"acme/other"}`, `{"name":"a\nb"}`} {
			if _, err := e.invoke(t, workflowArtifactsList, "repo", arguments); !isInvalidRequest(err) || *e.reads != reads {
				t.Errorf("%s %s: %v", p.id, arguments, err)
			}
		}
	}
}

func TestWorkflowArtifactGetBindsTheID(t *testing.T) {
	for _, p := range products {
		answer := artifactFixture(p)
		e, _ := newActionsEnvironment(t, p, artifactVersion(p), func(c call) *http.Response {
			return jsonResponse(200, answer)
		})
		result, err := e.invoke(t, workflowArtifactsGet, "repo", `{"artifact_id":5}`)
		if err != nil || result["id"] != float64(5) || result["run_id"] != float64(42) ||
			!reflect.DeepEqual(apiMethods(e), []string{"GET " + actionsBase + "/artifacts/5"}) {
			t.Fatalf("%s: %v, %v, %v", p.id, result, err, apiMethods(e))
		}
		// Another ID in the answer, and a foreign repository in the answer, are refused.
		wrongs := map[string]string{"id": strings.Replace(artifactFixture(p), `"id":5`, `"id":6`, 1)}
		if p == gitea {
			wrongs["repository"] = strings.ReplaceAll(giteaArtifact, "acme/widget", "other/foo")
		}
		for name, wrong := range wrongs {
			answer = wrong
			if _, err := e.invoke(t, workflowArtifactsGet, "repo", `{"artifact_id":5}`); err == nil ||
				!strings.Contains(err.Error(), "returned an artifact") {
				t.Errorf("%s %s: %v", p.id, name, err)
			}
		}
		reads := *e.reads
		for _, arguments := range []string{`{}`, `{"artifact_id":0}`, `{"artifact_id":5,"repository":"acme/other"}`} {
			if _, err := e.invoke(t, workflowArtifactsGet, "repo", arguments); !isInvalidRequest(err) || *e.reads != reads {
				t.Errorf("%s %s: %v", p.id, arguments, err)
			}
		}
	}
}

func TestWorkflowArtifactsNeedForgejo16(t *testing.T) {
	for _, tc := range []struct {
		p         *product
		v         string
		available bool
	}{{forgejo, "15.0.10", false}, {forgejo, "16.0.6", true}, {forgejo, "17.0.0", true}, {gitea, "1.27.3", true},
		{gitea, "28.2.0", true}} {
		for _, tool := range []*tool{workflowArtifactsList, workflowArtifactsGet} {
			e, _ := newActionsEnvironment(t, tc.p, tc.v, func(call) *http.Response { return jsonResponse(200, `{}`) })
			arguments := `{"artifact_id":5}`
			if tool == workflowArtifactsList {
				arguments = `{}`
			}
			_, err := e.invoke(t, tool, "repo", arguments)
			refused := err != nil && strings.Contains(err.Error(), "not available") && strings.Contains(err.Error(), "16")
			if refused == tc.available || (refused && len(apiCalls(e)) != 0) {
				t.Errorf("%s %s %s: %v", tc.p.id, tc.v, tool.action, err)
			}
		}
	}
}

func TestWorkflowArtifactDownloadWritesAZipWithinTheBounds(t *testing.T) {
	const archive = "PK-artifact-bytes"
	previous := maxArtifactBytes
	t.Cleanup(func() { maxArtifactBytes = previous })
	for _, p := range products {
		e, dirs := newActionsEnvironment(t, p, artifactVersion(p), func(c call) *http.Response {
			if c.path == actionsBase+"/artifacts/5/zip" {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(archive)), ContentLength: -1,
					Header: http.Header{"Content-Type": {"application/zip"}}}
			}
			return notFoundResponse()
		})
		out := filepath.Join(dirs.write, "dist.zip")
		arguments := `{"artifact_id":5,"local_path":` + quote(out) + `}`
		result, err := e.invokeAs(t, workflowArtifactsDownload, "files", arguments, false)
		sum := sha256.Sum256([]byte(archive))
		if err != nil || result["size"] != float64(len(archive)) || result["sha256"] != hex.EncodeToString(sum[:]) ||
			result["artifact_id"] != float64(5) || result["local_path"] != out {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if data, err := os.ReadFile(out); err != nil || string(data) != archive {
			t.Errorf("%s: file = %q, %v", p.id, data, err)
		}
		if calls := apiCalls(e); len(calls) != 1 || calls[0].auth != "token "+tokenValue {
			t.Errorf("%s calls = %v", p.id, apiMethods(e))
		}
		reads, calls := *e.reads, len(*e.calls)
		var confirm *application.ConfirmationRequiredError
		if _, err := e.invokeAs(t, workflowArtifactsDownload, "files", arguments, false); !errors.As(err, &confirm) ||
			*e.reads != reads || len(*e.calls) != calls {
			t.Errorf("%s: overwrite without confirmation: %v", p.id, err)
		}
		if _, err := e.invoke(t, workflowArtifactsDownload, "files", arguments); err != nil {
			t.Errorf("%s: confirmed overwrite: %v", p.id, err)
		}
		reads, calls = *e.reads, len(*e.calls)
		outside := filepath.Join(dirs.outside, "dist.zip")
		for _, bad := range []string{`{"artifact_id":5,"local_path":` + quote(outside) + `}`,
			`{"artifact_id":5,"repository":"acme/other","local_path":` + quote(filepath.Join(dirs.write, "b.zip")) + `}`,
			`{"artifact_id":0,"local_path":` + quote(filepath.Join(dirs.write, "c.zip")) + `}`, `{"artifact_id":5}`} {
			if _, err := e.invoke(t, workflowArtifactsDownload, "files", bad); !isInvalidRequest(err) ||
				strings.Contains(err.Error(), dirs.outside) || *e.reads != reads || len(*e.calls) != calls {
				t.Errorf("%s %s: %v", p.id, bad, err)
			}
		}
	}
	// A failed transfer leaves no file: redirect, refusal, server error, and a size above the limit.
	cases := map[string]func() *http.Response{
		"redirect": func() *http.Response {
			return &http.Response{StatusCode: http.StatusFound, Body: io.NopCloser(strings.NewReader("")),
				Header: http.Header{"Location": {"https://storage.example.test/signed"}}}
		},
		"refused": notFoundResponse,
		"server":  func() *http.Response { return jsonResponse(500, `{"message":"`+bodyCanary+`"}`) },
		"limit":   func() *http.Response { return plainResponse(200, strings.Repeat("z", 100)) },
	}
	for _, p := range products {
		for name, answer := range cases {
			maxArtifactBytes = previous
			if name == "limit" {
				maxArtifactBytes = 10
			}
			e, dirs := newActionsEnvironment(t, p, artifactVersion(p), func(c call) *http.Response {
				if strings.HasSuffix(c.path, "/zip") {
					return answer()
				}
				return notFoundResponse()
			})
			_, err := e.invoke(t, workflowArtifactsDownload, "files",
				`{"artifact_id":5,"local_path":`+quote(filepath.Join(dirs.write, "l.zip"))+`}`)
			if err == nil || strings.Contains(err.Error(), "storage.example") || strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("%s %s: %v", p.id, name, err)
			}
			if entries, _ := os.ReadDir(dirs.write); len(entries) != 0 {
				t.Errorf("%s %s: files left: %v", p.id, name, entries)
			}
			if len(apiCalls(e)) != 1 {
				t.Errorf("%s %s: calls %v", p.id, name, apiMethods(e))
			}
		}
	}
	// Forgejo 15 has no artifacts: refused before the request.
	e, dirs := newActionsEnvironment(t, forgejo, "15.0.10", func(call) *http.Response { return jsonResponse(200, `{}`) })
	_, err := e.invoke(t, workflowArtifactsDownload, "files", `{"artifact_id":5,"local_path":`+quote(filepath.Join(dirs.write, "v.zip"))+`}`)
	if err == nil || !strings.Contains(err.Error(), "not available") || len(apiCalls(e)) != 0 {
		t.Errorf("forgejo 15: %v", err)
	}
}

func TestWorkflowArtifactDeleteIsListedOnlyAndInNoProfile(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		metadata, _ := reg.ProviderMetadata(p.id)
		d, _, ok := reg.Lookup(p.toolID(workflowArtifactsDelete))
		if !ok || !d.RequiresToolAllowList || d.Risk.Confirmation != "required" || d.Risk.Effect != "delete" {
			t.Fatalf("%s descriptor = %+v", p.id, d)
		}
		for _, profile := range metadata.Profiles {
			if slices.Contains(profile.Tools, d.ID) {
				t.Errorf("%s: profile %s holds %s", p.id, profile.ID, d.ID)
			}
		}
		e, _ := newActionsEnvironment(t, p, artifactVersion(p), func(call) *http.Response {
			return &http.Response{StatusCode: 204, Body: http.NoBody, Header: http.Header{}}
		})
		if _, err := e.invoke(t, workflowArtifactsDelete, "repo", `{"artifact_id":5}`); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: delete without a tools list: %v", p.id, err)
		}
		if _, err := e.invokeAs(t, workflowArtifactsDelete, "actions-delete", `{"artifact_id":5}`, false); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: unconfirmed delete: %v", p.id, err)
		}
		result, err := e.invoke(t, workflowArtifactsDelete, "actions-delete", `{"artifact_id":5}`)
		if err != nil || result["deleted"] != true || !reflect.DeepEqual(apiMethods(e), []string{"DELETE " + actionsBase + "/artifacts/5"}) {
			t.Errorf("%s: %v, %v, %v", p.id, result, err, apiMethods(e))
		}
		f, _ := newActionsEnvironment(t, p, artifactVersion(p), func(call) *http.Response {
			return jsonResponse(503, `{"message":"`+bodyCanary+`"}`)
		})
		if _, err := f.invoke(t, workflowArtifactsDelete, "actions-delete", `{"artifact_id":5}`); err == nil ||
			!strings.Contains(err.Error(), "may have been applied") || len(apiCalls(f)) != 1 {
			t.Errorf("%s: 503: %v, calls %v", p.id, err, apiMethods(f))
		}
	}
}

func TestGiteaArtifactDownloadFollowsOnlyItsOwnSignedAddress(t *testing.T) {
	const archive = "PK-signed-bytes"
	const raw = "/api/v1/repos/acme/widget/actions/artifacts/5/zip/raw"
	redirect := func(location string) *http.Response {
		return &http.Response{StatusCode: http.StatusFound, Body: io.NopCloser(strings.NewReader("")),
			Header: http.Header{"Location": {location}}}
	}
	own := "https://" + apiHost + raw + "?sig=abc&expires=123"
	cases := []struct {
		name     string
		location string
		second   func() *http.Response
		ok       bool
	}{
		{"own", own, nil, true},
		{"relative", raw + "?sig=abc&expires=123", nil, true},
		{"host", "https://storage.example.test" + raw + "?sig=abc&expires=123", nil, false},
		{"port", "https://" + apiHost + ":8443" + raw + "?sig=abc&expires=123", nil, false},
		{"scheme", "http://" + apiHost + raw + "?sig=abc&expires=123", nil, false},
		{"path", "https://" + apiHost + "/api/v1/repos/acme/widget/actions/artifacts/5/zip?sig=abc&expires=123", nil, false},
		{"id", "https://" + apiHost + strings.Replace(raw, "/5/", "/6/", 1) + "?sig=abc&expires=123", nil, false},
		{"repository", "https://" + apiHost + strings.Replace(raw, "widget", "other", 1) + "?sig=abc&expires=123", nil, false},
		{"extra query", own + "&x=1", nil, false},
		{"missing query", "https://" + apiHost + raw + "?sig=abc", nil, false},
		{"user info", "https://u:p@" + apiHost + raw + "?sig=abc&expires=123", nil, false},
		{"second redirect", own, func() *http.Response { return redirect("https://" + apiHost + raw + "?sig=abc&expires=123") }, false},
		{"second refusal", own, notFoundResponse, false},
	}
	for _, tc := range cases {
		e, dirs := newActionsEnvironment(t, gitea, "28.2.0", func(c call) *http.Response {
			switch {
			case c.path == actionsBase+"/artifacts/5/zip":
				return redirect(tc.location)
			case strings.HasSuffix(c.path, "/zip/raw") && tc.second != nil:
				return tc.second()
			case c.path == raw:
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(archive)), ContentLength: -1,
					Header: http.Header{}}
			}
			return notFoundResponse()
		})
		out := filepath.Join(dirs.write, "dist.zip")
		result, err := e.invoke(t, workflowArtifactsDownload, "files", `{"artifact_id":5,"local_path":`+quote(out)+`}`)
		calls := apiCalls(e)
		if tc.ok {
			if err != nil || result["size"] != float64(len(archive)) || len(calls) != 2 || calls[0].auth == "" ||
				calls[1].auth != "" || calls[1].path != raw || queryOf(t, calls[1]).Get("sig") != "abc" {
				t.Errorf("%s: %v, %v, %v", tc.name, result, err, calls)
			}
			if data, err := os.ReadFile(out); err != nil || string(data) != archive {
				t.Errorf("%s: file = %q, %v", tc.name, data, err)
			}
			continue
		}
		if err == nil || strings.Contains(err.Error(), "storage.example") {
			t.Errorf("%s: %v", tc.name, err)
		}
		for _, c := range calls[min(1, len(calls)):] {
			if c.auth != "" || c.path != raw {
				t.Errorf("%s: second request %+v", tc.name, c)
			}
		}
		if len(calls) > 2 {
			t.Errorf("%s: calls %v", tc.name, apiMethods(e))
		}
		if entries, _ := os.ReadDir(dirs.write); len(entries) != 0 {
			t.Errorf("%s: files left: %v", tc.name, entries)
		}
	}
	// Forgejo never follows a redirect.
	e, dirs := newActionsEnvironment(t, forgejo, "16.0.6", func(c call) *http.Response { return redirect(own) })
	if _, err := e.invoke(t, workflowArtifactsDownload, "files",
		`{"artifact_id":5,"local_path":`+quote(filepath.Join(dirs.write, "f.zip"))+`}`); err == nil || len(apiCalls(e)) != 1 {
		t.Errorf("forgejo: %v, %v", err, apiMethods(e))
	}
}
