package nextcloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	talkFilesRoomPath = "/ocs/v2.php/apps/spreed/api/v4/room/" + boundRoom
	talkFilesArgs     = `{"token":"` + boundRoom + `","path":"2026/q1.pdf"}`
)

// talkFilesCore binds the folder and the given talk targets and lists the tool unless tools is false.
func talkFilesCore(t *testing.T, listed bool, targets ...string) *application.Core {
	t.Helper()
	cfg := coreConfig()
	connection := cfg.Connections["reports"]
	connection.Target = ""
	connection.Targets = targets
	connection.Effects = []config.Effect{config.EffectRead, config.EffectCreate,
		config.EffectUpdate, config.EffectDelete}
	if listed {
		connection.Tools = []string{talkFilesShare.ID, sharesDelete.ID}
	}
	cfg.Connections["reports"] = connection
	red := &redact.Redactor{}
	return application.New(registry(t), cfg, resolver(red), red)
}

func talkFilesBound() []string { return []string{"folder/Reports", "talk/" + boundRoom} }

func talkFilesShareCall(t *testing.T, core *application.Core, args string, confirmed bool) (application.InvokeResponse, error) {
	t.Helper()
	return core.Invoke(context.Background(), application.InvokeRequest{
		Operation: talkFilesShare.ID, Connection: "reports", Arguments: json.RawMessage(args), Confirmed: confirmed,
	})
}

func roomRead(kind int, objectType string) string {
	return `{"token":"` + boundRoom + `","type":` + strconv.Itoa(kind) + `,"participantType":3,"objectType":"` + objectType + `"}`
}

// talkFilesServer answers the room read with room and every other request with change.
func talkFilesServer(t *testing.T, room string, change func(*http.Request) (*http.Response, error)) *[]call {
	t.Helper()
	return serve(t, func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodGet && request.URL.Path == talkFilesRoomPath {
			return ocsResponse(http.StatusOK, ocsData(room)), nil
		}
		return change(request)
	})
}

func talkShareCreated(*http.Request) (*http.Response, error) {
	return ocsResponse(http.StatusOK, ocsData(shareJSON("7", "10", aliceUser, "/Reports/2026/q1.pdf", "/q1.pdf", boundRoom, "1",
		`,"share_with_displayname":"Team"`))), nil
}

func TestTalkFilesShareSendsOneFixedPostAfterOneRoomRead(t *testing.T) {
	calls := talkFilesServer(t, roomRead(2, ""), talkShareCreated)
	response, err := talkFilesShareCall(t, talkFilesCore(t, true, talkFilesBound()...), talkFilesArgs, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 || (*calls)[0].method != http.MethodGet || (*calls)[0].url.Path != talkFilesRoomPath {
		t.Fatalf("calls = %+v", *calls)
	}
	post := (*calls)[1]
	form := formOf(t, post)
	if post.method != http.MethodPost || post.url.Path != "/ocs/v2.php/apps/files_sharing/api/v1/shares" ||
		post.auth != basicAuth(aliceUser, aliceToken) || form.Get("shareType") != "10" || form.Get("shareWith") != boundRoom ||
		form.Get("path") != "/Reports/2026/q1.pdf" || form.Get("permissions") != "1" || len(form) != 4 {
		t.Errorf("request = %+v, form = %v", post, form)
	}
	encoded, _ := json.Marshal(response.Result)
	if !strings.Contains(string(encoded), `"shared":true`) || !strings.Contains(string(encoded), `"id":"7"`) ||
		!strings.Contains(string(encoded), `"type":"talk"`) || strings.Contains(string(encoded), boundRoom) {
		t.Errorf("result = %s", encoded)
	}
}

func TestTalkFilesShareIsRevokedByShareDelete(t *testing.T) {
	calls := serve(t, func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == http.MethodGet:
			return ocsResponse(http.StatusOK, ocsData(`[`+shareJSON("7", "10", aliceUser, "/Reports/2026/q1.pdf", "/q1.pdf",
				boundRoom, "1", "")+`]`)), nil
		}
		return ocsResponse(http.StatusOK, ocsData(`[]`)), nil
	})
	result, err := runShare(deleteShareTool, obj{"share_id": "7"})
	if err != nil || len(*calls) != 2 || (*calls)[1].method != http.MethodDelete ||
		(*calls)[1].url.Path != "/ocs/v2.php/apps/files_sharing/api/v1/shares/7" {
		t.Fatalf("err = %v, calls = %+v", err, *calls)
	}
	if encoded, _ := json.Marshal(result); !strings.Contains(string(encoded), `"type":"talk"`) {
		t.Errorf("result = %s", encoded)
	}
}

func TestTalkFilesShareNeedsConfirmationAndAToolsList(t *testing.T) {
	calls := talkFilesServer(t, roomRead(2, ""), talkShareCreated)
	if _, err := talkFilesShareCall(t, talkFilesCore(t, true, talkFilesBound()...), talkFilesArgs, false); err == nil ||
		!strings.Contains(err.Error(), "confirm") {
		t.Errorf("unconfirmed: err = %v", err)
	}
	if _, err := talkFilesShareCall(t, talkFilesCore(t, false, talkFilesBound()...), talkFilesArgs, true); err == nil {
		t.Error("ran without a tools list")
	}
	if len(*calls) != 0 {
		t.Errorf("calls = %+v", *calls)
	}
	risk := talkFilesShare.Risk
	if !talkFilesShare.RequiresToolAllowList || risk.Effect != "create" || !risk.OpenWorld ||
		risk.Confirmation != "required" || risk.Idempotency == "" || risk.DataSensitivity == "" {
		t.Errorf("descriptor = %+v", talkFilesShare)
	}
}

func TestTalkFilesShareRefusesMissingBindingsAndBadArgumentsLocally(t *testing.T) {
	calls := talkFilesServer(t, roomRead(2, ""), talkShareCreated)
	cases := map[string]struct {
		targets []string
		args    string
	}{
		"no folder":     {[]string{"talk/" + boundRoom}, talkFilesArgs},
		"no talk":       {[]string{"folder/Reports"}, talkFilesArgs},
		"foreign room":  {talkFilesBound(), `{"token":"` + foreignRoom + `","path":"2026/q1.pdf"}`},
		"bad token":     {talkFilesBound(), `{"token":"../x","path":"2026/q1.pdf"}`},
		"outside root":  {talkFilesBound(), `{"token":"` + boundRoom + `","path":"../ForeignAudit/x"}`},
		"absolute path": {talkFilesBound(), `{"token":"` + boundRoom + `","path":"/ForeignAudit/x"}`},
		"url path":      {talkFilesBound(), `{"token":"` + boundRoom + `","path":"https://foreign.example.invalid/x"}`},
		"root itself":   {talkFilesBound(), `{"token":"` + boundRoom + `","path":""}`},
		"permissions":   {talkFilesBound(), `{"token":"` + boundRoom + `","path":"a","permissions":31}`},
		"expiry":        {talkFilesBound(), `{"token":"` + boundRoom + `","path":"a","expires_at":"2027-01-01"}`},
	}
	for name, c := range cases {
		_, err := talkFilesShareCall(t, talkFilesCore(t, true, c.targets...), c.args, true)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		for _, leak := range []string{foreignRoom, "ForeignAudit", "foreign.example"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("%s: error names %q: %v", name, leak, err)
			}
		}
	}
	if len(*calls) != 0 {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestTalkFilesShareRefusesNonPrivateRoomsAfterOneRead(t *testing.T) {
	for name, room := range map[string]string{
		"public": roomRead(3, ""), "changelog": roomRead(4, ""), "former one-to-one": roomRead(5, ""),
		"note to self": roomRead(6, ""), "unknown": roomRead(99, ""), "no type": `{"token":"` + boundRoom + `"}`,
		"object room": roomRead(2, "room"), "other token": strings.Replace(roomRead(2, ""), boundRoom, "other999", 1),
	} {
		calls := talkFilesServer(t, room, talkShareCreated)
		_, err := talkFilesShareCall(t, talkFilesCore(t, true, talkFilesBound()...), talkFilesArgs, true)
		if err == nil || len(*calls) != 1 || (*calls)[0].method != http.MethodGet {
			t.Errorf("%s: err = %v, calls = %+v", name, err, *calls)
		}
	}
	for _, kind := range []int{1, 2} {
		calls := talkFilesServer(t, roomRead(kind, ""), talkShareCreated)
		if _, err := talkFilesShareCall(t, talkFilesCore(t, true, talkFilesBound()...), talkFilesArgs, true); err != nil ||
			len(*calls) != 2 {
			t.Errorf("type %d: err = %v, calls = %+v", kind, err, *calls)
		}
	}
	calls := serve(t, func(*http.Request) (*http.Response, error) { return status(404), nil })
	if _, err := talkFilesShareCall(t, talkFilesCore(t, true, talkFilesBound()...), talkFilesArgs, true); err == nil || len(*calls) != 1 {
		t.Errorf("missing room: err = %v, calls = %+v", err, *calls)
	}
}

func TestTalkFilesShareReportsUnclearOutcomesWithoutRepeating(t *testing.T) {
	unclear := map[string]func() (*http.Response, error){
		"500":        func() (*http.Response, error) { return status(500), nil },
		"503":        func() (*http.Response, error) { return status(503), nil },
		"connection": func() (*http.Response, error) { return nil, errors.New("connection reset by peer") },
		"timeout":    func() (*http.Response, error) { return nil, timeoutError{} },
		"unreadable": func() (*http.Response, error) { return ocsResponse(http.StatusOK, "not json"), nil },
	}
	for name, answer := range unclear {
		calls := talkFilesServer(t, roomRead(2, ""), func(*http.Request) (*http.Response, error) { return answer() })
		_, err := talkFilesShareCall(t, talkFilesCore(t, true, talkFilesBound()...), talkFilesArgs, true)
		if err == nil || len(*calls) != 2 || !strings.Contains(err.Error(), "check shares.shares.list before repeating") ||
			strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("%s: err = %v, calls = %d", name, err, len(*calls))
		}
	}
	for _, code := range []int{400, 403, 404} {
		calls := talkFilesServer(t, roomRead(2, ""), func(*http.Request) (*http.Response, error) { return status(code), nil })
		_, err := talkFilesShareCall(t, talkFilesCore(t, true, talkFilesBound()...), talkFilesArgs, true)
		if err == nil || len(*calls) != 2 || strings.Contains(err.Error(), "may have been") || strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("%d: err = %v, calls = %d", code, err, len(*calls))
		}
	}
}

func TestTalkFilesShareNeverFollowsRedirects(t *testing.T) {
	for _, code := range []int{301, 302, 303, 307, 308} {
		for _, atRead := range []bool{true, false} {
			calls := serve(t, func(request *http.Request) (*http.Response, error) {
				if strings.Contains(request.URL.Path, "outside-canary") {
					t.Errorf("the redirect was followed: %s", request.URL)
				}
				if request.Method == http.MethodGet && !atRead {
					return ocsResponse(http.StatusOK, ocsData(roomRead(2, ""))), nil
				}
				return &http.Response{StatusCode: code, Header: http.Header{"Location": []string{mainInstance + redirectTarget}},
					Body: status(code).Body}, nil
			})
			_, err := talkFilesShareCall(t, talkFilesCore(t, true, talkFilesBound()...), talkFilesArgs, true)
			want := 2
			if atRead {
				want = 1
			}
			if err == nil || len(*calls) != want || strings.Contains(err.Error(), "outside-canary") ||
				strings.Contains(err.Error(), "may have been") || strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("%d read=%v: err = %v, calls = %d", code, atRead, err, len(*calls))
			}
		}
	}
}

func TestTalkFilesShareIsInNoProfile(t *testing.T) {
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if id == talkFilesShare.ID {
				t.Errorf("profile %s contains %s", profile.ID, id)
			}
		}
	}
}
