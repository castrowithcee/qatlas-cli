package infomaniakdrive

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
)

type filterTool struct {
	id, connection string
	orderBy        []string
	right          bool
	path           string
}

func filterTools() []filterTool {
	return []filterTool{
		{filesList.ID, "drive", filesOrderByValues, false, fmt.Sprintf("/3/drive/%d/files/%d/files", ownDrive, rootID)},
		{trashListDescriptor.ID, "drive", trashOrderByValues, false, fmt.Sprintf("/3/drive/%d/trash", ownDrive)},
		{linksList.ID, "links", linksOrderByValues, true, fmt.Sprintf("/3/drive/%d/files/links", ownDrive)},
	}
}

// pageHandler answers any list with an empty page and the cursor "inner".
func pageHandler() func(*http.Request) (*http.Response, error) {
	return withOwnership(ownDrive, ownAccount, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"result":"success","data":[],"cursor":"inner","has_more":true}`), nil
	})
}

func listArgs(extra string) string {
	return fmt.Sprintf(`{"drive_id":%d%s}`, ownDrive, extra)
}

func TestListFiltersReachTheRequest(t *testing.T) {
	for _, tool := range filterTools() {
		t.Run(tool.id, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, pageHandler(), nil)
			by := tool.orderBy[3]
			extra := fmt.Sprintf(`,"type":["dir","vault"],"order_by":[%q,%q],"order":"desc","order_for":{%q:"asc"}`,
				tool.orderBy[0], by, by)
			if tool.right {
				extra += `,"right":["public","password"]`
			}
			if _, err := env.invoke(tool.id, tool.connection, listArgs(extra)); err != nil {
				t.Fatalf("invoke() = %v", err)
			}
			q := calls[len(calls)-1].query
			if calls[len(calls)-1].path != tool.path || !slices.Equal(q["type"], []string{"dir", "vault"}) ||
				!slices.Equal(q["order_by"], []string{tool.orderBy[0], by}) || q.Get("order") != "desc" ||
				q.Get("order_for["+by+"]") != "asc" || (tool.right && !slices.Equal(q["right"], []string{"public", "password"})) ||
				(!tool.right && q.Has("right")) {
				t.Fatalf("query = %v", q)
			}
		})
	}
}

func TestListFiltersAreOptionalAndAddNothingWithoutThem(t *testing.T) {
	for _, tool := range filterTools() {
		var calls []call
		env := newEnvironment(t, &calls, pageHandler(), nil)
		if _, err := env.invoke(tool.id, tool.connection, listArgs("")); err != nil {
			t.Fatalf("%s: %v", tool.id, err)
		}
		q := calls[len(calls)-1].query
		for key := range q {
			if key != "limit" && key != "with" {
				t.Fatalf("%s: unexpected query %q", tool.id, key)
			}
		}
	}
}

func TestListFiltersRefuseInvalidArgumentsBeforeAnySecretRead(t *testing.T) {
	for _, tool := range filterTools() {
		other := tool.orderBy[1]
		cases := map[string]string{
			"unknown type":        `,"type":["folder"]`,
			"repeated type":       `,"type":["dir","dir"]`,
			"unknown order_by":    `,"order_by":["color"]`,
			"repeated order_by":   `,"order_by":["name","name"]`,
			"bad order":           `,"order":"up"`,
			"bad direction":       `,"order_by":["name"],"order_for":{"name":"up"}`,
			"key outside by":      fmt.Sprintf(`,"order_by":["name"],"order_for":{%q:"asc"}`, other),
			"key without by":      `,"order_for":{"name":"asc"}`,
			"unknown key":         `,"order_by":["name"],"order_for":{"color":"asc"}`,
			"empty type":          `,"type":[]`,
			"not a list":          `,"type":"dir"`,
			"unknown right":       `,"right":["private"]`,
			"repeated right":      `,"right":["public","public"]`,
			"foreign order_by":    `,"order_by":["shared_by","deleted_at","created_at"]`,
			"not an order object": `,"order_for":["asc"]`,
		}
		if !tool.right {
			cases["right"] = `,"right":["public"]`
		}
		for name, extra := range cases {
			if tool.right && strings.HasPrefix(name, "foreign") {
				continue
			}
			t.Run(tool.id+" "+name, func(t *testing.T) {
				var calls []call
				env := newEnvironment(t, &calls, pageHandler(), nil)
				if _, err := env.invoke(tool.id, tool.connection, listArgs(extra)); err == nil {
					t.Fatal("invoke() succeeded")
				}
				if len(calls) != 0 || *env.reads != 0 {
					t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
				}
			})
		}
	}
}

// Sort attributes that only one operation documents are refused by the others.
func TestListOrderByValuesFollowTheOperation(t *testing.T) {
	cases := []struct {
		tool  filterTool
		value string
		ok    bool
	}{
		{filterTools()[0], "deleted_at", false}, {filterTools()[1], "deleted_at", true},
		{filterTools()[1], "shared_by", false}, {filterTools()[2], "shared_by", true},
		{filterTools()[2], "deleted_at", false}, {filterTools()[0], "created_at", false},
	}
	for _, c := range cases {
		var calls []call
		env := newEnvironment(t, &calls, pageHandler(), nil)
		_, err := env.invoke(c.tool.id, c.tool.connection, listArgs(fmt.Sprintf(`,"order_by":[%q]`, c.value)))
		if (err == nil) != c.ok {
			t.Fatalf("%s %s: err = %v, want ok=%v", c.tool.id, c.value, err, c.ok)
		}
	}
}

func TestListCursorRoundTripsAndBindsTheFilters(t *testing.T) {
	for _, tool := range filterTools() {
		t.Run(tool.id, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, pageHandler(), nil)
			filters := `,"type":["file"],"order_by":["name"],"order":"asc"`
			result, err := env.invoke(tool.id, tool.connection, listArgs(filters))
			if err != nil {
				t.Fatal(err)
			}
			var page struct{ Cursor string }
			if err := json.Unmarshal([]byte(result), &page); err != nil || page.Cursor == "" || page.Cursor == "inner" {
				t.Fatalf("cursor = %q, %v, want a Qatlas cursor", page.Cursor, err)
			}
			raw, err := base64.RawURLEncoding.DecodeString(page.Cursor)
			if err != nil || !strings.HasSuffix(string(raw), "inner") || len(raw) != cursorFingerprintBytes+len("inner") {
				t.Fatalf("cursor payload = %q, %v", raw, err)
			}
			if len(page.Cursor) > maxCursorLength {
				t.Fatal("cursor exceeds maxCursorLength")
			}

			// Same filters, limit and with may change: the request carries Infomaniak's own cursor.
			calls = nil
			if _, err := env.invoke(tool.id, tool.connection, listArgs(filters+fmt.Sprintf(`,"limit":50,"with":["path"],"cursor":%q`, page.Cursor))); err != nil {
				t.Fatal(err)
			}
			if got := calls[len(calls)-1].query.Get("cursor"); got != "inner" {
				t.Fatalf("cursor sent = %q, want the unchanged Infomaniak cursor", got)
			}

			// Another filter, a manipulated, an unreadable and a pre-binding cursor never reach Infomaniak.
			tampered := []byte(page.Cursor)
			tampered[3] ^= 1
			for name, args := range map[string]string{
				"other type":     fmt.Sprintf(`,"type":["dir"],"order_by":["name"],"order":"asc","cursor":%q`, page.Cursor),
				"other order":    fmt.Sprintf(`,"type":["file"],"order_by":["name"],"order":"desc","cursor":%q`, page.Cursor),
				"no filters":     fmt.Sprintf(`,"cursor":%q`, page.Cursor),
				"tampered":       fmt.Sprintf(`%s,"cursor":%q`, filters, tampered),
				"not base64":     fmt.Sprintf(`%s,"cursor":"!!!"`, filters),
				"raw infomaniak": fmt.Sprintf(`%s,"cursor":"inner"`, filters),
				"fingerprint only": fmt.Sprintf(`%s,"cursor":%q`, filters,
					base64.RawURLEncoding.EncodeToString(raw[:cursorFingerprintBytes])),
			} {
				calls, *env.reads = nil, 0
				_, err := env.invoke(tool.id, tool.connection, listArgs(args))
				if !isInvalidRequest(err) || !strings.Contains(err.Error(), "first page") || len(calls) != 0 || *env.reads != 0 {
					t.Fatalf("%s: err = %v, calls = %+v, reads = %d", name, err, calls, *env.reads)
				}
			}
		})
	}
}

func TestListFingerprintIgnoresSetOrderAndKeepsSortPriority(t *testing.T) {
	a := listFilter{Type: []string{"dir", "file"}, OrderBy: []string{"name", "size"}}
	b := listFilter{Type: []string{"file", "dir"}, OrderBy: []string{"name", "size"}}
	c := listFilter{Type: []string{"dir", "file"}, OrderBy: []string{"size", "name"}}
	if string(a.fingerprint()) != string(b.fingerprint()) || string(a.fingerprint()) == string(c.fingerprint()) {
		t.Fatal("fingerprint must ignore set order but keep order_by priority")
	}
}
