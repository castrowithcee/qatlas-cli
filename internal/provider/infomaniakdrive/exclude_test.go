package infomaniakdrive

import (
	"encoding/json"
	"fmt"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// Excluded folders of the fake drive, a child below one, and a files target below an excluded folder.
const (
	exFolder int64 = 7070
	exChild  int64 = 7071
	exAbove  int64 = 7080
	exTarget int64 = 7081
)

func init() {
	subParents[exFolder] = []int64{rootID}
	subParents[exChild] = []int64{rootID, exFolder}
	subParents[exAbove] = []int64{rootID}
	subParents[exTarget] = []int64{rootID, exAbove}
}

func driveTarget(driveID int64) string { return fmt.Sprintf("drive/%d", driveID) }

func filesRef(fileID int64) string { return fmt.Sprintf("drive/%d/files/%d", ownDrive, fileID) }

// excludeEnvironment is subtreeEnvironment with exclusions on the connection "sub".
func excludeEnvironment(t *testing.T, dir string, calls *[]call, handler func(*http.Request) (*http.Response, error),
	targets, excludes []string) *environment {
	t.Helper()
	env := subtreeEnvironment(t, dir, calls, handler, targets...)
	cfg := coreConfig()
	cfg.Connections["sub"] = config.Connection{Service: "im", Credential: "im-reader", Tools: connectionTools(t),
		Files: config.Files{Read: []string{dir}, Write: []string{dir}}, Targets: targets, Exclude: excludes}
	reg := registry(t)
	reads := 0
	red := &redact.Redactor{}
	env.core, env.red, env.reads = application.New(reg, cfg, resolver(red, &reads), red), red, &reads
	return env
}

func connectionTools(t *testing.T) []string {
	t.Helper()
	metadata, _ := registry(t).ProviderMetadata(Provider)
	var tools []string
	for _, tool := range metadata.Tools {
		tools = append(tools, tool.ID)
	}
	return tools
}

func wholeDriveWith(excludes ...int64) (targets, list []string) {
	targets = []string{"account/1001", driveTarget(ownDrive)}
	for _, id := range excludes {
		list = append(list, filesRef(id))
	}
	return targets, list
}

func TestExcludeProvesEveryObjectOfAWholeDriveWithOneRead(t *testing.T) {
	for _, tc := range objectCases() {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			var calls []call
			targets, excludes := wholeDriveWith(exFolder)
			env := excludeEnvironment(t, dir, &calls, subtreeFake{}.handler(t), targets, excludes)
			_, err := env.invokeConfirmed(tc.tool, "sub", tc.args(dir, subInside, destinationFor(tc, subOther)))
			method, path := tc.effect(subInside, destinationFor(tc, subOther))
			if !sent(calls, method, path) {
				t.Fatalf("err = %v; the effect %s %s was not sent: %+v", err, method, path, calls)
			}
			want := tc.proofs
			if needsAncestors[tc.name] {
				want++
			}
			if got := proofReads(calls); got != want {
				t.Fatalf("proof reads = %d, want %d: %+v", got, want, calls)
			}
			if tc.proofIsEffect && len(calls) != 2 {
				t.Fatalf("calls = %+v, want the ownership check and the one read", calls)
			}
		})
	}
}

// needsAncestors names the tools that also read each excluded object once, to know the folders above it.
var needsAncestors = map[string]bool{"copy": true, "trash": true, "get": true, "download": true, "access grant": true,
	"access update": true, "links get": true, "dropbox get": true}

func TestExcludeRefusesExcludedObjectsWithoutAnEffect(t *testing.T) {
	for _, tc := range objectCases() {
		cases := []struct {
			name        string
			object, dst int64
			mode        string
		}{
			{"excluded", exFolder, destinationFor(tc, subInside), ""},
			{"below excluded", exChild, destinationFor(tc, subInside), ""},
			{"no parents", subInside, destinationFor(tc, subOther), "noparents"},
			{"server error", subInside, destinationFor(tc, subOther), "5xx"},
		}
		if tc.proofs == 2 {
			cases = append(cases, struct {
				name        string
				object, dst int64
				mode        string
			}{"destination excluded", subInside, exFolder, ""}, struct {
				name        string
				object, dst int64
				mode        string
			}{"destination below excluded", subInside, exChild, ""})
		}
		for _, c := range cases {
			t.Run(tc.name+" "+c.name, func(t *testing.T) {
				dir := t.TempDir()
				var calls []call
				targets, excludes := wholeDriveWith(exFolder)
				env := excludeEnvironment(t, dir, &calls, subtreeFake{mode: c.mode}.handler(t), targets, excludes)
				_, err := env.invokeConfirmed(tc.tool, "sub", tc.args(dir, c.object, c.dst))
				if err == nil {
					t.Fatalf("an excluded or unproven object was accepted")
				}
				noIdentifiers(t, err, c.object, c.dst, exFolder, exChild)
				if strings.Contains(err.Error(), "exclu") {
					t.Errorf("error %q names an exclusion", err)
				}
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

func TestExcludeRefusesWithTheMessageOfAnObjectOutsideTheTargets(t *testing.T) {
	var calls []call
	targets, excludes := wholeDriveWith(exFolder)
	env := excludeEnvironment(t, t.TempDir(), &calls, subtreeFake{}.handler(t), targets, excludes)
	_, err := env.invoke(filesStat.ID, "sub", driveArgs(fmt.Sprintf(`,"file_id":%d`, exFolder)))
	if !isInvalidRequest(err) || !strings.Contains(err.Error(), outsideSubtree) {
		t.Fatalf("err = %v, want the refusal of an object outside the targets", err)
	}
}

// An exclusion applies to a files target below it as well, and to the target's default listing.
func TestExcludeBeatsATargetBelowIt(t *testing.T) {
	var calls []call
	env := excludeEnvironment(t, t.TempDir(), &calls, subtreeFake{}.handler(t),
		subTargets(exTarget), []string{filesRef(exAbove)})
	_, err := env.invoke(filesList.ID, "sub", driveArgs(""))
	if !isInvalidRequest(err) || sent(calls, "GET", fmt.Sprintf("/3/drive/%d/files/%d/files", ownDrive, exTarget)) {
		t.Fatalf("err = %v, calls = %+v, want a refusal without the listing", err, calls)
	}
	if proofReads(calls) != 1 {
		t.Fatalf("calls = %+v, want one proof read", calls)
	}
}

func TestExcludeListDropsExcludedChildrenAndKeepsThePaging(t *testing.T) {
	var calls []call
	targets, excludes := wholeDriveWith(exFolder)
	listing := func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == fmt.Sprintf("/3/drive/%d/files/%d/files", ownDrive, rootID) {
			return jsonResponse(200, `{"result":"success","data":[`+
				fileJSONOf(subInside, rootID, "a", "file")+`,`+fileJSONOf(exFolder, rootID, "b", "dir")+`,`+
				fileJSONOf(subOther, rootID, "c", "file")+`],"cursor":"next","has_more":true}`), nil
		}
		return subtreeFake{}.handler(t)(r)
	}
	env := excludeEnvironment(t, t.TempDir(), &calls, listing, targets, excludes)
	result, err := env.invoke(filesList.ID, "sub", driveArgs(""))
	if err != nil {
		t.Fatal(err)
	}
	var page FolderPage
	if err := json.Unmarshal([]byte(result), &page); err != nil {
		t.Fatal(err)
	}
	if page.Count != 2 || len(page.Entries) != 2 || !page.HasMore || page.Cursor == "" {
		t.Fatalf("page = %+v, want two entries and the paging kept", page)
	}
	for _, entry := range page.Entries {
		if entry.ID == exFolder {
			t.Fatalf("the excluded entry was listed: %+v", page.Entries)
		}
	}
	if len(calls) != 2 || proofReads(calls) != 0 {
		t.Fatalf("calls = %+v, want the ownership check and the listing only", calls)
	}
}

func TestExcludeListOfAnExcludedFolderIsRefused(t *testing.T) {
	for _, folder := range []int64{exFolder, exChild} {
		var calls []call
		targets, excludes := wholeDriveWith(exFolder)
		env := excludeEnvironment(t, t.TempDir(), &calls, subtreeFake{}.handler(t), targets, excludes)
		_, err := env.invoke(filesList.ID, "sub", driveArgs(fmt.Sprintf(`,"folder_id":%d`, folder)))
		if !isInvalidRequest(err) || sent(calls, "GET", fmt.Sprintf("/3/drive/%d/files/%d/files", ownDrive, folder)) {
			t.Fatalf("err = %v, calls = %+v, want a refusal without the listing", err, calls)
		}
		noIdentifiers(t, err, folder, exFolder)
	}
}

func TestExcludeDrivesListHidesAnExcludedDrive(t *testing.T) {
	var calls []call
	env := excludeEnvironment(t, t.TempDir(), &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"result":"success","data":[`+driveJSONOf(ownDrive, ownAccount, "Own")+`,`+
			driveJSONOf(otherOwnDrive, ownAccount, "Other")+`],"page":1,"pages":1,"total":2}`), nil
	}, []string{"account/1001"}, []string{driveTarget(otherOwnDrive)})
	result, err := env.invoke(drivesList.ID, "sub", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var page DrivesPage
	if err := json.Unmarshal([]byte(result), &page); err != nil {
		t.Fatal(err)
	}
	if page.Count != 1 || len(page.Drives) != 1 || page.Drives[0].ID != ownDrive {
		t.Fatalf("drives = %+v, want only the drive that is not excluded", page.Drives)
	}
}

func TestExcludeDriveIsRefusedBeforeTheSecret(t *testing.T) {
	var calls []call
	env := excludeEnvironment(t, t.TempDir(), &calls, subtreeFake{}.handler(t),
		[]string{"account/1001"}, []string{driveTarget(ownDrive)})
	for _, tool := range []string{filesStat.ID, filesList.ID} {
		_, err := env.invoke(tool, "sub", driveArgs(""))
		if !isInvalidRequest(err) || len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("%s: err = %v, calls = %+v, reads = %d, want a refusal before secret and request",
				tool, err, calls, *env.reads)
		}
	}
	_, err := env.invokeConfirmed(filesMove.ID, "sub", driveArgs(`,"file_id":43,"destination_id":42`))
	if !isInvalidRequest(err) || len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("move: err = %v, calls = %+v", err, calls)
	}
}

func TestExcludeOfAnotherDriveLeavesTheReadsUnchanged(t *testing.T) {
	var calls []call
	env := excludeEnvironment(t, t.TempDir(), &calls, withOwnership(ownDrive, ownAccount,
		func(r *http.Request) (*http.Response, error) {
			return jsonResponse(200, envelopeSuccess(fileJSONOf(childFileID, rootID, "a", "file"))), nil
		}), []string{"account/1001", driveTarget(ownDrive)}, []string{fmt.Sprintf("drive/%d/files/%d", otherOwnDrive, exFolder)})
	if _, err := env.invoke(filesStat.ID, "sub", driveArgs(fmt.Sprintf(`,"file_id":%d`, childFileID))); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || len(calls[1].query) != 0 {
		t.Fatalf("calls = %+v, want the ownership check and one plain read", calls)
	}
}

func TestExcludeTheRootOfADriveNeedsNoProofRead(t *testing.T) {
	var calls []call
	targets, excludes := wholeDriveWith(exFolder)
	env := excludeEnvironment(t, t.TempDir(), &calls, subtreeFake{}.handler(t), targets, excludes)
	if _, err := env.invoke(filesList.ID, "sub", driveArgs(fmt.Sprintf(`,"folder_id":%d`, rootID))); err != nil {
		t.Fatal(err)
	}
	if proofReads(calls) != 0 {
		t.Fatalf("calls = %+v, want no proof read for the root", calls)
	}
}

func TestParseScopeExclusions(t *testing.T) {
	base := []string{"account/1", "drive/5", "drive/6/files/3"}
	valid := [][]string{
		{"drive/7"}, {"drive/5/files/9"}, {"drive/6/files/4"}, {"drive/7", "drive/7/files/2"},
	}
	for _, excludes := range valid {
		if _, err := parseScope(base, excludes); err != nil {
			t.Errorf("parseScope(%v) = %v", excludes, err)
		}
	}
	invalid := [][]string{
		{"account/1"}, {"drive/5/files/1"}, {"drive/5"}, {"drive/6/files/3"}, {"drive/7", "drive/7"},
		{"drive/7/files/2", "drive/7/files/2"}, {""}, {"drive/*"}, {"drive/5/files/*"}, {"drive/05"}, {"other/1"},
	}
	for _, excludes := range invalid {
		_, err := parseScope(base, excludes)
		if err == nil {
			t.Errorf("parseScope(%v) accepted an invalid exclusion", excludes)
			continue
		}
		for _, value := range excludes {
			if value != "" && strings.Contains(err.Error(), value) {
				t.Errorf("the error %q quotes %q", err, value)
			}
		}
	}
	bound, err := parseScope(base, []string{"drive/7", "drive/5/files/9"})
	if err != nil || bound.wholeDrive(7) || bound.allowsDrive(7) || !bound.allowsDrive(5) ||
		!bound.excludesFile(5, 9) || !bound.hasFileExcludes(5) || bound.hasFileExcludes(6) {
		t.Fatalf("bound = %+v, err = %v", bound, err)
	}
	if len(bound.drives) != 2 && len(bound.files) != 1 {
		t.Fatalf("exclusions changed the targets: %+v", bound)
	}
}

func TestInvalidExclusionFailsTheCallBeforeAnyRequest(t *testing.T) {
	var calls []call
	env := excludeEnvironment(t, t.TempDir(), &calls, subtreeFake{}.handler(t),
		[]string{"account/1001", driveTarget(ownDrive)}, []string{"account/1001"})
	_, err := env.invoke(filesStat.ID, "sub", driveArgs(fmt.Sprintf(`,"file_id":%d`, subInside)))
	if err == nil || len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %+v, want a failure before secret and request", err, calls)
	}
}

// ancestorEnvironment excludes exChild, so exFolder (and the drive's root) lie above an excluded object.
func ancestorEnvironment(t *testing.T, dir string, calls *[]call,
	handler func(*http.Request) (*http.Response, error)) *environment {
	t.Helper()
	targets, excludes := wholeDriveWith(exChild)
	return excludeEnvironment(t, dir, calls, handler, targets, excludes)
}

// exclusionRead answers the read of the excluded object itself with a status or a body of its own.
func exclusionRead(status int, body string) func(*http.Request) (*http.Response, error) {
	inner := subtreeFake{}
	return func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet && r.URL.Path == fmt.Sprintf("/3/drive/%d/files/%d", ownDrive, exChild) {
			return jsonResponse(status, body), nil
		}
		return inner.handler(nil)(r)
	}
}

func caseNamed(t *testing.T, name string) objectCase {
	t.Helper()
	for _, tc := range objectCases() {
		if tc.name == name {
			return tc
		}
	}
	t.Fatalf("no object case %q", name)
	return objectCase{}
}

func TestExcludeAncestorsAreRefusedByToolsThatReachEverythingBelow(t *testing.T) {
	for _, name := range []string{"copy", "trash", "get", "download", "access grant", "access update", "links get", "dropbox get"} {
		t.Run(name, func(t *testing.T) {
			tc := caseNamed(t, name)
			dir := t.TempDir()
			var calls []call
			env := ancestorEnvironment(t, dir, &calls, subtreeFake{}.handler(t))
			dst := destinationFor(tc, subInside)
			_, err := env.invokeConfirmed(tc.tool, "sub", tc.args(dir, exFolder, dst))
			if !isInvalidRequest(err) || !strings.Contains(err.Error(), outsideSubtree) {
				t.Fatalf("err = %v, want the refusal of an object outside the targets", err)
			}
			noIdentifiers(t, err, exFolder, exChild)
			if strings.Contains(err.Error(), "exclu") {
				t.Errorf("error %q names an exclusion", err)
			}
			method, path := tc.effect(exFolder, dst)
			if sent(calls, method, path) {
				t.Fatalf("the effect %s %s was sent: %+v", method, path, calls)
			}
			for _, c := range calls {
				if c.method != http.MethodGet {
					t.Fatalf("a change was requested: %+v", calls)
				}
			}
			// The object itself, then the one excluded object whose parents are the ancestors.
			if got := proofReads(calls); got != tc.proofs+1 {
				t.Fatalf("proof reads = %d, want %d: %+v", got, tc.proofs+1, calls)
			}
		})
	}
}

func TestExcludeAncestorsStayOpenToToolsThatDoNotReachBelow(t *testing.T) {
	for _, name := range []string{"move", "rename", "access revoke", "access get", "links delete", "dropbox delete",
		"upload new", "folders create", "list", "stat"} {
		t.Run(name, func(t *testing.T) {
			tc := caseNamed(t, name)
			dir := t.TempDir()
			var calls []call
			env := ancestorEnvironment(t, dir, &calls, subtreeFake{}.handler(t))
			dst := destinationFor(tc, subInside)
			_, err := env.invokeConfirmed(tc.tool, "sub", tc.args(dir, exFolder, dst))
			method, path := tc.effect(exFolder, dst)
			if !sent(calls, method, path) {
				t.Fatalf("err = %v; the effect %s %s was not sent: %+v", err, method, path, calls)
			}
			if got := proofReads(calls); got != tc.proofs {
				t.Fatalf("proof reads = %d, want %d (no read of the excluded object): %+v", got, tc.proofs, calls)
			}
		})
	}
}

func TestExcludeAncestorOfAnExclusionThatIsNotFoundTouchesNothing(t *testing.T) {
	tc := caseNamed(t, "copy")
	var calls []call
	dir := t.TempDir()
	env := ancestorEnvironment(t, dir, &calls, exclusionRead(404, `{"result":"error"}`))
	_, err := env.invokeConfirmed(tc.tool, "sub", tc.args(dir, exFolder, subInside))
	method, path := tc.effect(exFolder, subInside)
	if !sent(calls, method, path) {
		t.Fatalf("err = %v; a missing excluded object must not refuse the copy: %+v", err, calls)
	}
}

func TestExcludeAncestorsFailClosedWhenTheExclusionCannotBeRead(t *testing.T) {
	for name, handler := range map[string]func(*http.Request) (*http.Response, error){
		"forbidden":   exclusionRead(403, `{"result":"error"}`),
		"garbage":     exclusionRead(200, `{"result":"success","data":"x"}`),
		"no parents":  exclusionRead(200, envelopeSuccess(fileJSONOf(exChild, exFolder, "x", "dir"))),
		"other file":  exclusionRead(200, envelopeSuccess(fileJSONOf(exAbove, rootID, "x", "dir"))),
		"empty reply": exclusionRead(200, ``),
	} {
		t.Run(name, func(t *testing.T) {
			tc := caseNamed(t, "copy")
			var calls []call
			dir := t.TempDir()
			env := ancestorEnvironment(t, dir, &calls, handler)
			_, err := env.invokeConfirmed(tc.tool, "sub", tc.args(dir, subInside, subOther))
			if !isInvalidRequest(err) {
				t.Fatalf("err = %v, want a refusal", err)
			}
			method, path := tc.effect(subInside, subOther)
			if sent(calls, method, path) {
				t.Fatalf("the copy was sent: %+v", calls)
			}
			noIdentifiers(t, err, exFolder, exChild)
		})
	}
}

func TestExcludeAncestorsAreReadOncePerCall(t *testing.T) {
	var calls []call
	env := ancestorEnvironment(t, t.TempDir(), &calls, linkBodyFake(t))
	_, err := env.invoke(filesList.ID, "sub", driveArgs(`,"with":["sharelink","dropbox"]`))
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	for _, c := range calls {
		if c.path == fmt.Sprintf("/3/drive/%d/files/%d", ownDrive, exChild) {
			reads++
		}
	}
	if reads != 1 {
		t.Fatalf("reads of the excluded object = %d, want 1: %+v", reads, calls)
	}
}

func TestExcludeTrashToolsAreRefusedBeforeTheSecret(t *testing.T) {
	for _, tool := range []struct{ id, args string }{
		{trashListDescriptor.ID, driveArgs("")},
		{trashRestore.ID, driveArgs(`,"file_id":43,"destination_id":42`)},
		{trashDelete.ID, driveArgs(`,"file_id":43`)},
		{trashEmpty.ID, driveArgs("")},
	} {
		t.Run(tool.id, func(t *testing.T) {
			var calls []call
			env := ancestorEnvironment(t, t.TempDir(), &calls, subtreeFake{}.handler(t))
			_, err := env.invokeConfirmed(tool.id, "sub", tool.args)
			if !isInvalidRequest(err) || len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("err = %v, calls = %+v, reads = %d, want a refusal before secret and request",
					err, calls, *env.reads)
			}
			if strings.Contains(err.Error(), "exclu") {
				t.Errorf("error %q names an exclusion", err)
			}
		})
	}
}

// The tools stay offered with exclusions: the exclusions only refuse single calls.
func TestExcludeKeepsEveryToolOffered(t *testing.T) {
	metadata, _ := registry(t).ProviderMetadata(Provider)
	targets, _ := wholeDriveWith(exFolder)
	for _, tool := range metadata.Tools {
		if reached, hint := metadata.Target.Reach(tool.ID, targets); !reached {
			t.Errorf("%s is not offered: %s", tool.ID, hint)
		}
	}
	for _, tool := range wholeDriveTools {
		if !slices.Contains(connectionTools(t), tool) {
			t.Errorf("%s is not a tool of the provider", tool)
		}
	}
}

func linkRow(id int64, parents ...int64) string {
	var list []string
	for _, parent := range parents {
		list = append(list, fmt.Sprintf(`{"id":%d}`, parent))
	}
	row := fileJSONOf(id, rootID, "n", "dir")
	row = row[:len(row)-1] + fmt.Sprintf(`,"sharelink":{"file_id":%d,"right":"public","url":"https://k.example/s"}`, id)
	if parents != nil {
		row += `,"parents":[` + strings.Join(list, ",") + `]`
	}
	return row + "}"
}

func TestExcludeLinksListDropsRowsOfExcludedObjectsAndAncestors(t *testing.T) {
	const below int64 = 7090
	var calls []call
	rows := strings.Join([]string{
		linkRow(subInside, rootID), linkRow(exFolder, rootID), linkRow(exChild, rootID, exFolder),
		linkRow(below, rootID, exFolder, exChild), linkRow(subOther), linkRow(rootID, rootID),
	}, ",")
	env := ancestorEnvironment(t, t.TempDir(), &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == fmt.Sprintf("/3/drive/%d/files/links", ownDrive) {
			return jsonResponse(200, `{"result":"success","data":[`+rows+`],"cursor":"next","has_more":true}`), nil
		}
		return subtreeFake{}.handler(t)(r)
	}))
	result, err := env.invoke(linksList.ID, "sub", driveArgs(""))
	if err != nil {
		t.Fatal(err)
	}
	var page LinkPage
	if err := json.Unmarshal([]byte(result), &page); err != nil {
		t.Fatal(err)
	}
	// Only the row with proven parents that are neither excluded nor above an excluded object stays: the
	// drive's root counts as an ancestor, and the row without parents is unproven.
	if page.Count != 1 || len(page.Entries) != 1 || page.Entries[0].ID != subInside || !page.HasMore || page.Cursor == "" {
		t.Fatalf("page = %+v, want the one provable row and the paging kept", page)
	}
	if len(page.Entries[0].Parents) != 0 {
		t.Errorf("parents were added to the entry: %+v", page.Entries[0])
	}
	for _, c := range calls {
		if strings.HasSuffix(c.path, "/files/links") && c.query.Get("with") != "sharelink,parents" {
			t.Errorf("with = %q, want sharelink,parents", c.query.Get("with"))
		}
	}
}

func TestExcludeLinksListKeepsAskedParentsAndWithoutExclusionTheRequest(t *testing.T) {
	list := func(excluded bool, with string) string {
		var calls []call
		handler := withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == fmt.Sprintf("/3/drive/%d/files/links", ownDrive) {
				return jsonResponse(200, `{"result":"success","data":[`+linkRow(subInside, rootID)+`],"has_more":false}`), nil
			}
			return subtreeFake{}.handler(t)(r)
		})
		targets, excludes := wholeDriveWith()
		if excluded {
			targets, excludes = wholeDriveWith(exChild)
		}
		env := excludeEnvironment(t, t.TempDir(), &calls, handler, targets, excludes)
		result, err := env.invoke(linksList.ID, "sub", driveArgs(with))
		if err != nil {
			t.Fatal(err)
		}
		if excluded && with != "" && !strings.Contains(result, `"parents"`) {
			t.Errorf("asked parents missing: %s", result)
		}
		for _, c := range calls {
			if strings.HasSuffix(c.path, "/files/links") {
				return c.query.Get("with")
			}
		}
		return "no request"
	}
	if got := list(false, ""); got != "sharelink" {
		t.Errorf("without exclusion with = %q, want sharelink", got)
	}
	if got := list(true, `,"with":["parents"]`); got != "sharelink,parents" {
		t.Errorf("with = %q, want parents once", got)
	}
}

// linkBody is a file resource with the members that carry a share link and a dropbox.
func linkBody(id int64, parents ...int64) string {
	list := []string{}
	for _, parent := range parents {
		list = append(list, fmt.Sprintf(`{"id":%d}`, parent))
	}
	return fmt.Sprintf(`{"id":%d,"name":"n","type":"dir","parent_id":1,"status":"ok","parents":[%s],`+
		`"sharelink":{"file_id":%d,"right":"public"},"dropbox":{"id":%d,"name":"d"},`+
		`"rewind":{"id":9,"status":"ok","directory_name":"canary-dir","rewind_name":"canary-rewind"}}`,
		id, strings.Join(list, ","), id, id)
}

func linkBodyFake(t *testing.T) func(*http.Request) (*http.Response, error) {
	return withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
		if match := fileReadPath.FindStringSubmatch(r.URL.Path); match != nil {
			switch match[1] {
			case "7070":
				return jsonResponse(200, envelopeSuccess(linkBody(exFolder, rootID))), nil
			case "7071":
				return jsonResponse(200, envelopeSuccess(linkBody(exChild, rootID, exFolder))), nil
			}
			var id int64
			fmt.Sscan(match[1], &id)
			return jsonResponse(200, envelopeSuccess(linkBody(id, rootID))), nil
		}
		if strings.HasSuffix(r.URL.Path, fmt.Sprintf("/files/%d/files", rootID)) {
			return jsonResponse(200, `{"result":"success","data":[`+linkBody(exFolder, rootID)+`,`+
				linkBody(subInside, rootID)+`],"has_more":false}`), nil
		}
		return subtreeFake{}.handler(t)(r)
	})
}

func TestExcludeAncestorEntriesLoseTheirLinkMembers(t *testing.T) {
	var calls []call
	env := ancestorEnvironment(t, t.TempDir(), &calls, linkBodyFake(t))
	withLinks := `,"with":["sharelink","sharelink.views","dropbox","dropbox.capabilities"]`
	read := func(tool, args string) map[string]map[string]any {
		result, err := env.invoke(tool, "sub", args)
		if err != nil {
			t.Fatal(err)
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(result), &raw); err != nil {
			t.Fatal(err)
		}
		entries := map[string]map[string]any{}
		if list, ok := raw["entries"].([]any); ok {
			for _, item := range list {
				entry := item.(map[string]any)
				entries[fmt.Sprint(int64(entry["id"].(float64)))] = entry
			}
			return entries
		}
		entries[fmt.Sprint(int64(raw["id"].(float64)))] = raw
		return entries
	}
	stat := read(filesStat.ID, driveArgs(fmt.Sprintf(`,"file_id":%d`, exFolder)+withLinks))["7070"]
	if stat["sharelink"] != nil || stat["dropbox"] != nil {
		t.Errorf("stat of an ancestor shows link members: %+v", stat)
	}
	stat = read(filesStat.ID, driveArgs(fmt.Sprintf(`,"file_id":%d`, subInside)+withLinks))["7043"]
	if stat["sharelink"] == nil || stat["dropbox"] == nil {
		t.Errorf("stat of another folder lost its link members: %+v", stat)
	}
	listed := read(filesList.ID, driveArgs(withLinks))
	if listed["7070"] == nil || listed["7070"]["sharelink"] != nil || listed["7070"]["dropbox"] != nil ||
		listed["7043"]["sharelink"] == nil || listed["7043"]["dropbox"] == nil {
		t.Errorf("listed entries = %+v, want link members only on the folder that is no ancestor", listed)
	}
}

func TestExcludeRewindMasksNamesLikeASubtreeBinding(t *testing.T) {
	fileRewind := func(targets, excludes []string) map[string]any {
		var calls []call
		env := excludeEnvironment(t, t.TempDir(), &calls, linkBodyFake(t), targets, excludes)
		result, err := env.invoke(filesStat.ID, "sub", driveArgs(fmt.Sprintf(`,"file_id":%d,"with":["rewind"]`, subInside)))
		if err != nil {
			t.Fatal(err)
		}
		var entry struct{ Rewind map[string]any }
		if err := json.Unmarshal([]byte(result), &entry); err != nil {
			t.Fatal(err)
		}
		return entry.Rewind
	}
	targets, excludes := wholeDriveWith(exChild)
	if rewind := fileRewind(targets, excludes); rewind["id"] == nil || rewind["directory_name"] != nil || rewind["rewind_name"] != nil {
		t.Errorf("rewind with exclusions = %+v, want it without names", rewind)
	}
	targets, excludes = wholeDriveWith()
	if rewind := fileRewind(targets, excludes); rewind["directory_name"] != "canary-dir" || rewind["rewind_name"] != "canary-rewind" {
		t.Errorf("rewind without exclusions = %+v, want the names", rewind)
	}
}

func TestExcludeDriveRewindMasksNames(t *testing.T) {
	body := `{"result":"success","data":[` + strings.TrimSuffix(driveJSONOf(ownDrive, ownAccount, "Own"), "}") +
		`,"rewind":{"id":9,"status":"ok","directory_name":"canary-dir","rewind_name":"canary-rewind"}}],` +
		`"page":1,"pages":1,"total":1}`
	for excluded, want := range map[bool]bool{true: false, false: true} {
		var calls []call
		targets, excludes := wholeDriveWith()
		if excluded {
			targets, excludes = wholeDriveWith(exChild)
		}
		env := excludeEnvironment(t, t.TempDir(), &calls, func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, body), nil
		}, targets, excludes)
		result, err := env.invoke(drivesList.ID, "sub", `{"with":["rewind"]}`)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(result, "canary-dir") || strings.Contains(result, "canary-rewind"); got != want {
			t.Errorf("excluded = %t: names shown = %t, want %t: %s", excluded, got, want, result)
		}
	}
}

func TestExcludedSizesAreWarnedAboutInTheToolDescriptions(t *testing.T) {
	for _, descriptor := range []capability.Descriptor{filesList, filesStat, drivesList} {
		if !strings.Contains(descriptor.Description, "excluded content") {
			t.Errorf("%s does not warn that totals count excluded content: %s", descriptor.ID, descriptor.Description)
		}
	}
}

func TestExcludeIsDeclaredForDrivesAndFilesOnly(t *testing.T) {
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, kind := range metadata.Target.Kinds {
		if want := kind.Name != "account"; kind.Excludable != want {
			t.Errorf("kind %s: Excludable = %t, want %t", kind.Name, kind.Excludable, want)
		}
	}
}

func TestExcludeConfigurationIsCheckedByTheCore(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	targets := []string{"account/1001", "drive/5001"}
	for _, value := range []string{"drive/5002", "drive/5001/files/7070", "drive/5002/files/7"} {
		if findings := metadata.Target.CheckExcludes([]string{value}, targets); len(findings) != 0 {
			t.Errorf("%s was refused: %+v", value, findings)
		}
	}
	for _, value := range []string{"account/1001", "drive/5001", "drive/*", "drive/5001/files/*", "account/*", ""} {
		if findings := metadata.Target.CheckExcludes([]string{value}, targets); len(findings) != 1 {
			t.Errorf("%q was accepted", value)
		}
	}
	document := func(targets, excludes string) string {
		return "version: 1\nservices:\n  im:\n    provider: " + Provider + "\ncredentials:\n  key:\n    provider: " + Provider +
			"\n    type: env\n    values:\n      token: " + tokenEnv + "\nconnections:\n  c:\n    service: im\n" +
			"    credential: key\n    targets: " + targets + "\n    exclude: " + excludes + "\n"
	}
	valid := [][2]string{
		{"[account/1001, drive/5001]", "[drive/5002]"},
		{"[account/1001, drive/5001]", "[drive/5001/files/7070]"},
		{"[account/1001]", "[drive/5001, drive/5001/files/7070]"},
	}
	for _, c := range valid {
		if _, err := config.Decode(strings.NewReader(document(c[0], c[1])), reg); err != nil {
			t.Errorf("targets %s exclude %s: %v", c[0], c[1], err)
		}
	}
	invalid := [][2]string{
		{"[account/1001, drive/5001]", "[account/1001]"},
		{"[account/1001, drive/5001]", "[drive/5001]"},
		{"[account/1001, drive/5001]", "[drive/5002, drive/5002]"},
		{"[account/1001, drive/5001]", "[drive/5001/files/*]"},
	}
	for _, c := range invalid {
		_, err := config.Decode(strings.NewReader(document(c[0], c[1])), reg)
		if err == nil {
			t.Errorf("targets %s exclude %s were accepted", c[0], c[1])
			continue
		}
		if strings.Contains(err.Error(), "drive/5002") || strings.Contains(err.Error(), "files/*") {
			t.Errorf("the error quotes a value: %v", err)
		}
	}
}

func TestExcludeLeavesTheReadsOfAConnectionWithoutExclusionUnchanged(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, envelopeSuccess(fileJSONOf(childFileID, rootID, "a", "file"))), nil
	}), nil)
	for _, tool := range []string{filesStat.ID, filesGet.ID, filesTrash.ID, linksGet.ID, dropboxGet.ID} {
		calls = nil
		args := fmt.Sprintf(`{"drive_id":%d,"file_id":%d}`, ownDrive, childFileID)
		_, _ = env.invokeConfirmed(tool, "drive", args)
		for _, c := range calls {
			if c.query.Get("with") == "parents" || strings.Contains(c.query.Get("with"), "parents") {
				t.Errorf("%s: a read asked for parents: %+v", tool, c)
			}
		}
		if len(calls) > 2 {
			t.Errorf("%s: calls = %+v, want the ownership check and at most the one request", tool, calls)
		}
	}
}
