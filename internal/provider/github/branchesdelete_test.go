package github

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/application"
	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
)

const featureHead = "cafed00dcafed00dcafed00dcafed00dcafed00d"

func branchDeleteCore(t *testing.T, reads *int) (*fakeGitHub, *fakeContentsWrite, *application.Core) {
	t.Helper()
	f, w, base := serveContentsWrite(t)
	w.defaultBranch = "main"
	w.refs["main"] = strings.Repeat("a", 40)
	w.refs["feature/login"] = featureHead
	red := &redact.Redactor{}
	return f, w, application.New(registry(t), contentsWriteConfig(base), resolver(red, reads), red)
}

func deletes(f *fakeGitHub) int {
	n := 0
	for _, r := range f.recorded() {
		if r.method == http.MethodDelete {
			n++
		}
	}
	return n
}

func TestBranchesDeleteRemovesABranchWithOneDelete(t *testing.T) {
	f, w, core := branchDeleteCore(t, nil)
	out, err := invoke(t, core, branchesDelete.ID, "repo-listed", `{"branch":"feature/login"}`, true)
	if err != nil || !strings.Contains(string(out), `"ref":"refs/heads/feature/login"`) ||
		!strings.Contains(string(out), `"branch":"feature/login"`) || strings.Contains(string(out), `"sha"`) {
		t.Fatalf("delete = %s, %v", out, err)
	}
	if _, exists := w.refs["feature/login"]; exists || deletes(f) != 1 {
		t.Errorf("branch kept or deletes = %d", deletes(f))
	}
	last := f.recorded()[len(f.recorded())-1]
	if last.method != http.MethodDelete || last.path != actionsPrefix+"git/refs/heads/feature%2Flogin" &&
		last.path != actionsPrefix+"git/refs/heads/feature/login" {
		t.Errorf("last request = %+v", last)
	}
}

func TestBranchesDeleteComparesExpectedSHAFirst(t *testing.T) {
	f, w, core := branchDeleteCore(t, nil)
	_, err := invoke(t, core, branchesDelete.ID, "repo-listed",
		`{"branch":"feature/login","expected_sha":"`+strings.Repeat("b", 40)+`"}`, true)
	if !isInvalidRequest(err) || !strings.Contains(err.Error(), featureHead) {
		t.Errorf("mismatch = %v, want an invalid request naming the current sha", err)
	}
	if deletes(f) != 0 || w.refs["feature/login"] == "" {
		t.Errorf("a mismatching expected_sha still deleted")
	}
	out, err := invoke(t, core, branchesDelete.ID, "repo-listed",
		`{"branch":"feature/login","expected_sha":"`+featureHead+`"}`, true)
	if err != nil || !strings.Contains(string(out), `"sha":"`+featureHead+`"`) || deletes(f) != 1 {
		t.Errorf("match = %s, %v, deletes %d", out, err, deletes(f))
	}
}

func TestBranchesDeleteRefusesTheDefaultBranchWithoutTouchingTheRef(t *testing.T) {
	f, w, core := branchDeleteCore(t, nil)
	_, err := invoke(t, core, branchesDelete.ID, "repo-listed", `{"branch":"main","expected_sha":"`+
		strings.Repeat("a", 40)+`"}`, true)
	if !isInvalidRequest(err) {
		t.Errorf("default branch = %v, want an invalid request", err)
	}
	for _, r := range f.recorded() {
		if strings.Contains(r.path, "git/refs") {
			t.Errorf("request %s %s touched the ref", r.method, r.path)
		}
	}
	if _, exists := w.refs["main"]; !exists {
		t.Error("default branch was deleted")
	}
}

func TestBranchesDeleteMapsRefusalsWithoutProviderText(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
		class  provider.Class
	}{
		{"missing 422", http.StatusUnprocessableEntity, `{"message":"Reference does not exist"}`, provider.ClassNotFound},
		{"missing 404", http.StatusNotFound, `{"message":"Not Found"}`, provider.ClassNotFound},
		{"protected 422", http.StatusUnprocessableEntity, `{"message":"Cannot delete this protected branch SECRETTEXT"}`, provider.ClassPermission},
		{"ruleset 422", http.StatusUnprocessableEntity, `{"message":"Repository rule violations found SECRETTEXT"}`, provider.ClassPermission},
		{"forbidden", http.StatusForbidden, `{"message":"Resource not accessible SECRETTEXT"}`, provider.ClassPermission},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f, w, core := branchDeleteCore(t, nil)
			w.refDelete = func(rw http.ResponseWriter) {
				rw.WriteHeader(tt.status)
				fmt.Fprint(rw, tt.body)
			}
			_, err := invoke(t, core, branchesDelete.ID, "repo-listed", `{"branch":"feature/login"}`, true)
			if classOf(err) != tt.class || err == nil || strings.Contains(err.Error(), "SECRETTEXT") {
				t.Errorf("err = %v, want class %s without provider text", err, tt.class)
			}
			if tt.class == provider.ClassPermission && !strings.Contains(err.Error(), "Contents") {
				t.Errorf("permission message = %v, want the needed rights", err)
			}
			if deletes(f) != 1 {
				t.Errorf("deletes = %d, want 1", deletes(f))
			}
		})
	}
}

func TestBranchesDeleteNeverRepeatsAnUnclearDelete(t *testing.T) {
	for name, answer := range map[string]func(http.ResponseWriter){
		"server error":    func(rw http.ResponseWriter) { rw.WriteHeader(http.StatusBadGateway) },
		"gateway timeout": func(rw http.ResponseWriter) { rw.WriteHeader(http.StatusGatewayTimeout) },
		"unreadable":      func(rw http.ResponseWriter) { rw.WriteHeader(http.StatusOK); fmt.Fprint(rw, "x") },
		"dropped connection": func(rw http.ResponseWriter) {
			if connection, _, err := rw.(http.Hijacker).Hijack(); err == nil {
				connection.Close()
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f, w, core := branchDeleteCore(t, nil)
			w.refDelete = answer
			_, err := invoke(t, core, branchesDelete.ID, "repo-listed", `{"branch":"feature/login"}`, true)
			if err == nil || !strings.Contains(err.Error(), "may have been applied") {
				t.Errorf("err = %v, want an unclear outcome", err)
			}
			if deletes(f) != 1 {
				t.Errorf("deletes = %d, want exactly 1", deletes(f))
			}
		})
	}
}

func TestBranchesDeleteChecksArgumentsAndTargetBeforeSecrets(t *testing.T) {
	reads := 0
	f, _, core := branchDeleteCore(t, &reads)
	for name, arguments := range map[string]string{
		"empty":      `{"branch":""}`,
		"prefix":     `{"branch":"refs/heads/feature/login"}`,
		"bad sha":    `{"branch":"x","expected_sha":"nothex"}`,
		"foreign":    `{"branch":"x","repository":"other-org/other"}`,
		"unexpected": `{"branch":"x","force":true}`,
	} {
		reads = 0
		before := len(f.recorded())
		if _, err := invoke(t, core, branchesDelete.ID, "repo-listed", arguments, true); err == nil {
			t.Errorf("%s accepted", name)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s reached the credential or GitHub", name)
		}
	}
	reads = 0
	if _, err := invoke(t, core, branchesDelete.ID, "repo-listed", `{"branch":"feature/login"}`, false); err == nil || reads != 0 {
		t.Errorf("unconfirmed = %v, reads %d", err, reads)
	}
}

func TestBranchesDeleteIsOfferedOnlyWhereListed(t *testing.T) {
	_, _, core := branchDeleteCore(t, nil)
	var unsupported *capability.UnsupportedError
	if _, err := invoke(t, core, branchesDelete.ID, "repo", `{"branch":"feature/login"}`, true); !errors.As(err, &unsupported) {
		t.Errorf("without a tools entry = %v, want unsupported-capability", err)
	}
}
