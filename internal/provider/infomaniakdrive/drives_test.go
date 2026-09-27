package infomaniakdrive

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

// ListDrives reports the account's own page and total transparently, while filtering the entries themselves
// to the connection's drive allow-list and to drives that actually belong to the bound account.
func TestDrivesListFiltersToTheConnectionAndReportsPagingTransparently(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/2/drive" {
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
		return jsonResponse(200, `{"result":"success","data":[`+
			driveJSONOf(ownDrive, ownAccount, "Own")+`,`+
			driveJSONOf(otherOwnDrive, ownAccount, "Other")+`,`+
			driveJSONOf(foreignDrive, otherAccount, foreignCanary)+
			`],"page":2,"pages":3,"total":21}`), nil
	}, nil)

	// The "drive" connection allows only ownDrive, so the same account's other drive and the foreign
	// account's drive are both left out, even though the fake server (like a broader token) returned them.
	result, err := env.invoke(drivesList.ID, "drive", `{"page":2}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var page DrivesPage
	if err := json.Unmarshal([]byte(result), &page); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if page.Page != 2 || page.Pages != 3 || page.Total != 21 {
		t.Fatalf("page = %+v, want the account's own pagination reported transparently", page)
	}
	if page.Count != 1 || len(page.Drives) != 1 || page.Drives[0].ID != ownDrive {
		t.Fatalf("drives = %+v, want only the allow-listed drive of the bound account", page.Drives)
	}
	for _, drive := range page.Drives {
		if drive.Name == foreignCanary {
			t.Fatalf("a foreign drive's name leaked into the result: %+v", page.Drives)
		}
	}
	if len(calls) != 1 || calls[0].query.Get("account_id") != strconv.FormatInt(ownAccount, 10) ||
		calls[0].query.Get("page") != "2" {
		t.Fatalf("calls = %+v", calls)
	}

	// An account-only connection (no drive allow-list) keeps every drive of the bound account, but never one
	// of a foreign account, even if the fake server answered with one.
	result, err = env.invoke(drivesList.ID, "account", `{}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if err := json.Unmarshal([]byte(result), &page); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if page.Count != 2 {
		t.Fatalf("drives = %+v, want both of the bound account and none of the foreign one", page.Drives)
	}
	for _, drive := range page.Drives {
		if drive.Name == foreignCanary {
			t.Fatalf("a foreign account's drive leaked into the result: %+v", page.Drives)
		}
	}
}
