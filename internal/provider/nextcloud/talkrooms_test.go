package nextcloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	roomsPath = "/ocs/v2.php/apps/spreed/api/v4/room"
	roomPath  = roomsPath + "/" + boundRoom
)

func roomManager(t *testing.T, targets ...string) *application.Core {
	t.Helper()
	cfg := coreConfig()
	connection := cfg.Connections["reports"]
	connection.Target = ""
	connection.Targets = targets
	connection.Permissions = []config.Permission{config.PermissionRead, config.PermissionCreate,
		config.PermissionUpdate, config.PermissionDelete}
	connection.Tools = []string{talkRoomsCreate.ID, talkRoomsUpdate.ID, talkRoomsDelete.ID}
	cfg.Connections["reports"] = connection
	red := &redact.Redactor{}
	return application.New(registry(t), cfg, resolver(red), red)
}

func roomState(kind, role int, objectType string) string {
	raw, _ := json.Marshal(map[string]any{"token": boundRoom, "type": kind, "participantType": role, "objectType": objectType})
	return string(raw)
}

func roomArgs(t *testing.T, members map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(members)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func roomInvoke(t *testing.T, core *application.Core, operation, args string, confirmed bool) error {
	t.Helper()
	_, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: "reports", Arguments: json.RawMessage(args), Confirmed: confirmed,
	})
	return err
}

// roomServer answers the capability read, the read of the room, and every change with the given answers.
func roomServer(t *testing.T, state string, change func(*http.Request) (*http.Response, error)) *[]call {
	t.Helper()
	return serve(t, func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Path == "/ocs/v2.php/cloud/capabilities":
			return ocsResponse(http.StatusOK, ocsData(capabilitiesBody)), nil
		case request.Method == http.MethodGet && request.URL.Path == roomPath:
			return ocsResponse(http.StatusOK, ocsData(state)), nil
		}
		return change(request)
	})
}

func changed(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodPost {
		return ocsResponse(http.StatusCreated, ocsData(roomJSON(boundRoom, "New", 2))), nil
	}
	return ocsResponse(http.StatusOK, ocsData(`[]`)), nil
}

type roomCase struct {
	name, operation, args, method, path, read string
	form                                      url.Values
}

func roomCases(t *testing.T) []roomCase {
	return []roomCase{
		{"create group", talkRoomsCreate.ID, roomArgs(t, map[string]any{"kind": "group", "name": "Project"}),
			http.MethodPost, roomsPath, "/ocs/v2.php/cloud/capabilities", url.Values{"roomType": {"2"}, "roomName": {"Project"}}},
		{"create one-to-one", talkRoomsCreate.ID, roomArgs(t, map[string]any{"kind": "one-to-one", "invite": "bob.o'neil@x"}),
			http.MethodPost, roomsPath, "/ocs/v2.php/cloud/capabilities",
			url.Values{"roomType": {"1"}, "invite": {"bob.o'neil@x"}, "source": {"users"}}},
		{"rename", talkRoomsUpdate.ID, roomArgs(t, map[string]any{"token": boundRoom, "name": "Atlas"}),
			http.MethodPut, roomPath, roomPath, url.Values{"roomName": {"Atlas"}}},
		{"describe", talkRoomsUpdate.ID, roomArgs(t, map[string]any{"token": boundRoom, "description": "About"}),
			http.MethodPut, roomPath + "/description", roomPath, url.Values{"description": {"About"}}},
		{"delete", talkRoomsDelete.ID, roomArgs(t, map[string]any{"token": boundRoom}), http.MethodDelete, roomPath, roomPath, nil},
	}
}

func roomTargets(c roomCase) string {
	if strings.HasSuffix(c.operation, ".create") {
		return "talk"
	}
	return "talk/" + boundRoom
}

func TestTalkRoomsNeedConfirmationAndSendOneChangeAfterOneRead(t *testing.T) {
	for _, c := range roomCases(t) {
		t.Run(c.name, func(t *testing.T) {
			refuse(t)
			if err := roomInvoke(t, roomManager(t, roomTargets(c)), c.operation, c.args, false); err == nil ||
				!strings.Contains(err.Error(), "confirm") {
				t.Fatalf("ran without confirm: %v", err)
			}
			calls := roomServer(t, roomState(2, 2, ""), changed)
			if err := roomInvoke(t, roomManager(t, roomTargets(c)), c.operation, c.args, true); err != nil {
				t.Fatal(err)
			}
			if len(*calls) != 2 || (*calls)[0].method != http.MethodGet || (*calls)[0].url.Path != c.read {
				t.Fatalf("calls = %+v", *calls)
			}
			got := (*calls)[1]
			if got.method != c.method || got.url.Path != c.path {
				t.Errorf("request = %s %s", got.method, got.url.Path)
			}
			form, _ := url.ParseQuery(got.body)
			if len(form) != len(c.form) {
				t.Errorf("form = %v", form)
			}
			for name, want := range c.form {
				if form.Get(name) != want[0] {
					t.Errorf("form %s = %q", name, form.Get(name))
				}
			}
			if c.form == nil && got.body != "" {
				t.Errorf("body = %q", got.body)
			}
		})
	}
}

func TestTalkRoomsCreateReportsTheNormalisedRoom(t *testing.T) {
	roomServer(t, "", func(*http.Request) (*http.Response, error) {
		return ocsResponse(http.StatusOK, ocsData(roomJSON(boundRoom, "Bob", 1))), nil
	})
	out, err := roomManager(t, "talk").Invoke(context.Background(), application.InvokeRequest{
		Operation: talkRoomsCreate.ID, Connection: "reports", Confirmed: true,
		Arguments: json.RawMessage(roomArgs(t, map[string]any{"kind": "one-to-one", "invite": "bob"})),
	})
	var room Room
	if err != nil || json.Unmarshal(out.Result, &room) != nil || room.Token != boundRoom || room.Type != "one-to-one" ||
		strings.Contains(string(out.Result), "avatar-canary") {
		t.Errorf("result = %s, %v", out.Result, err)
	}
}

func TestTalkRoomsRefuseWithoutAChangeWhenTheRoomIsNotManageable(t *testing.T) {
	states := map[string]string{
		"member":       roomState(2, 3, ""),
		"guest":        roomState(2, 4, ""),
		"breakout":     roomState(2, 1, "room"),
		"event":        roomState(2, 1, "event"),
		"public":       roomState(3, 1, ""),
		"changelog":    roomState(4, 1, ""),
		"note to self": roomState(6, 1, ""),
		"other token":  strings.Replace(roomState(2, 1, ""), boundRoom, "othr5678", 1),
	}
	for name, state := range states {
		for _, c := range roomCases(t)[2:] {
			calls := roomServer(t, state, changed)
			err := roomInvoke(t, roomManager(t, "talk/"+boundRoom), c.operation, c.args, true)
			if err == nil || strings.Contains(err.Error(), boundRoom) {
				t.Errorf("%s %s: err = %v", name, c.name, err)
			}
			if len(*calls) != 1 || (*calls)[0].method != http.MethodGet {
				t.Errorf("%s %s: calls = %+v", name, c.name, *calls)
			}
		}
	}
	// A one-to-one conversation can be deleted but has no name or description of its own.
	calls := roomServer(t, roomState(1, 1, ""), changed)
	cases := roomCases(t)
	if err := roomInvoke(t, roomManager(t, "talk/"+boundRoom), cases[2].operation, cases[2].args, true); err == nil || len(*calls) != 1 {
		t.Errorf("update of a one-to-one: err = %v, calls = %d", err, len(*calls))
	}
	calls = roomServer(t, roomState(1, 1, ""), changed)
	if err := roomInvoke(t, roomManager(t, "talk/"+boundRoom), cases[4].operation, cases[4].args, true); err != nil || len(*calls) != 2 {
		t.Errorf("delete of a one-to-one: err = %v, calls = %d", err, len(*calls))
	}
}

func TestTalkRoomsReadFailureEndsWithoutAChange(t *testing.T) {
	for _, c := range roomCases(t)[2:] {
		calls := serve(t, func(request *http.Request) (*http.Response, error) {
			return status(404), nil
		})
		err := roomInvoke(t, roomManager(t, "talk/"+boundRoom), c.operation, c.args, true)
		var providerErr *provider.Error
		if !errors.As(err, &providerErr) || providerErr.Class != provider.ClassNotFound || strings.Contains(err.Error(), boundRoom) ||
			len(*calls) != 1 {
			t.Errorf("%s: err = %v, calls = %d", c.name, err, len(*calls))
		}
	}
}

func TestTalkRoomsClassifyChangeFailures(t *testing.T) {
	for _, c := range roomCases(t) {
		t.Run(c.name, func(t *testing.T) {
			answers := map[string]struct {
				answer    func(*http.Request) (*http.Response, error)
				class     provider.Class
				uncertain bool
			}{
				"429":  {func(*http.Request) (*http.Response, error) { return status(429), nil }, provider.ClassRateLimited, false},
				"500":  {func(*http.Request) (*http.Response, error) { return status(500), nil }, provider.ClassProviderError, true},
				"503":  {func(*http.Request) (*http.Response, error) { return status(503), nil }, provider.ClassUnreachable, true},
				"drop": {func(*http.Request) (*http.Response, error) { return nil, errors.New("connection reset") }, "", true},
				"body": {func(*http.Request) (*http.Response, error) {
					return ocsResponse(http.StatusOK, `{"nope":true}`), nil
				}, provider.ClassInvalidResponse, true},
				"403": {func(*http.Request) (*http.Response, error) { return status(403), nil }, provider.ClassPermission, false},
				"400": {func(*http.Request) (*http.Response, error) { return status(400), nil }, provider.ClassProviderError, false},
				"404": {func(*http.Request) (*http.Response, error) { return status(404), nil }, provider.ClassNotFound, false},
				"302": {func(*http.Request) (*http.Response, error) { return status(302), nil }, provider.ClassProviderError, false},
			}
			for name, a := range answers {
				calls := roomServer(t, roomState(2, 2, ""), a.answer)
				err := roomInvoke(t, roomManager(t, roomTargets(c)), c.operation, c.args, true)
				if err == nil {
					t.Fatalf("%s: no error", name)
				}
				text := err.Error()
				var providerErr *provider.Error
				if a.class != "" && (!errors.As(err, &providerErr) || providerErr.Class != a.class) {
					t.Errorf("%s: err = %v", name, err)
				}
				if has := strings.Contains(text, "may have been"); has != a.uncertain {
					t.Errorf("%s: hint present = %v in %q", name, has, text)
				}
				for _, leaked := range []string{bodyCanary, boundRoom, "Project"} {
					if strings.Contains(text, leaked) {
						t.Errorf("%s: leaked %q in %q", name, leaked, text)
					}
				}
				if len(*calls) != 2 {
					t.Errorf("%s: %d requests, want one read and one change", name, len(*calls))
				}
			}
		})
	}
}

func TestTalkRoomsRefuseUnusableArgumentsBeforeAnyIO(t *testing.T) {
	refuse(t)
	long := strings.Repeat("a", 256)
	cases := map[string]struct {
		operation string
		args      map[string]any
		targets   string
	}{
		"public kind":          {talkRoomsCreate.ID, map[string]any{"kind": "public", "name": "x"}, "talk"},
		"create bound only":    {talkRoomsCreate.ID, map[string]any{"kind": "group", "name": "x"}, "talk/" + boundRoom},
		"create folder only":   {talkRoomsCreate.ID, map[string]any{"kind": "group", "name": "x"}, "folder/Reports"},
		"group without name":   {talkRoomsCreate.ID, map[string]any{"kind": "group"}, "talk"},
		"group with user":      {talkRoomsCreate.ID, map[string]any{"kind": "group", "name": "x", "invite": "bob"}, "talk"},
		"group long name":      {talkRoomsCreate.ID, map[string]any{"kind": "group", "name": long}, "talk"},
		"group blank name":     {talkRoomsCreate.ID, map[string]any{"kind": "group", "name": "  "}, "talk"},
		"one-to-one no user":   {talkRoomsCreate.ID, map[string]any{"kind": "one-to-one"}, "talk"},
		"one-to-one with name": {talkRoomsCreate.ID, map[string]any{"kind": "one-to-one", "invite": "bob", "name": "x"}, "talk"},
		"user with slash":      {talkRoomsCreate.ID, map[string]any{"kind": "one-to-one", "invite": "bob/../x"}, "talk"},
		"user too long":        {talkRoomsCreate.ID, map[string]any{"kind": "one-to-one", "invite": strings.Repeat("b", 65)}, "talk"},
		"create with token":    {talkRoomsCreate.ID, map[string]any{"kind": "group", "name": "x", "token": boundRoom}, "talk"},
		"update both":          {talkRoomsUpdate.ID, map[string]any{"token": boundRoom, "name": "x", "description": "y"}, "talk/" + boundRoom},
		"update none":          {talkRoomsUpdate.ID, map[string]any{"token": boundRoom}, "talk/" + boundRoom},
		"update long name":     {talkRoomsUpdate.ID, map[string]any{"token": boundRoom, "name": long}, "talk/" + boundRoom},
		"update blank name":    {talkRoomsUpdate.ID, map[string]any{"token": boundRoom, "name": ""}, "talk/" + boundRoom},
		"update long text":     {talkRoomsUpdate.ID, map[string]any{"token": boundRoom, "description": strings.Repeat("d", 2001)}, "talk/" + boundRoom},
		"update unbound":       {talkRoomsUpdate.ID, map[string]any{"token": foreignRoom, "name": "x"}, "talk/" + boundRoom},
		"update no talk":       {talkRoomsUpdate.ID, map[string]any{"token": boundRoom, "name": "x"}, "account"},
		"delete unbound":       {talkRoomsDelete.ID, map[string]any{"token": foreignRoom}, "talk/" + boundRoom},
		"delete extra":         {talkRoomsDelete.ID, map[string]any{"token": boundRoom, "name": "x"}, "talk/" + boundRoom},
		"delete no talk":       {talkRoomsDelete.ID, map[string]any{"token": boundRoom}, "folder/Reports"},
	}
	for name, c := range cases {
		err := roomInvoke(t, roomManager(t, c.targets), c.operation, roomArgs(t, c.args), true)
		if err == nil || strings.Contains(err.Error(), foreignRoom) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// A long description at the limit and an empty one that clears it are accepted by the local checks.
	if !validRoomDescription(strings.Repeat("d", 2000)) || !validRoomDescription("") {
		t.Error("the description limit is 2000 characters")
	}
}

func TestTalkRoomsDescriptorsCarryTheirRisk(t *testing.T) {
	for _, d := range []capability.Descriptor{talkRoomsCreate, talkRoomsUpdate, talkRoomsDelete} {
		if d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld || d.Risk.DataSensitivity != talkSensitivity ||
			d.Group != "" && d.Group != groupTalk {
			t.Errorf("%s risk = %+v", d.ID, d.Risk)
		}
		if strings.Contains(string(d.InputSchema), "public") {
			t.Errorf("%s offers a public conversation", d.ID)
		}
	}
	if talkRoomsCreate.Risk.Effect != capability.EffectCreate || talkRoomsCreate.Risk.Idempotency != capability.IdempotencyNonIdempotent ||
		talkRoomsUpdate.Risk.Effect != capability.EffectUpdate || talkRoomsUpdate.Risk.Idempotency != capability.IdempotencyIdempotent ||
		talkRoomsDelete.Risk.Effect != capability.EffectDelete || !talkRoomsDelete.RequiresToolAllowList ||
		talkRoomsCreate.RequiresToolAllowList || talkRoomsUpdate.RequiresToolAllowList {
		t.Error("unexpected effect, idempotency, or tools list requirement")
	}
}
