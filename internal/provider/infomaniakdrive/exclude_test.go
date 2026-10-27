package infomaniakdrive

import (
	"encoding/json"
	"fmt"
	"net/http"
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
			if got := proofReads(calls); got != tc.proofs {
				t.Fatalf("proof reads = %d, want %d: %+v", got, tc.proofs, calls)
			}
			if tc.proofIsEffect && len(calls) != 2 {
				t.Fatalf("calls = %+v, want the ownership check and the one read", calls)
			}
		})
	}
}

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
