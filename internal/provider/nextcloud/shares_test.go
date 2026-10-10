package nextcloud

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	tokenCanary = "link-token-canary-nextcloud-5d2f"
	urlCanary   = "https://cloud.example.invalid/s/link-token-canary-nextcloud-5d2f"
	hashCanary  = "password-hash-canary-nextcloud-9a40"
	textCanary  = "provider-message-canary-nextcloud-1e77"
	roomCanary  = "talk-room-token-canary-nextcloud-3c81"
	otherCanary = "unknown-type-share-with-canary-nextcloud-77b0"
)

func ocsResponse(status int, body string) *http.Response {
	response := xmlResponse(status, body)
	response.Header = http.Header{"Content-Type": []string{"application/json"}}
	return response
}

func ocsData(data string) string {
	return `{"ocs":{"meta":{"status":"ok","statuscode":200,"message":"` + textCanary + `"},"data":` + data + `}}`
}

func shareJSON(id, shareType, owner, path, target, with, perms string, extra string) string {
	return `{"id":"` + id + `","share_type":` + shareType + `,"uid_owner":"` + owner + `","uid_initiator":"` + owner +
		`","path":"` + path + `","file_target":"` + target + `","item_type":"file","permissions":` + perms +
		`,"share_with":"` + with + `","share_with_displayname":"Name of ` + with + `"` + extra + `}`
}

const linkExtra = `,"token":"` + tokenCanary + `","url":"` + urlCanary + `","password":"` + hashCanary + `"`

const passwordExtra = `,"password":"` + hashCanary + `"`

func TestSharesListFiltersToTheRootAndHidesAccessSecrets(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		return ocsResponse(http.StatusOK, ocsData(`[`+
			shareJSON("1", "0", aliceUser, "/Reports/q1.pdf", "/q1.pdf", "bob", "19", `,"expiration":"2026-12-31 00:00:00","note":"hi","label":"lbl"`)+`,`+
			shareJSON("2", "3", aliceUser, "/Reports/2026", "/2026", hashCanary, "1", linkExtra)+`,`+
			shareJSON("3", "4", aliceUser, "/Reports", "/Reports", "x@example.invalid", "3", passwordExtra)+`,`+
			shareJSON("4", "6", aliceUser, "/Reports/a", "/a", "r@remote", "1", "")+`,`+
			shareJSON("5", "0", aliceUser, "/Other/secret.txt", "/secret.txt", "bob", "1", "")+`,`+
			shareJSON("6", "0", aliceUser, "/ReportsX/a", "/a", "bob", "1", "")+`,`+
			shareJSON("7", "99", aliceUser, "/Reports/b", "/b", otherCanary, "1", "")+`,`+
			shareJSON("8", "0", "dave", "/Reports/c", "/Reports/c", "alice", "1", "")+`,`+
			shareJSON("9", "10", aliceUser, "/Reports/d", "/d", roomCanary, "1", `,"share_with_displayname":"Project room"`)+`,`+
			shareJSON("10", "7", aliceUser, "/Reports/e", "/e", "circle1", "1", "")+
			`]`)), nil
	})
	c, _ := client(t)
	result, err := c.ListShares(context.Background(), "", false, false)
	if err != nil {
		t.Fatalf("ListShares() = %v", err)
	}
	var ids []string
	for _, share := range result.Shares {
		ids = append(ids, share.ID)
	}
	if strings.Join(ids, ",") != "1,2,3,4,7,9,10" {
		t.Errorf("ids = %v, want only the outgoing shares below the root", ids)
	}
	encoded, _ := json.Marshal(result)
	for _, canary := range []string{tokenCanary, urlCanary, hashCanary, textCanary, roomCanary, otherCanary} {
		if strings.Contains(string(encoded), canary) {
			t.Errorf("the result carries %q: %s", canary, encoded)
		}
	}
	first, link, email := result.Shares[0], result.Shares[1], result.Shares[2]
	if first.Type != "user" || first.Path != "q1.pdf" || first.Recipient.ID != "bob" || first.Recipient.Name != "Name of bob" ||
		!first.Rights.Read || !first.Rights.Update || first.Rights.Create || first.Rights.Delete || !first.Rights.Share ||
		first.ExpiresAt != "2026-12-31T00:00:00Z" || first.Note != "hi" || first.Label != "lbl" || first.HasPassword {
		t.Errorf("first = %+v", first)
	}
	if link.Type != "link" || link.Recipient != nil || !link.HasPassword || email.Type != "email" || !email.HasPassword ||
		email.Path != "" || result.Shares[3].Type != "federated" || result.Shares[3].Recipient.ID != "r@remote" ||
		result.Shares[4].Type != "other" || result.Shares[4].Recipient != nil {
		t.Errorf("shares = %+v", result.Shares)
	}
	talk, team := result.Shares[5], result.Shares[6]
	if talk.Type != "talk" || talk.Recipient == nil || talk.Recipient.ID != "" || talk.Recipient.Name != "Project room" {
		t.Errorf("talk = %+v", talk)
	}
	if team.Type != "team" || team.Recipient == nil || team.Recipient.ID != "circle1" {
		t.Errorf("team = %+v", team)
	}
	request := (*calls)[0]
	if request.method != http.MethodGet || request.url.Path != "/ocs/v2.php/apps/files_sharing/api/v1/shares" ||
		request.url.Query().Get("format") != "json" || request.url.Query().Get("path") != "" {
		t.Errorf("request = %+v", request)
	}
}

func TestOCSRequestsCarryTheOCSHeadersAndBasicAuth(t *testing.T) {
	var header http.Header
	serve(t, func(request *http.Request) (*http.Response, error) {
		header = request.Header
		return ocsResponse(http.StatusOK, ocsData(`[]`)), nil
	})
	c, _ := client(t)
	if _, err := c.ListShares(context.Background(), "2026", true, false); err != nil {
		t.Fatalf("ListShares() = %v", err)
	}
	if header.Get("OCS-APIRequest") != "true" || header.Get("Accept") != "application/json" ||
		header.Get("Authorization") != basicAuth(aliceUser, aliceToken) {
		t.Errorf("headers = %v", header)
	}
}

func TestSharesListPathAndFlagsBecomeFixedQueryValues(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		return ocsResponse(http.StatusOK, ocsData(`[]`)), nil
	})
	c, _ := client(t)
	if _, err := c.ListShares(context.Background(), "2026/a b", true, false); err != nil {
		t.Fatalf("ListShares() = %v", err)
	}
	if _, err := c.ListShares(context.Background(), "", false, true); err != nil {
		t.Fatalf("ListShares() = %v", err)
	}
	first, second := (*calls)[0].url.Query(), (*calls)[1].url.Query()
	if first.Get("path") != "/Reports/2026/a b" || first.Get("subfiles") != "true" || first.Get("shared_with_me") != "" ||
		second.Get("shared_with_me") != "true" || second.Get("path") != "" {
		t.Errorf("queries = %v, %v", first, second)
	}
	for _, bad := range []struct {
		path     string
		subfiles bool
		incoming bool
	}{{"", true, false}, {"x", true, true}, {"../Audit", false, false}} {
		if _, err := c.ListShares(context.Background(), bad.path, bad.subfiles, bad.incoming); err == nil {
			t.Errorf("ListShares(%+v) was accepted", bad)
		}
	}
	if len(*calls) != 2 {
		t.Errorf("calls = %d, want the refused ones not sent", len(*calls))
	}
}

func TestIncomingSharesAreBoundByTheirTarget(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		return ocsResponse(http.StatusOK, ocsData(`[`+
			shareJSON("1", "0", "dave", "/Elsewhere/x", "/Reports/x", "alice", "1", "")+`,`+
			shareJSON("2", "0", "dave", "/Reports/y", "/Shared/y", "alice", "1", "")+`,`+
			shareJSON("3", "0", aliceUser, "/Reports/z", "/z", "bob", "1", "")+
			`]`)), nil
	})
	c, _ := client(t)
	result, err := c.ListShares(context.Background(), "", false, true)
	if err != nil {
		t.Fatalf("ListShares() = %v", err)
	}
	if len(result.Shares) != 1 || result.Shares[0].ID != "1" || result.Shares[0].Direction != "incoming" ||
		result.Shares[0].Path != "x" {
		t.Errorf("shares = %+v, want only the incoming share whose target is below the root", result.Shares)
	}
	if (*calls)[0].url.Query().Get("shared_with_me") != "true" {
		t.Errorf("query = %v", (*calls)[0].url.Query())
	}
}

func TestSharesGetRefusesAForeignShareLikeAMissingOne(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"outside the root": {http.StatusOK, ocsData(`[` + shareJSON("5", "0", aliceUser, "/Other/s.txt", "/s.txt", "bob", "1", "") + `]`)},
		"missing":          {http.StatusNotFound, `{"ocs":{"meta":{"status":"failure","statuscode":404,"message":"` + textCanary + `"},"data":[]}}`},
		"empty data":       {http.StatusOK, ocsData(`[]`)},
	} {
		t.Run(name, func(t *testing.T) {
			serve(t, func(*http.Request) (*http.Response, error) { return ocsResponse(tc.status, tc.body), nil })
			c, _ := client(t)
			_, err := c.GetShare(context.Background(), "5")
			if err == nil || classOf(err) != provider.ClassNotFound || err.Error() != "get share: "+messageShareNotFound &&
				!strings.Contains(err.Error(), messageShareNotFound) {
				t.Fatalf("GetShare() = %v", err)
			}
			for _, leak := range []string{"Other", "secret", textCanary, "s.txt"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("the error names %q: %v", leak, err)
				}
			}
		})
	}
}

func TestSharesGetReturnsAShareBelowTheRoot(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		return ocsResponse(http.StatusOK, ocsData(`[`+shareJSON("12", "3", aliceUser, "/Reports/q1.pdf", "/q1.pdf", hashCanary, "1", linkExtra)+`]`)), nil
	})
	c, _ := client(t)
	share, err := c.GetShare(context.Background(), "12")
	if err != nil {
		t.Fatalf("GetShare() = %v", err)
	}
	encoded, _ := json.Marshal(share)
	for _, canary := range []string{tokenCanary, urlCanary, hashCanary} {
		if strings.Contains(string(encoded), canary) {
			t.Errorf("the share carries %q: %s", canary, encoded)
		}
	}
	if share.Type != "link" || !share.HasPassword || share.Path != "q1.pdf" ||
		(*calls)[0].url.Path != "/ocs/v2.php/apps/files_sharing/api/v1/shares/12" {
		t.Errorf("share = %+v, url = %s", share, (*calls)[0].url)
	}
}

func TestInvalidShareIDsAreRefusedBeforeAnyRequest(t *testing.T) {
	refuse(t)
	c, _ := client(t)
	for _, id := range []string{"", "1/2", "../1", "1?x=y", "ocinternal:1", "1 ", "%31", strings.Repeat("1", 19)} {
		if _, err := c.GetShare(context.Background(), id); err == nil {
			t.Errorf("GetShare(%q) was accepted", id)
		}
	}
}

func TestOCSEnvelopeFailuresAreClearAndCarryNoProviderText(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		class  provider.Class
	}{
		"wrong status code":  {200, `{"ocs":{"meta":{"status":"ok","statuscode":100,"message":"` + textCanary + `"},"data":[]}}`, provider.ClassInvalidResponse},
		"failure status":     {200, `{"ocs":{"meta":{"status":"failure","statuscode":200,"message":"` + textCanary + `"},"data":[]}}`, provider.ClassInvalidResponse},
		"missing meta":       {200, `{"ocs":{"data":[]}}`, provider.ClassInvalidResponse},
		"missing envelope":   {200, `{"data":[]}`, provider.ClassInvalidResponse},
		"unreadable":         {200, `<html>` + textCanary + `</html>`, provider.ClassInvalidResponse},
		"mirrored forbidden": {200, `{"ocs":{"meta":{"status":"failure","statuscode":403,"message":"` + textCanary + `"},"data":[]}}`, provider.ClassPermission},
		"unauthorised":       {401, `{"ocs":{"meta":{"status":"failure","statuscode":401,"message":"` + textCanary + `"}}}`, provider.ClassAuth},
		"unusable data":      {200, ocsData(`{"a":1}`), provider.ClassInvalidResponse},
	} {
		t.Run(name, func(t *testing.T) {
			serve(t, func(*http.Request) (*http.Response, error) { return ocsResponse(tc.status, tc.body), nil })
			c, _ := client(t)
			_, err := c.ListShares(context.Background(), "", false, false)
			if err == nil || classOf(err) != tc.class || strings.Contains(err.Error(), textCanary) {
				t.Errorf("ListShares() = %v (class %q), want class %q", err, classOf(err), tc.class)
			}
		})
	}
}

func TestOCSOversizedAnswersAndRedirectsAreRefused(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return ocsResponse(http.StatusOK, ocsData(`["`+strings.Repeat("a", maxBodyBytes)+`"]`)), nil
	})
	c, _ := client(t)
	if _, err := c.ListShares(context.Background(), "", false, false); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("oversized: %v", err)
	}
	serve(t, func(*http.Request) (*http.Response, error) {
		response := ocsResponse(http.StatusFound, "")
		response.Header.Set("Location", "https://evil.example.invalid/ocs")
		return response, nil
	})
	c, _ = client(t)
	_, err := c.ListShares(context.Background(), "", false, false)
	if err == nil || !strings.Contains(err.Error(), messageRedirect) || strings.Contains(err.Error(), "evil") {
		t.Errorf("redirect: %v", err)
	}
}

func accountClient(t *testing.T, targets ...string) *application.Core {
	t.Helper()
	cfg := coreConfig()
	connection := cfg.Connections["reports"]
	connection.Target = ""
	connection.Targets = targets
	cfg.Connections["reports"] = connection
	red := &redact.Redactor{}
	return application.New(registry(t), cfg, resolver(red), red)
}

func TestShareesSearchSendsFixedQueryAndReportsOnlyIdentity(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		return ocsResponse(http.StatusOK, ocsData(`{"exact":{"users":[{"label":"Bob","value":{"shareType":0,"shareWith":"bob"},"uuid":"u","extra":"`+textCanary+`"}],"groups":[]},`+
			`"users":[{"label":"Bob","value":{"shareType":0,"shareWith":"bob"}},{"label":"Bobby","value":{"shareType":0,"shareWith":"bobby"}}],`+
			`"groups":[{"label":"Team","value":{"shareType":1,"shareWith":"team"}}],`+
			`"emails":[{"label":"e","value":{"shareType":4,"shareWith":"e@example.invalid"}}],`+
			`"rooms":[{"label":"Room","value":{"shareType":10,"shareWith":"`+roomCanary+`"}}],`+
			`"circles":[{"label":"Odd","value":{"shareType":99,"shareWith":"`+otherCanary+`"}}],`+
			`"lookup":[{"label":"L","value":{"shareType":0,"shareWith":"far"}}],"lookupEnabled":false}`)), nil
	})
	core := accountClient(t, "account")
	response, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: "nextcloud.sharees.search", Connection: "reports",
		Arguments: json.RawMessage(`{"search":"bo","per_page":50,"item_type":"folder"}`),
	})
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	var result ShareeResult
	if err := json.Unmarshal(response.Result, &result); err != nil || result.Count != 4 ||
		result.Sharees[0] != (Sharee{Type: "user", ID: "bob", Name: "Bob"}) || strings.Contains(string(response.Result), textCanary) ||
		strings.Contains(string(response.Result), "far") || strings.Contains(string(response.Result), roomCanary) ||
		strings.Contains(string(response.Result), otherCanary) {
		t.Errorf("result = %s, %v", response.Result, err)
	}
	query := (*calls)[0].url.Query()
	if (*calls)[0].url.Path != "/ocs/v2.php/apps/files_sharing/api/v1/sharees" || query.Get("lookup") != "false" ||
		query.Get("perPage") != "50" || query.Get("itemType") != "folder" || query.Get("search") != "bo" ||
		query.Get("format") != "json" {
		t.Errorf("request = %s", (*calls)[0].url)
	}
}

func TestShareesSearchNeedsTheAccountTargetAndABoundedPageSize(t *testing.T) {
	refuse(t)
	for name, tc := range map[string]struct {
		targets []string
		args    string
	}{
		"folder only":      {[]string{"folder/Reports"}, `{"search":"bo"}`},
		"per page too big": {[]string{"account"}, `{"search":"bo","per_page":51}`},
		"per page zero":    {[]string{"account"}, `{"search":"bo","per_page":0}`},
		"no search":        {[]string{"account"}, `{}`},
		"free parameter":   {[]string{"account"}, `{"search":"bo","lookup":true}`},
		"bad item type":    {[]string{"account"}, `{"search":"bo","item_type":"call"}`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := accountClient(t, tc.targets...).Invoke(context.Background(), application.InvokeRequest{
				Operation: "nextcloud.sharees.search", Connection: "reports", Arguments: json.RawMessage(tc.args),
			})
			if err == nil {
				t.Error("the request was accepted")
			}
		})
	}
	// Called directly, the handler bounds the page size as well.
	c, _ := client(t)
	if _, err := c.SearchSharees(context.Background(), "bo", "file", maxPerPage+1); err == nil {
		t.Error("SearchSharees accepted a page size beyond the cap")
	}
}

func TestSharesNeedAFolderTargetAndAccountOnlyConnectionsAreRefusedLocally(t *testing.T) {
	refuse(t)
	for _, op := range []string{"nextcloud.shares.list", "nextcloud.shares.get"} {
		_, err := accountClient(t, "account").Invoke(context.Background(), application.InvokeRequest{
			Operation: op, Connection: "reports", Arguments: json.RawMessage(`{"share_id":"1"}`),
		})
		if err == nil {
			t.Errorf("%s ran on a connection without a folder", op)
		}
	}
}

func TestSharesOperationsRunThroughTheApplicationCore(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return ocsResponse(http.StatusOK, ocsData(`[`+shareJSON("12", "0", aliceUser, "/Reports/q1.pdf", "/q1.pdf", "bob", "1", "")+`]`)), nil
	})
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(), resolver(red), red)
	for _, request := range []application.InvokeRequest{
		{Operation: "nextcloud.shares.list", Connection: "reports", Arguments: json.RawMessage(`{"shared_with_me":false}`)},
		{Operation: "nextcloud.shares.get", Connection: "reports", Arguments: json.RawMessage(`{"share_id":"12"}`)},
	} {
		response, err := core.Invoke(context.Background(), request)
		if err != nil || !strings.Contains(string(response.Result), `"id":"12"`) {
			t.Errorf("%s = %s, %v", request.Operation, response.Result, err)
		}
	}
	for _, bad := range []string{`{"share_id":"1/2"}`, `{"share_id":"x"}`, `{}`, `{"share_id":"1","path":"x"}`} {
		if _, err := core.Invoke(context.Background(), application.InvokeRequest{
			Operation: "nextcloud.shares.get", Connection: "reports", Arguments: json.RawMessage(bad),
		}); err == nil {
			t.Errorf("shares.get accepted %s", bad)
		}
	}
}

func TestProfilesOfferTheShareReadsButNotTheRecipientSearch(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		if profile.ID == "deck-read" || profile.ID == "talk-read" || profile.ID == "notes-read" {
			continue
		}
		has := map[string]bool{}
		for _, id := range profile.Tools {
			has[id] = true
		}
		if !has[sharesList.ID] || !has[sharesGet.ID] || has[shareesSearch.ID] {
			t.Errorf("profile %s tools = %v", profile.ID, profile.Tools)
		}
	}
}
