package infomaniakdrive

import (
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// The tree of the fake drive: a files target, an object below it, and objects outside of it. The identifiers
// are distinctive so a test can prove that no refusal quotes one.
const (
	subTarget  int64 = 7042
	subInside  int64 = 7043
	subOutside int64 = 7050
	subOther   int64 = 7060
	subName          = "secret-name-canary-77"
)

var subParents = map[int64][]int64{
	subTarget:  {rootID},
	subInside:  {rootID, subTarget},
	subOutside: {rootID},
	subOther:   {rootID},
}

var fileReadPath = regexp.MustCompile(`^/3/drive/5001/files/(\d+)$`)

// subtreeFake answers the drive tree. mode changes the answer of a read with parents: "" is honest, "noparents"
// omits them, "5xx" fails, "404" answers not found.
type subtreeFake struct{ mode string }

func (f subtreeFake) handler(t *testing.T) func(*http.Request) (*http.Response, error) {
	return withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
		if match := fileReadPath.FindStringSubmatch(r.URL.Path); match != nil && r.Method == http.MethodGet {
			id, _ := strconv.ParseInt(match[1], 10, 64)
			if f.mode != "plain" && r.URL.Query().Get("with") != "parents" {
				t.Errorf("a read of %s asked for no parents", r.URL.Path)
			}
			switch f.mode {
			case "5xx":
				return jsonResponse(500, `{"result":"error"}`), nil
			case "404":
				return jsonResponse(404, `{"result":"error"}`), nil
			}
			parents, known := subParents[id]
			if !known {
				return jsonResponse(404, `{"result":"error"}`), nil
			}
			body := fmt.Sprintf(`{"id":%d,"name":%q,"type":"dir","parent_id":%d,"status":"ok","last_modified_at":1735776000`,
				id, subName, parents[len(parents)-1])
			if f.mode != "noparents" {
				var list []string
				for _, parent := range parents {
					list = append(list, fmt.Sprintf(`{"id":%d}`, parent))
				}
				body += `,"parents":[` + strings.Join(list, ",") + `]`
			}
			return jsonResponse(200, envelopeSuccess(body+"}")), nil
		}
		switch {
		case r.URL.Path == usersPathOf():
			return jsonResponse(200, usersPage(1, 1, memberJSONOf(memberUser, "active", memberTeam))), nil
		case strings.HasSuffix(r.URL.Path, "/download"):
			return &http.Response{StatusCode: 200, ContentLength: 4, Body: httpBody("data")}, nil
		case strings.HasSuffix(r.URL.Path, "/files"):
			return jsonResponse(200, `{"result":"success","data":[],"cursor":"","has_more":false}`), nil
		}
		return jsonResponse(200, envelopeSuccess(`true`)), nil
	})
}

// subtreeEnvironment is the core with one connection "sub" that lists every tool and releases dir.
func subtreeEnvironment(t *testing.T, dir string, calls *[]call, handler func(*http.Request) (*http.Response, error),
	targets ...string) *environment {
	t.Helper()
	serve(t, calls, handler, nil)
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	var tools []string
	for _, tool := range metadata.Tools {
		tools = append(tools, tool.ID)
	}
	cfg := coreConfig()
	cfg.Connections["sub"] = config.Connection{Service: "im", Credential: "im-reader", Tools: tools,
		Files: config.Files{Read: []string{dir}, Write: []string{dir}}, Targets: targets}
	reads := 0
	red := &redact.Redactor{}
	return &environment{core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

func subTargets(files ...int64) []string {
	targets := []string{"account/" + strconv.FormatInt(ownAccount, 10)}
	for _, id := range files {
		targets = append(targets, fmt.Sprintf("drive/%d/files/%d", ownDrive, id))
	}
	return targets
}

// objectCase is one tool with object identifiers: its arguments for an object and a destination, how many
// proof reads it needs, and the request its effect is.
type objectCase struct {
	name, tool string
	// proofs is the number of objects it proves: 1, or 2 with a destination.
	proofs int
	args   func(dir string, object, destination int64) string
	// effect is the method and path of the request that has the effect; for a read it is the read itself.
	effect func(object, destination int64) (string, string)
	// proofIsEffect marks a tool whose effect is the very read that proves the object.
	proofIsEffect bool
}

func driveArgs(extra string) string { return fmt.Sprintf(`{"drive_id":%d%s}`, ownDrive, extra) }

func objectCases() []objectCase {
	file := func(id int64) string { return fmt.Sprintf("/3/drive/%d/files/%d", ownDrive, id) }
	legacy := func(id int64) string { return fmt.Sprintf("/2/drive/%d/files/%d", ownDrive, id) }
	return []objectCase{
		{name: "list", tool: filesList.ID, proofs: 1,
			args:   func(_ string, o, _ int64) string { return driveArgs(fmt.Sprintf(`,"folder_id":%d`, o)) },
			effect: func(o, _ int64) (string, string) { return "GET", file(o) + "/files" }},
		{name: "stat", tool: filesStat.ID, proofs: 1, proofIsEffect: true,
			args:   func(_ string, o, _ int64) string { return driveArgs(fmt.Sprintf(`,"file_id":%d`, o)) },
			effect: func(o, _ int64) (string, string) { return "GET", file(o) }},
		{name: "get", tool: filesGet.ID, proofs: 1,
			args:   func(_ string, o, _ int64) string { return driveArgs(fmt.Sprintf(`,"file_id":%d`, o)) },
			effect: func(o, _ int64) (string, string) { return "GET", legacy(o) + "/download" }},
		{name: "download", tool: filesDownload.ID, proofs: 1,
			args: func(dir string, o, _ int64) string {
				return driveArgs(fmt.Sprintf(`,"file_id":%d,"local_path":%q`, o, filepath.Join(dir, "out.bin")))
			},
			effect: func(o, _ int64) (string, string) { return "GET", legacy(o) + "/download" }},
		{name: "upload new", tool: filesUpload.ID, proofs: 1,
			args: func(_ string, o, _ int64) string {
				return driveArgs(fmt.Sprintf(`,"directory_id":%d,"name":"a.txt","content_base64":"ZGF0YQ=="`, o))
			},
			effect: func(_, _ int64) (string, string) { return "POST", fmt.Sprintf("/3/drive/%d/upload", ownDrive) }},
		{name: "upload replace", tool: filesUpload.ID, proofs: 1,
			args: func(_ string, o, _ int64) string {
				return driveArgs(fmt.Sprintf(`,"file_id":%d,"etag":%q,"content_base64":"ZGF0YQ=="`, o, knownEtag))
			},
			effect: func(_, _ int64) (string, string) { return "POST", fmt.Sprintf("/3/drive/%d/upload", ownDrive) }},
		{name: "folders create", tool: foldersCreate.ID, proofs: 1,
			args:   func(_ string, o, _ int64) string { return driveArgs(fmt.Sprintf(`,"parent_id":%d,"name":"New"`, o)) },
			effect: func(o, _ int64) (string, string) { return "POST", file(o) + "/directory" }},
		{name: "rename", tool: filesRename.ID, proofs: 1,
			args:   func(_ string, o, _ int64) string { return driveArgs(fmt.Sprintf(`,"file_id":%d,"name":"New"`, o)) },
			effect: func(o, _ int64) (string, string) { return "POST", legacy(o) + "/rename" }},
		{name: "move", tool: filesMove.ID, proofs: 2,
			args: func(_ string, o, d int64) string {
				return driveArgs(fmt.Sprintf(`,"file_id":%d,"destination_id":%d`, o, d))
			},
			effect: func(o, d int64) (string, string) { return "POST", fmt.Sprintf("%s/move/%d", file(o), d) }},
		{name: "copy", tool: filesCopy.ID, proofs: 2,
			args: func(_ string, o, d int64) string {
				return driveArgs(fmt.Sprintf(`,"file_id":%d,"destination_id":%d`, o, d))
			},
			effect: func(o, d int64) (string, string) { return "POST", fmt.Sprintf("%s/copy/%d", file(o), d) }},
		{name: "trash", tool: filesTrash.ID, proofs: 1,
			args:   func(_ string, o, _ int64) string { return driveArgs(fmt.Sprintf(`,"file_id":%d`, o)) },
			effect: func(o, _ int64) (string, string) { return "DELETE", legacy(o) }},
		{name: "links get", tool: linksGet.ID, proofs: 1,
			args:   func(_ string, o, _ int64) string { return driveArgs(fmt.Sprintf(`,"file_id":%d`, o)) },
			effect: func(o, _ int64) (string, string) { return "GET", legacy(o) + "/link" }},
		{name: "links delete", tool: linksDelete.ID, proofs: 1,
			args:   func(_ string, o, _ int64) string { return driveArgs(fmt.Sprintf(`,"file_id":%d`, o)) },
			effect: func(o, _ int64) (string, string) { return "DELETE", legacy(o) + "/link" }},
		{name: "dropbox get", tool: dropboxGet.ID, proofs: 1,
			args:   func(_ string, o, _ int64) string { return driveArgs(fmt.Sprintf(`,"file_id":%d`, o)) },
			effect: func(o, _ int64) (string, string) { return "GET", legacy(o) + "/dropbox" }},
		{name: "dropbox delete", tool: dropboxDelete.ID, proofs: 1,
			args:   func(_ string, o, _ int64) string { return driveArgs(fmt.Sprintf(`,"file_id":%d`, o)) },
			effect: func(o, _ int64) (string, string) { return "DELETE", legacy(o) + "/dropbox" }},
		{name: "access get", tool: accessGet.ID, proofs: 1,
			args:   func(_ string, o, _ int64) string { return driveArgs(fmt.Sprintf(`,"file_id":%d`, o)) },
			effect: func(o, _ int64) (string, string) { return "GET", legacy(o) + "/access" }},
		{name: "access grant", tool: accessGrant.ID, proofs: 1,
			args: func(_ string, o, _ int64) string {
				return driveArgs(fmt.Sprintf(`,"file_id":%d,"right":"read","user_ids":[%d]`, o, memberUser))
			},
			effect: func(o, _ int64) (string, string) { return "POST", legacy(o) + "/access" }},
		{name: "access update", tool: accessUpdate.ID, proofs: 1,
			args: func(_ string, o, _ int64) string {
				return driveArgs(fmt.Sprintf(`,"file_id":%d,"user_id":%d,"right":"read"`, o, memberUser))
			},
			effect: func(o, _ int64) (string, string) {
				return "PUT", fmt.Sprintf("%s/access/users/%d", legacy(o), memberUser)
			}},
		{name: "access revoke", tool: accessRevoke.ID, proofs: 1,
			args: func(_ string, o, _ int64) string {
				return driveArgs(fmt.Sprintf(`,"file_id":%d,"user_id":%d`, o, memberUser))
			},
			effect: func(o, _ int64) (string, string) {
				return "DELETE", fmt.Sprintf("%s/access/users/%d", legacy(o), memberUser)
			}},
	}
}

// proofReads counts the reads of one object with its parents, whatever object.
func proofReads(calls []call) int {
	count := 0
	for _, c := range calls {
		if c.method == http.MethodGet && fileReadPath.MatchString(c.path) && c.query.Get("with") == "parents" {
			count++
		}
	}
	return count
}

func sent(calls []call, method, path string) bool {
	for _, c := range calls {
		if c.method == method && c.path == path {
			return true
		}
	}
	return false
}

func noIdentifiers(t *testing.T, err error, ids ...int64) {
	t.Helper()
	if err == nil {
		return
	}
	for _, id := range ids {
		if id > 0 && strings.Contains(err.Error(), strconv.FormatInt(id, 10)) {
			t.Errorf("error %q names an identifier", err)
		}
	}
	if strings.Contains(err.Error(), subName) {
		t.Errorf("error %q names an object", err)
	}
}

// destination is the second object of a tool with two, otherwise unused.
func destinationFor(tc objectCase, inside int64) int64 {
	if tc.proofs == 2 {
		return inside
	}
	return 0
}

func TestSubtreeAllowsObjectsInsideWithExactlyOneReadEach(t *testing.T) {
	for _, tc := range objectCases() {
		for _, object := range []int64{subInside, subTarget} {
			if object == subTarget && (tc.tool == filesMove.ID || tc.tool == filesTrash.ID) {
				continue // the target itself is protected, see TestSubtreeProtectsTheTargetItself
			}
			t.Run(fmt.Sprintf("%s %d", tc.name, object), func(t *testing.T) {
				dir := t.TempDir()
				var calls []call
				env := subtreeEnvironment(t, dir, &calls, subtreeFake{}.handler(t), subTargets(subTarget)...)
				// A destination inside is the target for a source below it, and a child for the target itself.
				destination := destinationFor(tc, subTarget)
				if object == subTarget {
					destination = destinationFor(tc, subInside)
				}
				_, err := env.invokeConfirmed(tc.tool, "sub", tc.args(dir, object, destination))
				noIdentifiers(t, err, subInside, subTarget)
				method, path := tc.effect(object, destination)
				if !sent(calls, method, path) {
					t.Fatalf("err = %v; the effect %s %s was not sent: %+v", err, method, path, calls)
				}
				if got := proofReads(calls); got != tc.proofs {
					t.Fatalf("proof reads = %d, want %d: %+v", got, tc.proofs, calls)
				}
				if tc.proofIsEffect && len(calls) != 2 {
					t.Fatalf("calls = %+v, want the ownership check and the one read", calls)
				}
			})
		}
	}
}

func TestSubtreeRefusesObjectsOutsideWithoutAnEffect(t *testing.T) {
	for _, tc := range objectCases() {
		cases := []struct {
			name        string
			object, dst int64
			mode        string
		}{
			{"outside", subOutside, destinationFor(tc, subInside), ""},
			{"outside below", subOther, destinationFor(tc, subInside), ""},
			{"no parents", subInside, destinationFor(tc, subTarget), "noparents"},
			{"server error", subInside, destinationFor(tc, subTarget), "5xx"},
			{"not found", subInside, destinationFor(tc, subTarget), "404"},
		}
		if tc.proofs == 2 {
			cases = append(cases, struct {
				name        string
				object, dst int64
				mode        string
			}{"destination outside", subInside, subOutside, ""})
		}
		for _, c := range cases {
			t.Run(tc.name+" "+c.name, func(t *testing.T) {
				dir := t.TempDir()
				var calls []call
				env := subtreeEnvironment(t, dir, &calls, subtreeFake{mode: c.mode}.handler(t), subTargets(subTarget)...)
				_, err := env.invokeConfirmed(tc.tool, "sub", tc.args(dir, c.object, c.dst))
				if err == nil {
					t.Fatalf("an object that is not proven was accepted")
				}
				noIdentifiers(t, err, c.object, c.dst, subInside, subTarget, subOutside, subOther)
				method, path := tc.effect(c.object, c.dst)
				if sent(calls, method, path) && !tc.proofIsEffect {
					t.Fatalf("the effect %s %s was sent: %+v", method, path, calls)
				}
				if got := proofReads(calls); got > tc.proofs {
					t.Fatalf("proof reads = %d, want at most %d", got, tc.proofs)
				}
			})
		}
	}
}

// A drive bound as a whole keeps the reads exactly as before: no proof read, no parents asked for.
func TestWholeDriveBindingReadsNothingExtra(t *testing.T) {
	for _, targets := range [][]string{
		{"account/1001", "drive/5001"},
		{"account/1001", "drive/5001", "drive/5001/files/7042"},
		{"account/1001"},
	} {
		for _, tc := range objectCases() {
			t.Run(tc.name+" "+strings.Join(targets[1:], ","), func(t *testing.T) {
				dir := t.TempDir()
				var calls []call
				env := subtreeEnvironment(t, dir, &calls, func(r *http.Request) (*http.Response, error) {
					if fileReadPath.MatchString(r.URL.Path) && r.URL.Query().Get("with") != "" {
						t.Errorf("a whole drive read %s with %v", r.URL.Path, r.URL.Query())
					}
					return subtreeFake{mode: "plain"}.handler(t)(r)
				}, targets...)
				_, _ = env.invokeConfirmed(tc.tool, "sub", tc.args(dir, subOutside, destinationFor(tc, subOther)))
				if got := proofReads(calls); got != 0 {
					t.Fatalf("proof reads = %d, want none", got)
				}
			})
		}
	}
}

func TestSubtreeProtectsTheTargetItself(t *testing.T) {
	for _, tc := range []string{filesMove.ID, filesTrash.ID} {
		var calls []call
		env := subtreeEnvironment(t, t.TempDir(), &calls, subtreeFake{}.handler(t), subTargets(subTarget)...)
		args := driveArgs(fmt.Sprintf(`,"file_id":%d,"destination_id":%d`, subTarget, subInside))
		if tc == filesTrash.ID {
			args = driveArgs(fmt.Sprintf(`,"file_id":%d`, subTarget))
		}
		_, err := env.invokeConfirmed(tc, "sub", args)
		if !isInvalidRequest(err) || len(calls) != 0 {
			t.Fatalf("%s of the target: err = %v, calls = %+v, want a refusal before any request", tc, err, calls)
		}
		noIdentifiers(t, err, subTarget, subInside)
	}
	// The target may be renamed and used as a destination.
	var calls []call
	env := subtreeEnvironment(t, t.TempDir(), &calls, subtreeFake{}.handler(t), subTargets(subTarget)...)
	if _, err := env.invokeConfirmed(filesRename.ID, "sub", driveArgs(fmt.Sprintf(`,"file_id":%d,"name":"New"`, subTarget))); err != nil {
		t.Fatalf("rename of the target = %v", err)
	}
	if _, err := env.invokeConfirmed(filesMove.ID, "sub",
		driveArgs(fmt.Sprintf(`,"file_id":%d,"destination_id":%d`, subInside, subTarget))); err != nil {
		t.Fatalf("move into the target = %v", err)
	}
}

func TestListDefaultsToTheOnlyTargetAndNeedsFolderIDForSeveral(t *testing.T) {
	var calls []call
	env := subtreeEnvironment(t, t.TempDir(), &calls, subtreeFake{}.handler(t), subTargets(subTarget)...)
	if _, err := env.invoke(filesList.ID, "sub", driveArgs("")); err != nil {
		t.Fatalf("list = %v", err)
	}
	if !sent(calls, "GET", fmt.Sprintf("/3/drive/%d/files/%d/files", ownDrive, subTarget)) || len(calls) != 2 {
		t.Fatalf("calls = %+v, want the ownership check and the listing of the target", calls)
	}

	calls = nil
	env = subtreeEnvironment(t, t.TempDir(), &calls, subtreeFake{}.handler(t), subTargets(subTarget, subOther)...)
	_, err := env.invoke(filesList.ID, "sub", driveArgs(""))
	if !isInvalidRequest(err) || !strings.Contains(err.Error(), "folder_id") || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want an invalid request naming folder_id before any request", err, calls)
	}

	// With the drive bound as a whole the root stays the default.
	calls = nil
	env = subtreeEnvironment(t, t.TempDir(), &calls, subtreeFake{}.handler(t),
		append(subTargets(subTarget, subOther), "drive/5001")...)
	if _, err := env.invoke(filesList.ID, "sub", driveArgs("")); err != nil {
		t.Fatalf("list = %v", err)
	}
	if !sent(calls, "GET", fmt.Sprintf("/3/drive/%d/files/%d/files", ownDrive, rootID)) || len(calls) != 2 {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestWholeDriveToolsAreNotReachedBySubtreeBinding(t *testing.T) {
	wholeTools := []struct{ tool, args string }{
		{trashListDescriptor.ID, driveArgs("")},
		{trashRestore.ID, driveArgs(fmt.Sprintf(`,"file_id":%d,"destination_id":%d`, subInside, subTarget))},
		{trashDelete.ID, driveArgs(fmt.Sprintf(`,"file_id":%d`, subInside))},
		{trashEmpty.ID, driveArgs("")},
		{linksList.ID, driveArgs("")},
	}
	for _, w := range wholeTools {
		t.Run(w.tool, func(t *testing.T) {
			// Not offered under a files target alone.
			var calls []call
			env := subtreeEnvironment(t, t.TempDir(), &calls, subtreeFake{}.handler(t), subTargets(subTarget)...)
			if _, err := env.invokeConfirmed(w.tool, "sub", w.args); err == nil || len(calls) != 0 {
				t.Fatalf("err = %v, calls = %+v, want a refusal before any request", err, calls)
			}
			// Offered but refused for a drive that is bound only as a subtree.
			calls = nil
			env = subtreeEnvironment(t, t.TempDir(), &calls, subtreeFake{}.handler(t),
				"account/1001", "drive/5002", "drive/5001/files/7042")
			_, err := env.invokeConfirmed(w.tool, "sub", w.args)
			if !isInvalidRequest(err) || len(calls) != 0 {
				t.Fatalf("err = %v, calls = %+v, want an invalid request before any request", err, calls)
			}
			// Reached with the whole drive.
			calls = nil
			env = subtreeEnvironment(t, t.TempDir(), &calls, subtreeFake{}.handler(t),
				"account/1001", "drive/5001", "drive/5001/files/7042")
			_, err = env.invokeConfirmed(w.tool, "sub", w.args)
			if isInvalidRequest(err) || !sent(calls, "GET", ownershipPath(ownDrive)) {
				t.Fatalf("err = %v, calls = %+v, want the tool to run", err, calls)
			}
		})
	}
}

func TestReachesPerBinding(t *testing.T) {
	metadata, _ := registry(t).ProviderMetadata(Provider)
	cases := []struct {
		targets []string
		reached bool
	}{
		{[]string{"account/1"}, true},
		{[]string{"account/1", "drive/2"}, true},
		{[]string{"account/1", "drive/2/files/3"}, false},
		{[]string{"account/1", "drive/2/files/3", "drive/4"}, true},
	}
	for _, tool := range wholeDriveTools {
		for _, c := range cases {
			reached, hint := metadata.Target.Reach(tool, c.targets)
			if reached != c.reached || (!reached && hint != "needs drive/DRIVE_ID") {
				t.Errorf("%s with %v: reached = %t, hint = %q", tool, c.targets, reached, hint)
			}
		}
	}
	if reached, _ := metadata.Target.Reach(filesList.ID, []string{"account/1", "drive/2/files/3"}); !reached {
		t.Error("files.list must be reached by a files target")
	}
}

func TestFilesTargetOfAForeignDriveFailsTheAccountProof(t *testing.T) {
	var calls []call
	env := subtreeEnvironment(t, t.TempDir(), &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == ownershipPath(foreignDrive) {
			return jsonResponse(200, envelopeSuccess(driveJSONOf(foreignDrive, otherAccount, "x"))), nil
		}
		t.Errorf("unexpected request %s", r.URL.Path)
		return jsonResponse(500, ""), nil
	}, "account/1001", fmt.Sprintf("drive/%d/files/%d", foreignDrive, subTarget))
	_, err := env.invoke(filesList.ID, "sub", fmt.Sprintf(`{"drive_id":%d}`, foreignDrive))
	if !isInvalidRequest(err) || len(calls) != 1 {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
}

func TestExistingBindingKeepsItsReads(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, envelopeSuccess(fileJSONOf(childFileID, rootID, "a", "file"))), nil
	}), nil)
	if _, err := env.invoke(filesStat.ID, "drive", fmt.Sprintf(`{"drive_id":%d,"file_id":%d}`, ownDrive, childFileID)); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || len(calls[1].query) != 0 {
		t.Fatalf("calls = %+v, want the ownership check and one plain read", calls)
	}
}

func TestFilesTargetValidation(t *testing.T) {
	cases := []struct {
		target  string
		wantErr bool
	}{
		{"drive/5/files/3", false},
		{"drive/5/files", true},
		{"drive/5/files/", true},
		{"drive//files/3", true},
		{"drive/5/files/abc", true},
		{"drive/abc/files/3", true},
		{"drive/5/files/*", true},
		{"drive/*/files/3", true},
		{"drive/5/files/03", true},
		{"drive/05/files/3", true},
		{"drive/5/files/3/4", true},
		{"drive/5/folders/3", true},
		{"drive/5/files/-3", true},
		{"drive/5/files/0", true},
	}
	for _, c := range cases {
		err := validateTarget(c.target)
		if (err != nil) != c.wantErr {
			t.Errorf("validateTarget(%q) = %v, wantErr = %t", c.target, err, c.wantErr)
		}
		if err != nil && strings.Contains(err.Error(), c.target) {
			t.Errorf("the error quotes the target %q", c.target)
		}
	}
	if _, err := parseAllowlist([]string{"account/1", "drive/5/files/3", "drive/5/files/3"}); err == nil {
		t.Error("a duplicate files target was accepted")
	}
	bound, err := parseAllowlist([]string{"account/1", "drive/5/files/3", "drive/5/files/4", "drive/6/files/3"})
	if err != nil || len(bound.files) != 3 || !bound.allowsDrive(5) || bound.wholeDrive(5) || bound.allowsDrive(7) {
		t.Fatalf("bound = %+v, err = %v", bound, err)
	}
}
