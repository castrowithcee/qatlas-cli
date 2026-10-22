package infomaniakdrive

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// Every field of FileV3/DirectoryV3 that Infomaniak sends without `with`, with placeholder values.
const (
	entryBaseFields = `"visibility":"is_root","drive_id":1111,"depth":3,"created_by":2001,"last_modified_by":2002,` +
		`"deleted_by":2003,"added_at":1735862400,"revised_at":1735948800,"updated_at":1736035200,` +
		`"deleted_at":1736121600,"shared_root_id":9,"extension_type":"pdf","symbolic_link":"example-target",` +
		`"scan_status":"clean"`
	entryFileFull = `{"id":7,"name":"a.pdf","type":"file","mime_type":"application/pdf","size":12,"parent_id":4,` +
		`"status":"ok","created_at":1735689600,"last_modified_at":1735776000,` + entryBaseFields + `}`
	entryFolderFull = `{"id":8,"name":"dir","type":"dir","parent_id":4,"status":"ok","created_at":1735689600,` +
		`"last_modified_at":1735776000,` + entryBaseFields + `,"color":"#0098FF"}`

	wantFileFull = `{"id":7,"name":"a.pdf","type":"file","mime_type":"application/pdf","size":12,"parent_id":4,` +
		`"status":"ok","created_at":"2025-01-01T00:00:00Z","modified_at":"2025-01-02T00:00:00Z",` + wantBaseFields + `}`
	wantFolderFull = `{"id":8,"name":"dir","type":"folder","parent_id":4,"status":"ok",` +
		`"created_at":"2025-01-01T00:00:00Z","modified_at":"2025-01-02T00:00:00Z",` + wantBaseFields + `,"color":"#0098FF"}`
	wantBaseFields = `"deleted_at":"2025-01-06T00:00:00Z","visibility":"is_root","drive_id":1111,"depth":3,` +
		`"created_by":2001,"last_modified_by":2002,"deleted_by":2003,"added_at":"2025-01-03T00:00:00Z",` +
		`"revised_at":"2025-01-04T00:00:00Z","updated_at":"2025-01-05T00:00:00Z","shared_root_id":9,` +
		`"extension_type":"pdf","symbolic_link":"example-target","scan_status":"clean"`

	// legacyEntry carries only the fields the tools reported before the full field set.
	legacyEntry = `{"id":7,"name":"a.pdf","type":"file","parent_id":4,"status":"ok","created_at":1735689600,"last_modified_at":1735776000}`
	// wantLegacyEntry is the former output, with the keys in the order the core writes them.
	wantLegacyEntry = `{"created_at":"2025-01-01T00:00:00Z","id":7,"modified_at":"2025-01-02T00:00:00Z","name":"a.pdf","parent_id":4,"status":"ok","type":"file"}`
)

type entryFieldCase struct {
	descriptor capability.Descriptor
	connection string
	arguments  string
	path       string
	// shape wraps entries into the response data and the expected output.
	wrap     func(entries string) string
	wantWrap func(entries string) string
	// legacy is the former output of the tool for one legacyEntry, byte for byte.
	legacy string
}

func entryFieldCases() []entryFieldCase {
	list := func(entries string) string {
		return `{"result":"success","data":[` + entries + `],"cursor":"","has_more":false,"response_at":1736208000}`
	}
	return []entryFieldCase{
		{descriptor: filesList, connection: "drive", arguments: fmt.Sprintf(`{"drive_id":%d}`, ownDrive),
			path: fmt.Sprintf("/3/drive/%d/files/%d/files", ownDrive, rootID), wrap: list,
			wantWrap: func(e string) string {
				return fmt.Sprintf(`{"drive_id":%d,"folder_id":%d,"entries":[%s],"has_more":false,"count":2,`+
					`"response_at":"2025-01-07T00:00:00Z"}`, ownDrive, rootID, e)
			},
			legacy: `{"count":1,"drive_id":5001,"entries":[` + wantLegacyEntry + `],"folder_id":1,"has_more":false}`},
		{descriptor: trashListDescriptor, connection: "drive", arguments: fmt.Sprintf(`{"drive_id":%d}`, ownDrive),
			path: fmt.Sprintf("/3/drive/%d/trash", ownDrive), wrap: list,
			wantWrap: func(e string) string {
				return fmt.Sprintf(`{"drive_id":%d,"entries":[%s],"has_more":false,"count":2,`+
					`"response_at":"2025-01-07T00:00:00Z"}`, ownDrive, e)
			},
			legacy: `{"count":1,"drive_id":5001,"entries":[` + wantLegacyEntry + `],"has_more":false}`},
		{descriptor: linksList, connection: "links", arguments: fmt.Sprintf(`{"drive_id":%d}`, ownDrive),
			path: fmt.Sprintf("/3/drive/%d/files/links", ownDrive), wrap: list,
			wantWrap: func(e string) string {
				return fmt.Sprintf(`{"drive_id":%d,"entries":[%s],"has_more":false,"count":2,`+
					`"response_at":"2025-01-07T00:00:00Z"}`, ownDrive, e)
			},
			legacy: `{"count":1,"drive_id":5001,"entries":[` + wantLegacyEntry + `],"has_more":false}`},
	}
}

func runEntryField(t *testing.T, c entryFieldCase, body string) (string, []call) {
	t.Helper()
	var calls []call
	env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != c.path {
			t.Fatalf("path = %s, want %s", r.URL.Path, c.path)
		}
		return jsonResponse(200, body), nil
	}), nil)
	result, err := env.invoke(c.descriptor.ID, c.connection, c.arguments)
	if err != nil {
		t.Fatalf("%s: invoke() = %v", c.descriptor.ID, err)
	}
	return result, calls
}

func TestEntryToolsReturnEveryFieldWithoutWith(t *testing.T) {
	for _, c := range entryFieldCases() {
		t.Run(c.descriptor.ID, func(t *testing.T) {
			result, calls := runEntryField(t, c, c.wrap(entryFileFull+","+entryFolderFull))
			if want := c.wantWrap(wantFileFull + "," + wantFolderFull); !jsonEqual(result, want) {
				t.Fatalf("result = %s\nwant   = %s", result, want)
			}
			if len(calls) != 2 {
				t.Fatalf("calls = %d, want the ownership check and one read", len(calls))
			}
		})
	}
}

func TestEntryToolsKeepTheirFormerOutputByteForByte(t *testing.T) {
	for _, c := range entryFieldCases() {
		t.Run(c.descriptor.ID, func(t *testing.T) {
			body := `{"result":"success","data":[` + legacyEntry + `],"cursor":"","has_more":false}`
			result, calls := runEntryField(t, c, body)
			if result != c.legacy {
				t.Fatalf("result = %s\nwant   = %s", result, c.legacy)
			}
			if len(calls) != 2 {
				t.Fatalf("calls = %d, want 2", len(calls))
			}
		})
	}
}

func TestFilesStatReturnsEveryFieldWithoutWith(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"file": {entryFileFull, wantFileFull}, "folder": {entryFolderFull, wantFolderFull},
		"legacy": {legacyEntry, wantLegacyEntry},
	} {
		t.Run(name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
				return jsonResponse(200, envelopeSuccess(tc.body)), nil
			}), nil)
			result, err := env.invoke(filesStat.ID, "drive", fmt.Sprintf(`{"drive_id":%d,"file_id":7}`, ownDrive))
			if err != nil {
				t.Fatal(err)
			}
			if name == "legacy" && result != tc.want || name != "legacy" && !jsonEqual(result, tc.want) {
				t.Fatalf("result = %s\nwant   = %s", result, tc.want)
			}
			if len(calls) != 2 {
				t.Fatalf("calls = %d, want 2", len(calls))
			}
		})
	}
}

// A drive bound only by a files target proves the object with the same read that supplies the metadata.
func TestFilesStatSubtreeKeepsItsSingleReadAndReturnsEveryField(t *testing.T) {
	var calls []call
	env := subtreeEnvironment(t, t.TempDir(), &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, envelopeSuccess(fmt.Sprintf(
			`{"id":%d,"name":"dir","type":"dir","parent_id":4,"status":"ok","last_modified_at":1735776000,`+
				`"drive_id":1111,"depth":3,"color":"#0098FF","parents":[{"id":%d,"name":"t","parent_id":1}]}`,
			subTarget, subTarget))), nil
	}), subTargets(subTarget)...)
	result, err := env.invoke(filesStat.ID, "sub", fmt.Sprintf(`{"drive_id":%d,"file_id":%d}`, ownDrive, subTarget))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil || out["color"] != "#0098FF" || out["depth"] != float64(3) {
		t.Fatalf("result = %s, %v", result, err)
	}
	reads := 0
	for _, c := range calls {
		if c.path == fmt.Sprintf("/3/drive/%d/files/%d", ownDrive, subTarget) {
			reads++
		}
	}
	if reads != 1 {
		t.Fatalf("reads of the object = %d, want 1 (calls %+v)", reads, calls)
	}
}

func TestEveryEntryOutputMemberIsInSchemaAndFields(t *testing.T) {
	check := func(t *testing.T, d capability.Descriptor, top map[string]json.RawMessage, entries []map[string]json.RawMessage) {
		t.Helper()
		var schema struct {
			Properties map[string]struct {
				Items struct {
					Properties map[string]any `json:"properties"`
				} `json:"items"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(d.OutputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		fields := map[string]bool{}
		for _, f := range d.Fields {
			fields[f.Name] = true
		}
		delete(top, "entries")
		for key := range top {
			if _, ok := schema.Properties[key]; !ok || !fields[key] {
				t.Errorf("%s: page member %s missing in schema or Fields", d.ID, key)
			}
		}
		for _, entry := range entries {
			for key := range entry {
				if _, ok := schema.Properties["entries"].Items.Properties[key]; !ok || !fields[key] {
					t.Errorf("%s: entry member %s missing in schema or Fields", d.ID, key)
				}
			}
		}
		for key := range schema.Properties["entries"].Items.Properties {
			if !fields[key] {
				t.Errorf("%s: schema member %s missing in Fields", d.ID, key)
			}
		}
	}
	for _, c := range entryFieldCases() {
		result, _ := runEntryField(t, c, c.wrap(entryFileFull+","+entryFolderFull))
		var top struct {
			Entries []map[string]json.RawMessage `json:"entries"`
		}
		var members map[string]json.RawMessage
		if err := json.Unmarshal([]byte(result), &top); err != nil || json.Unmarshal([]byte(result), &members) != nil {
			t.Fatal(err)
		}
		check(t, c.descriptor, members, top.Entries)
	}

	var calls []call
	env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, envelopeSuccess(entryFolderFull)), nil
	}), nil)
	result, err := env.invoke(filesStat.ID, "drive", fmt.Sprintf(`{"drive_id":%d,"file_id":8}`, ownDrive))
	if err != nil {
		t.Fatal(err)
	}
	var entry map[string]json.RawMessage
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal([]byte(result), &entry); err != nil || json.Unmarshal(filesStat.OutputSchema, &schema) != nil {
		t.Fatal(err)
	}
	fields := map[string]bool{}
	for _, f := range filesStat.Fields {
		fields[f.Name] = true
	}
	for key := range entry {
		if _, ok := schema.Properties[key]; !ok || !fields[key] {
			t.Errorf("files.stat member %s missing in schema or Fields", key)
		}
	}
}
