package infomaniakdrive

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

func browseList(t *testing.T, req config.TargetListRequest, targets ...string) (config.TargetPage, error) {
	t.Helper()
	red := &redact.Redactor{}
	return registry(t).ListTargets(context.Background(), resolvedConnection(targets...), resolver(red, nil), red, req)
}

func TestTargetBrowserRegistration(t *testing.T) {
	meta, ok := registry(t).ProviderMetadata(Provider)
	if !ok {
		t.Fatal("provider not registered")
	}
	account, _ := meta.Target.KindByName("account")
	drive, _ := meta.Target.KindByName("drive")
	if strings.Join(account.Parents, ",") != "" || strings.Join(drive.Parents, ",") != "account" {
		t.Fatalf("parents = %q, %q", strings.Join(account.Parents, ","), strings.Join(drive.Parents, ","))
	}
	if !drive.Top {
		t.Fatal("drive is not offered at the top level")
	}
	if _, err := browseList(t, config.TargetListRequest{Kind: "account"}); errorsIsUnsupported(err) {
		t.Fatal("no target browser registered")
	}
}

func errorsIsUnsupported(err error) bool { return err == config.ErrTargetBrowseUnsupported }

func TestAccountTargetsComeFromTheAccountsEndpoint(t *testing.T) {
	var calls []call
	serve(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/1/accounts" {
			t.Fatalf("unexpected %s", r.URL.Path)
		}
		if r.URL.Query().Get("page") == "2" {
			return jsonResponse(200, `{"result":"success","data":[{"id":1003,"name":"Third"}],"page":2,"pages":2}`), nil
		}
		return jsonResponse(200, `{"result":"success","data":[{"id":1002,"name":"Customer B"},{"id":1001,"name":""},`+
			`{"id":0,"name":"Invalid"}],"page":1,"pages":2}`), nil
	}, nil)
	// A connection without any account target is a draft; the list still works.
	page, err := browseList(t, config.TargetListRequest{Kind: "account"})
	if err != nil {
		t.Fatal(err)
	}
	want := []config.TargetEntry{
		{Target: "account/1002", Name: "Customer B", Kind: "account", HasChildren: true},
		{Target: "account/1001", Name: "1001", Kind: "account", HasChildren: true},
	}
	if !reflect.DeepEqual(page.Entries, want) || page.Next != "2" || page.ServerSearch {
		t.Fatalf("page = %+v", page)
	}
	page, err = browseList(t, config.TargetListRequest{Kind: "account", Cursor: page.Next})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Target != "account/1003" || page.Next != "" {
		t.Fatalf("page 2 = %+v, %v", page, err)
	}
	if len(calls) != 2 || calls[0].query.Has("page") || calls[1].query.Get("page") != "2" {
		t.Fatalf("calls = %+v", calls)
	}
	for _, c := range calls {
		if c.path == "/2/drive" {
			t.Fatalf("the account level read the drives listing: %+v", c)
		}
	}
}

func TestAccountTargetsWithoutPaginationAreOnePage(t *testing.T) {
	var calls []call
	serve(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"result":"success","data":[{"id":1001,"name":"A"}]}`), nil
	}, nil)
	page, err := browseList(t, config.TargetListRequest{Kind: "account"})
	if err != nil || len(page.Entries) != 1 || page.Next != "" {
		t.Fatalf("page = %+v, %v", page, err)
	}
}

func TestAccountTargetsClassifyFailures(t *testing.T) {
	for status, want := range map[int]provider.Class{
		403: provider.ClassPermission, 404: provider.ClassNotFound, 401: provider.ClassAuth, 500: provider.ClassProviderError,
	} {
		var calls []call
		serve(t, &calls, func(r *http.Request) (*http.Response, error) {
			return jsonResponse(status, `{}`), nil
		}, nil)
		if _, err := browseList(t, config.TargetListRequest{Kind: "account"}); classOf(err) != want {
			t.Fatalf("status %d: err = %v", status, err)
		}
		if len(calls) != 1 || calls[0].path != "/1/accounts" {
			t.Fatalf("status %d: calls = %+v", status, calls)
		}
	}
}

func TestDriveTargetsWithoutParentUseTheSingleAccountTarget(t *testing.T) {
	var calls []call
	serve(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/2/drive" {
			t.Fatalf("unexpected %s", r.URL.Path)
		}
		return jsonResponse(200, `{"result":"success","data":[`+driveJSONOf(ownDrive, ownAccount, "Mine")+`,`+
			driveJSONOf(foreignDrive, otherAccount, foreignCanary)+`],"page":1,"pages":1,"total":2}`), nil
	}, nil)
	page, err := browseList(t, config.TargetListRequest{Kind: "drive"}, "account/1001", "drive/5001")
	if err != nil {
		t.Fatal(err)
	}
	want := []config.TargetEntry{{Target: "drive/5001", Name: "Mine", Kind: "drive"}}
	if !reflect.DeepEqual(page.Entries, want) {
		t.Fatalf("page = %+v", page)
	}
	if len(calls) != 1 || calls[0].query.Get("account_id") != "1001" {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestDriveTargetsWithoutParentNeedExactlyOneAccountTarget(t *testing.T) {
	var calls []call
	serve(t, &calls, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected %s", r.URL.Path)
		return nil, nil
	}, nil)
	for _, targets := range [][]string{nil, {"drive/5001"}, {"account/1001", "account/1002"}} {
		if _, err := browseList(t, config.TargetListRequest{Kind: "drive"}, targets...); !isInvalidRequest(err) {
			t.Fatalf("%v: err = %v", targets, err)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestDriveTargetsPageThroughCursorsAndIgnoreTargets(t *testing.T) {
	var calls []call
	serve(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/2/drive" {
			t.Fatalf("unexpected %s", r.URL.Path)
		}
		if r.URL.Query().Get("page") == "2" {
			return jsonResponse(200, `{"result":"success","data":[`+driveJSONOf(5003, ownAccount, "Third")+`],"page":2,"pages":2,"total":3}`), nil
		}
		return jsonResponse(200, `{"result":"success","data":[`+driveJSONOf(ownDrive, ownAccount, "First")+`,`+
			driveJSONOf(otherOwnDrive, ownAccount, "Second")+`,`+driveJSONOf(foreignDrive, otherAccount, foreignCanary)+
			`],"page":1,"pages":2,"total":3}`), nil
	}, nil)
	// The connection's only drive target is 5001; other drives still appear.
	req := config.TargetListRequest{Kind: "drive", Parent: "account/1001"}
	page, err := browseList(t, req, "account/1001", "drive/5001")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 2 || page.Entries[1].Target != "drive/5002" || page.Entries[1].Name != "Second" ||
		page.Entries[1].Kind != "drive" || page.Next != "2" || page.ServerSearch {
		t.Fatalf("page 1 = %+v", page)
	}
	req.Cursor = page.Next
	page, err = browseList(t, req)
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Target != "drive/5003" || page.Next != "" {
		t.Fatalf("page 2 = %+v, %v", page, err)
	}
	if calls[0].query.Get("account_id") != "1001" || calls[1].query.Get("page") != "2" {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestTargetListErrors(t *testing.T) {
	var calls []call
	serve(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(429, `{}`), nil
	}, nil)
	drive := config.TargetListRequest{Kind: "drive", Parent: "account/1001"}
	if _, err := browseList(t, drive); classOf(err) != provider.ClassRateLimited {
		t.Fatalf("err = %v", err)
	}
	for _, req := range []config.TargetListRequest{
		{Kind: "folder"},
		{Kind: "drive"},
		{Kind: "drive", Parent: "drive/5001"},
		{Kind: "account", Cursor: "abc"},
		{Kind: "account", Cursor: "0"},
	} {
		if _, err := browseList(t, req); !isInvalidRequest(err) {
			t.Fatalf("%+v: err = %v", req, err)
		}
	}
	if len(calls) != 1 {
		t.Fatalf("invalid requests sent %d requests", len(calls)-1)
	}
}

func TestResolveTargetURL(t *testing.T) {
	acc := func(id string) config.TargetEntry {
		return config.TargetEntry{Target: "account/" + id, Name: id, Kind: "account", HasChildren: true}
	}
	drv := func(id string) config.TargetEntry {
		return config.TargetEntry{Target: "drive/" + id, Name: id, Kind: "drive"}
	}
	fil := func(drive, id string) config.TargetEntry {
		return config.TargetEntry{Target: "drive/" + drive + "/files/" + id, Name: id, Kind: "files"}
	}
	cases := []struct {
		name, url string
		want      []config.TargetEntry
	}{
		{"kdrive", "https://kdrive.infomaniak.com/app/drive/5001/files", []config.TargetEntry{drv("5001")}},
		{"kdrive ignores an account number", "https://kdrive.infomaniak.com/1001/app/drive/5001", []config.TargetEntry{drv("5001")}},
		{"ksuite with account", "https://ksuite.infomaniak.com/1001/kdrive/app/drive/5001/files",
			[]config.TargetEntry{acc("1001"), drv("5001")}},
		{"ksuite files url", "https://ksuite.infomaniak.com/1001/kdrive/app/drive/5001/files/42",
			[]config.TargetEntry{acc("1001"), drv("5001"), fil("5001", "42")}},
		{"files url", "https://kdrive.infomaniak.com/app/drive/5001/files/42/", []config.TargetEntry{drv("5001"), fil("5001", "42")}},
		{"broken file id", "https://kdrive.infomaniak.com/app/drive/5001/files/abc", []config.TargetEntry{drv("5001")}},
		{"zero padded file id", "https://kdrive.infomaniak.com/app/drive/5001/files/042", []config.TargetEntry{drv("5001")}},
		{"extra segment after file", "https://kdrive.infomaniak.com/app/drive/5001/files/42/x", []config.TargetEntry{drv("5001")}},
		{"preview url gives the file", "https://kdrive.infomaniak.com/app/drive/5001/files/7/preview/pdf/8",
			[]config.TargetEntry{drv("5001"), fil("5001", "8")}},
		{"preview without type", "https://kdrive.infomaniak.com/app/drive/5001/files/7/preview/8",
			[]config.TargetEntry{drv("5001"), fil("5001", "8")}},
		{"preview with two numbers", "https://kdrive.infomaniak.com/app/drive/5001/files/7/preview/9/8", []config.TargetEntry{drv("5001")}},
		{"preview with broken number", "https://kdrive.infomaniak.com/app/drive/5001/files/7/preview/8x", []config.TargetEntry{drv("5001")}},
		{"preview without file", "https://kdrive.infomaniak.com/app/drive/5001/files/7/preview", []config.TargetEntry{drv("5001")}},
		{"preview with broken folder", "https://kdrive.infomaniak.com/app/drive/5001/files/x/preview/8", []config.TargetEntry{drv("5001")}},
		{"foreign host files url", "https://example.invalid/app/drive/5001/files/42", nil},
		{"ksuite without account", "https://ksuite.infomaniak.com/kdrive/app/drive/5001", []config.TargetEntry{drv("5001")}},
		{"upper case host", "https://KDRIVE.infomaniak.com/app/drive/5001", []config.TargetEntry{drv("5001")}},
		{"foreign host", "https://example.invalid/app/drive/5001", nil},
		{"api host", "https://api.infomaniak.com/2/drive/5001", nil},
		{"lookalike host", "https://kdrive.infomaniak.com.example.invalid/app/drive/5001", nil},
		{"missing number", "https://kdrive.infomaniak.com/app/drive", nil},
		{"non numeric", "https://kdrive.infomaniak.com/app/drive/abc", nil},
		{"zero padded", "https://kdrive.infomaniak.com/app/drive/05001", nil},
		{"no drive segment", "https://kdrive.infomaniak.com/app/files/5001", nil},
		{"invalid url", "https://kdrive.infomaniak.com/%zz", nil},
	}
	var calls []call
	serve(t, &calls, func(*http.Request) (*http.Response, error) { return nil, nil }, nil)
	red := &redact.Redactor{}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := registry(t).ResolveTargetURL(context.Background(), resolvedConnection(), resolver(red, nil), red, apiRoot, tt.url)
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
	if len(calls) != 0 {
		t.Fatalf("URL resolution sent %d requests", len(calls))
	}
}

func TestFileTargetsUnderDriveAndFolder(t *testing.T) {
	var calls []call
	serve(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/3/drive/5001/files/1/files":
			return jsonResponse(200, `{"result":"success","data":[`+
				fileJSONOf(30, 1, "Reports", "dir")+`,`+fileJSONOf(31, 1, "a.pdf", "file")+`],"cursor":"c2","has_more":true}`), nil
		case "/3/drive/5001/files/30":
			if r.URL.Query().Get("with") != "parents" {
				t.Errorf("no parents asked")
			}
			// Parents deliberately out of order; the root is among them.
			return jsonResponse(200, `{"result":"success","data":`+
				`{"id":30,"name":"Reports","type":"dir","parent_id":20,"parents":[{"id":20,"name":"Kunden","parent_id":10},`+
				`{"id":1,"name":"Root","parent_id":0},{"id":10,"name":"A\u0007","parent_id":1}]}}`), nil
		case "/3/drive/5001/files/31":
			return jsonResponse(200, `{"result":"success","data":{"id":31,"name":"x","type":"dir","parent_id":77,"parents":[{"id":5,"name":"Z","parent_id":1}]}}`), nil
		case "/3/drive/5001/files/30/files":
			return jsonResponse(200, `{"result":"success","data":[`+fileJSONOf(32, 30, "b.txt", "file")+`],"cursor":"","has_more":false}`), nil
		}
		t.Fatalf("unexpected %s", r.URL.Path)
		return nil, nil
	}, nil)
	page, err := browseList(t, config.TargetListRequest{Kind: "files", Parent: "drive/5001"}, "account/1001", "drive/5002")
	if err != nil {
		t.Fatal(err)
	}
	want := []config.TargetEntry{
		{Target: "drive/5001/files/30", Name: "Reports", Kind: "files", Path: "/", HasChildren: true},
		{Target: "drive/5001/files/31", Name: "a.pdf", Kind: "files", Path: "/"},
	}
	if !reflect.DeepEqual(page.Entries, want) || page.Next != "c2" || page.ServerSearch {
		t.Fatalf("page = %+v", page)
	}
	page, err = browseList(t, config.TargetListRequest{Kind: "files", Parent: "drive/5001/files/30"})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Target != "drive/5001/files/32" || page.Entries[0].Path != "/A/Kunden/Reports" || page.Next != "" {
		t.Fatalf("folder page = %+v, %v", page, err)
	}
	if _, err = browseList(t, config.TargetListRequest{Kind: "files", Parent: "drive/5001", Cursor: "c2"}); err != nil {
		t.Fatal(err)
	}
	if calls[3].query.Get("cursor") != "c2" || calls[0].query.Has("cursor") || len(calls) != 4 ||
		calls[0].path != "/3/drive/5001/files/1/files" || calls[1].path != "/3/drive/5001/files/30" ||
		calls[2].path != "/3/drive/5001/files/30/files" {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestFileTargetErrors(t *testing.T) {
	var calls []call
	serve(t, &calls, func(*http.Request) (*http.Response, error) { return jsonResponse(404, `{}`), nil }, nil)
	for _, req := range []config.TargetListRequest{
		{Kind: "files"},
		{Kind: "files", Parent: "account/1001"},
		{Kind: "files", Parent: "drive/abc"},
		{Kind: "files", Parent: "drive/5001/files/x"},
		{Kind: "files", Parent: "drive/5001", Cursor: "a\nb"},
		{Kind: "files", Parent: "drive/5001", Cursor: strings.Repeat("a", 2000)},
	} {
		if _, err := browseList(t, req); !isInvalidRequest(err) {
			t.Fatalf("%+v: err = %v", req, err)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("invalid requests sent %d requests", len(calls))
	}
	_, err := browseList(t, config.TargetListRequest{Kind: "files", Parent: "drive/5001/files/9"})
	if classOf(err) != provider.ClassNotFound {
		t.Fatalf("err = %v", err)
	}
}

func TestFolderPathNeedsAUnambiguousChain(t *testing.T) {
	var calls []call
	serve(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/3/drive/5001/files/31" {
			return jsonResponse(200, `{"result":"success","data":{"id":31,"name":"x","type":"dir","parent_id":77,"parents":[{"id":5,"name":"Z","parent_id":1}]}}`), nil
		}
		return jsonResponse(200, `{"result":"success","data":[`+fileJSONOf(40, 31, "f", "file")+`],"cursor":"","has_more":false}`), nil
	}, nil)
	page, err := browseList(t, config.TargetListRequest{Kind: "files", Parent: "drive/5001/files/31"})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Path != "" {
		t.Fatalf("page = %+v, %v", page, err)
	}
}
