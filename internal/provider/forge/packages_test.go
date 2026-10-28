package forge

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	packageRow = `{"id":5,"name":"@scope/lib","type":"npm","version":"1.0.0","html_url":"https://forge.example.test/acme/-/packages/npm/lib/1.0.0",` +
		`"created_at":"2030-01-01T00:00:00Z","creator":{"id":1,"login":"bob"},"owner":{"id":2,"login":"acme"},` +
		`"repository":{"id":9,"name":"widget","full_name":"acme/widget"}}`
	foreignPackageRow = `{"id":6,"name":"other","type":"npm","version":"2.0.0","owner":{"id":3,"login":"mallory"}}`
)

func packageServer(p *product, extra func(call) *http.Response) func(call) *http.Response {
	api := func(c call) *http.Response {
		if extra != nil {
			if r := extra(c); r != nil {
				return r
			}
		}
		switch {
		case c.method == http.MethodGet && c.path == "/api/v1/packages/acme":
			return jsonResponse(200, `[`+packageRow+`,`+foreignPackageRow+`]`, "X-Total-Count", "2")
		case c.method == http.MethodGet && strings.HasSuffix(c.path, "/files"):
			if p == forgejo {
				return jsonResponse(200, `[{"id":1,"name":"a.tgz","Size":12,"size":99,"md5":"m"}]`)
			}
			return jsonResponse(200, `[{"id":1,"name":"a.tgz","size":12,"md5":"m"}]`)
		case c.method == http.MethodGet:
			return jsonResponse(200, packageRow)
		case c.method == http.MethodPost || c.method == http.MethodDelete:
			return jsonResponse(204, "")
		}
		return notFoundResponse()
	}
	if p == forgejo {
		return forgejoAt("17.0.0", api)
	}
	return giteaAt("28.2.0", api)
}

func TestPackageToolsRegisteredPerProduct(t *testing.T) {
	reg := registry(t)
	giteaOnlyTools := []*tool{packageVersionsList, packageVersionsGetLatest, packagesDelete}
	both := []*tool{packagesList, packagesGet, packageFilesList, packagesLink, packagesUnlink, packagesDeleteVersion}
	for _, p := range products {
		for _, tl := range both {
			if _, _, ok := reg.Lookup(p.toolID(tl)); !ok {
				t.Errorf("%s misses %s", p.id, tl.group+"."+tl.action)
			}
		}
		for _, tl := range giteaOnlyTools {
			if _, _, ok := reg.Lookup(p.toolID(tl)); ok != (p == gitea) {
				t.Errorf("%s: %s registered = %v", p.id, tl.group+"."+tl.action, ok)
			}
		}
		metadata, _ := reg.ProviderMetadata(p.id)
		for _, profile := range metadata.Profiles {
			for _, id := range profile.Tools {
				if strings.Contains(id, ".packages.") || strings.Contains(id, ".package-") {
					t.Errorf("%s: profile %s holds %s", p.id, profile.ID, id)
				}
			}
		}
		d, _, _ := reg.Lookup(p.toolID(packagesList))
		hasTerraform := strings.Contains(string(d.InputSchema), "terraform")
		if hasTerraform != (p == gitea) {
			t.Errorf("%s: terraform in type enum = %v", p.id, hasTerraform)
		}
		for _, tl := range []*tool{packagesDeleteVersion, packagesDelete} {
			if d, _, ok := reg.Lookup(p.toolID(tl)); ok && (!d.RequiresToolAllowList ||
				d.Risk.Effect != "delete" || d.Risk.Confirmation != "required") {
				t.Errorf("%s: %s = %+v", p.id, tl.action, d)
			}
		}
	}
}

func TestPackagesListFiltersRowsAndPages(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, packageServer(p, nil))
		result, err := e.invoke(t, packagesList, "org", `{"owner":"orgs/acme","type":"npm","q":"li","limit":10}`)
		if err != nil {
			t.Fatalf("%s: %v", p.id, err)
		}
		rows, _ := result["packages"].([]any)
		if len(rows) != 1 || rows[0].(map[string]any)["name"] != "@scope/lib" || result["total_count"] != float64(2) ||
			result["owner"] != "orgs/acme" || result["has_more"] != false {
			t.Errorf("%s: %v", p.id, result)
		}
		if rows[0].(map[string]any)["repository"].(map[string]any)["full_name"] != "acme/widget" {
			t.Errorf("%s: repository missing: %v", p.id, rows[0])
		}
		if got := (*e.calls)[len(*e.calls)-1]; got.query != "limit=10&page=1&q=li&type=npm" {
			t.Errorf("%s: query %q", p.id, got.query)
		}
	}
}

func TestPackagesListHasMoreFromLink(t *testing.T) {
	e := newEnvironment(t, gitea, packageServer(gitea, func(c call) *http.Response {
		if c.path == "/api/v1/packages/acme" {
			return jsonResponse(200, `[`+packageRow+`]`, "Link", `<https://forge.example.test/x?page=2>; rel="next"`)
		}
		return nil
	}))
	result, err := e.invoke(t, packagesList, "open", `{"owner":"orgs/acme"}`)
	if err != nil || result["has_more"] != true || result["next_cursor"] == "" {
		t.Fatalf("%v, %v", result, err)
	}
}

func TestPackageOwnerOutsideTargetsRefusedBeforeIO(t *testing.T) {
	cases := []struct {
		name, connection, arguments string
		tl                          *tool
	}{
		{"no owner", "open", `{"type":"npm"}`, packagesList},
		{"other owner", "org", `{"owner":"orgs/other"}`, packagesList},
		{"other scope", "org", `{"owner":"users/acme"}`, packagesList},
		{"repository target", "repo", `{"owner":"orgs/acme"}`, packagesList},
		{"get", "owner", `{"owner":"orgs/acme","type":"npm","name":"n","version":"1"}`, packagesGet},
		{"files", "org", `{"owner":"orgs/x","type":"npm","name":"n","version":"1"}`, packageFilesList},
		{"link", "org", `{"owner":"orgs/x","type":"npm","name":"n","repo_name":"widget"}`, packagesLink},
		{"unlink", "org", `{"owner":"orgs/x","type":"npm","name":"n"}`, packagesUnlink},
		{"delete version", "package-delete", `{"owner":"orgs/x","type":"npm","name":"n","version":"1"}`, packagesDeleteVersion},
	}
	for _, p := range products {
		for _, c := range cases {
			e := newEnvironment(t, p, packageServer(p, nil))
			_, err := e.invoke(t, c.tl, c.connection, c.arguments)
			if err == nil || !isInvalidRequest(err) || len(*e.calls) != 0 || *e.reads != 0 ||
				strings.Contains(err.Error(), "other") || strings.Contains(err.Error(), "orgs/x") {
				t.Errorf("%s/%s: err=%v calls=%v reads=%d", p.id, c.name, err, *e.calls, *e.reads)
			}
		}
	}
}

func TestPackageArgumentsAreChecked(t *testing.T) {
	base := `"owner":"orgs/acme","type":"npm"`
	cases := map[string]string{
		"dot name":       `{` + base + `,"name":"..","version":"1"}`,
		"dot part":       `{` + base + `,"name":"a/../../admin","version":"1"}`,
		"hyphen part":    `{` + base + `,"name":"a/-/link","version":"1"}`,
		"version part":   `{` + base + `,"name":"n","version":"1/.."}`,
		"hyphen version": `{` + base + `,"name":"n","version":"-"}`,
		"control":        `{` + base + `,"name":"n\u0000","version":"1"}`,
		"unknown type":   `{"owner":"orgs/acme","type":"bogus","name":"n","version":"1"}`,
		"empty version":  `{` + base + `,"name":"n","version":""}`,
	}
	for _, p := range products {
		for name, arguments := range cases {
			e := newEnvironment(t, p, packageServer(p, nil))
			if _, err := e.invoke(t, packagesGet, "open", arguments); err == nil || !isInvalidRequest(err) ||
				len(*e.calls) != 0 || *e.reads != 0 {
				t.Errorf("%s/%s: %v %v", p.id, name, err, *e.calls)
			}
		}
	}
	// terraform exists on Gitea only.
	e := newEnvironment(t, forgejo, packageServer(forgejo, nil))
	if _, err := e.invoke(t, packagesList, "open", `{"owner":"orgs/acme","type":"terraform"}`); err == nil ||
		!isInvalidRequest(err) || len(*e.calls) != 0 || *e.reads != 0 {
		t.Errorf("forgejo terraform: %v", err)
	}
	e = newEnvironment(t, gitea, packageServer(gitea, nil))
	if _, err := e.invoke(t, packagesList, "open", `{"owner":"orgs/acme","type":"terraform"}`); err != nil {
		t.Errorf("gitea terraform: %v", err)
	}
}

func TestPackageNameWithSlashIsOneEscapedSegment(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, packageServer(p, nil))
		result, err := e.invoke(t, packagesGet, "org", `{"owner":"orgs/acme","type":"container","name":"app/web","version":"sha256:ab"}`)
		if err != nil || result["name"] != "@scope/lib" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		got := (*e.calls)[len(*e.calls)-1]
		if got.escaped != "/api/v1/packages/acme/container/app%2Fweb/sha256:ab" {
			t.Errorf("%s: escaped path %q", p.id, got.escaped)
		}
	}
}

func TestPackageGetRefusesAnotherOwnersAnswer(t *testing.T) {
	e := newEnvironment(t, gitea, packageServer(gitea, func(c call) *http.Response {
		if strings.HasPrefix(c.path, "/api/v1/packages/acme/npm") {
			return jsonResponse(200, foreignPackageRow)
		}
		return nil
	}))
	_, err := e.invoke(t, packagesGet, "org", `{"owner":"orgs/acme","type":"npm","name":"n","version":"1"}`)
	if err == nil || classOfError(err) != provider.ClassNotFound || strings.Contains(err.Error(), "mallory") {
		t.Errorf("err = %v", err)
	}
}

func TestPackageFilesUnifySize(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, packageServer(p, nil))
		result, err := e.invoke(t, packageFilesList, "org", `{"owner":"orgs/acme","type":"npm","name":"n","version":"1"}`)
		files, _ := result["files"].([]any)
		if err != nil || len(files) != 1 {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		file := files[0].(map[string]any)
		if file["size"] != float64(12) || file["Size"] != nil || file["md5"] != "m" {
			t.Errorf("%s: file %v", p.id, file)
		}
	}
}

func TestGiteaPackageVersionTools(t *testing.T) {
	e := newEnvironment(t, gitea, packageServer(gitea, func(c call) *http.Response {
		if c.path == "/api/v1/packages/acme/npm/n" {
			return jsonResponse(200, `[`+packageRow+`,`+foreignPackageRow+`]`)
		}
		return nil
	}))
	result, err := e.invoke(t, packageVersionsList, "org", `{"owner":"orgs/acme","type":"npm","name":"n"}`)
	if rows, _ := result["packages"].([]any); err != nil || len(rows) != 1 {
		t.Fatalf("%v, %v", result, err)
	}
	if _, err := e.invoke(t, packageVersionsGetLatest, "org", `{"owner":"orgs/acme","type":"npm","name":"n"}`); err != nil {
		t.Fatal(err)
	}
	if got := methods(e); !reflect.DeepEqual(got[len(got)-2:], []string{"GET /api/v1/packages/acme/npm/n",
		"GET /api/v1/packages/acme/npm/n/-/latest"}) {
		t.Errorf("calls = %v", got)
	}
}

func TestPackageLinkAndUnlinkSendOneRequest(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, packageServer(p, nil))
		if _, err := e.invokeAs(t, packagesLink, "org", `{"owner":"orgs/acme","type":"npm","name":"@s/n","repo_name":"widget"}`, false); err == nil ||
			len(*e.calls) != 0 {
			t.Errorf("%s: unconfirmed link: %v", p.id, err)
		}
		result, err := e.invoke(t, packagesLink, "org", `{"owner":"orgs/acme","type":"npm","name":"@s/n","repo_name":"widget"}`)
		if err != nil || result["linked"] != true || result["repo_name"] != "widget" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		call := (*e.calls)[len(*e.calls)-1]
		if call.method != http.MethodPost || call.escaped != "/api/v1/packages/acme/npm/@s%2Fn/-/link/widget" || call.body != "" {
			t.Errorf("%s: %+v", p.id, call)
		}
		before := len(*e.calls)
		if result, err = e.invoke(t, packagesUnlink, "org", `{"owner":"orgs/acme","type":"npm","name":"n"}`); err != nil ||
			result["unlinked"] != true || len(*e.calls) != before+1 ||
			(*e.calls)[before].path != "/api/v1/packages/acme/npm/n/-/unlink" {
			t.Errorf("%s: unlink %v, %v", p.id, result, err)
		}
	}
}

func TestPackageLinkRefusesRepositoryOutsideTargets(t *testing.T) {
	// A repository of the owner follows the owner; the check also runs for the repository name.
	allowed := allowlist{targets: []target{{kind: kindOwner, scope: "orgs", owner: "acme"}}}
	if allowed.allowsRepository("acme", "widget") != true || allowed.allowsRepository("other", "widget") {
		t.Fatal("allowsRepository")
	}
	e := newEnvironment(t, gitea, packageServer(gitea, nil))
	if _, err := e.invoke(t, packagesLink, "org", `{"owner":"orgs/acme","type":"npm","name":"n","repo_name":"../x"}`); err == nil ||
		!isInvalidRequest(err) || len(*e.calls) != 0 {
		t.Errorf("err = %v", err)
	}
}

func TestPackageChangeIsNotRetriedAfterServerError(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, packageServer(p, func(c call) *http.Response {
			if c.method == http.MethodPost {
				return jsonResponse(502, `{"message":"`+bodyCanary+`"}`)
			}
			return nil
		}))
		_, err := e.invoke(t, packagesUnlink, "org", `{"owner":"orgs/acme","type":"npm","name":"n"}`)
		if err == nil || !strings.Contains(err.Error(), "may have been applied") {
			t.Errorf("%s: err = %v", p.id, err)
		}
		posts := 0
		for _, c := range *e.calls {
			if c.method == http.MethodPost {
				posts++
			}
		}
		if posts != 1 {
			t.Errorf("%s: %d posts", p.id, posts)
		}
	}
}

func TestPackageDeletesAreListedOnly(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, packageServer(p, nil))
		args := `{"owner":"orgs/acme","type":"npm","name":"n","version":"1"}`
		if _, err := e.invoke(t, packagesDeleteVersion, "org", args); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: delete-version without a tools list: %v", p.id, err)
		}
		if _, err := e.invokeAs(t, packagesDeleteVersion, "package-delete", args, false); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: unconfirmed delete: %v", p.id, err)
		}
		result, err := e.invoke(t, packagesDeleteVersion, "package-delete", args)
		call := (*e.calls)[len(*e.calls)-1]
		if err != nil || result["deleted"] != true || call.method != http.MethodDelete ||
			call.path != "/api/v1/packages/acme/npm/n/1" {
			t.Errorf("%s: %v, %v, %+v", p.id, result, err, call)
		}
		if p == gitea {
			if _, err := e.invoke(t, packagesDelete, "org", `{"owner":"orgs/acme","type":"npm","name":"n"}`); err == nil {
				t.Errorf("delete without a tools list")
			}
			result, err := e.invoke(t, packagesDelete, "package-delete", `{"owner":"orgs/acme","type":"npm","name":"n"}`)
			call := (*e.calls)[len(*e.calls)-1]
			if err != nil || result["deleted"] != true || call.method != http.MethodDelete || call.path != "/api/v1/packages/acme/npm/n" {
				t.Errorf("delete: %v, %v, %+v", result, err, call)
			}
		}
	}
}

func TestPackagesOfAnExcludedOwnerAreRefusedBeforeIO(t *testing.T) {
	cases := []struct {
		tl        *tool
		arguments string
	}{
		{packagesList, `{"owner":"orgs/other"}`},
		{packagesGet, `{"owner":"users/Other","type":"npm","name":"n","version":"1"}`},
		{packageFilesList, `{"owner":"orgs/other","type":"npm","name":"n","version":"1"}`},
		{packagesLink, `{"owner":"orgs/other","type":"npm","name":"n","repo_name":"widget"}`},
		{packagesUnlink, `{"owner":"orgs/other","type":"npm","name":"n"}`},
		// A repository under the owner of the call that an exclusion takes out cannot be linked.
		{packagesLink, `{"owner":"orgs/acme","type":"npm","name":"n","repo_name":"Gadget"}`},
	}
	for _, p := range products {
		for _, c := range cases {
			e := newEnvironment(t, p, packageServer(p, nil))
			_, err := e.invoke(t, c.tl, "exclude", c.arguments)
			if err == nil || !isInvalidRequest(err) || len(*e.calls) != 0 || *e.reads != 0 ||
				strings.Contains(strings.ToLower(err.Error()), "/other") || strings.Contains(err.Error(), "adget") ||
				strings.Contains(err.Error(), "exclu") {
				t.Errorf("%s %s %s: err=%v calls=%v reads=%d", p.id, c.tl.action, c.arguments, err, *e.calls, *e.reads)
			}
		}
		// An exclusion of another owner leaves the owner and the link of an unrelated repository alone.
		e := newEnvironment(t, p, packageServer(p, nil))
		if _, err := e.invoke(t, packagesLink, "exclude", `{"owner":"orgs/acme","type":"npm","name":"n","repo_name":"widget"}`); err != nil {
			t.Errorf("%s: link to a reachable repository: %v", p.id, err)
		}
	}
}

func TestPackagesDropAnExcludedRepositoryLinkAndParent(t *testing.T) {
	linked := func(repository string) string {
		return `{"id":5,"name":"lib","type":"npm","version":"1.0.0","owner":{"id":2,"login":"acme"},"repository":` +
			repository + `}`
	}
	for _, p := range products {
		for name, tc := range map[string]struct {
			repository string
			want       bool
			parent     bool
		}{
			"reachable":             {`{"id":9,"name":"widget","full_name":"acme/widget"}`, true, false},
			"excluded":              {`{"id":9,"name":"gadget","full_name":"Acme/Gadget"}`, false, false},
			"without a name":        {`{"id":9,"name":"widget"}`, false, false},
			"excluded fork parent":  {`{"id":9,"name":"widget","full_name":"acme/widget","parent":{"id":4,"full_name":"other/up"}}`, true, false},
			"reachable fork parent": {`{"id":9,"name":"widget","full_name":"acme/widget","parent":{"id":4,"full_name":"zed/up"}}`, true, true},
		} {
			row := linked(tc.repository)
			e := newEnvironment(t, p, packageServer(p, func(c call) *http.Response {
				switch c.path {
				case "/api/v1/packages/acme":
					return jsonResponse(200, `[`+row+`]`)
				case "/api/v1/packages/acme/npm/lib/1.0.0":
					return jsonResponse(200, row)
				}
				return nil
			}))
			list, err := e.invoke(t, packagesList, "exclude", `{"owner":"orgs/acme"}`)
			got, err2 := e.invoke(t, packagesGet, "exclude", `{"owner":"orgs/acme","type":"npm","name":"lib","version":"1.0.0"}`)
			if err != nil || err2 != nil || len(rowsOf(t, list, "packages")) != 1 {
				t.Fatalf("%s %s: %v %v %v", p.id, name, list, err, err2)
			}
			for what, shown := range map[string]map[string]any{"list": rowsOf(t, list, "packages")[0], "get": got} {
				repository, has := shown["repository"].(map[string]any)
				if has != tc.want {
					t.Errorf("%s %s %s: repository shown = %t", p.id, name, what, has)
				}
				if has {
					if _, parent := repository["parent"]; parent != tc.parent {
						t.Errorf("%s %s %s: parent shown = %t", p.id, name, what, parent)
					}
				}
				if shown["name"] != "lib" {
					t.Errorf("%s %s %s: the package vanished: %v", p.id, name, what, shown)
				}
			}
		}
	}
}
