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
	participantsPath = "/ocs/v2.php/apps/spreed/api/v4/room/" + boundRoom + "/participants"
	listBody         = `[{"attendeeId":1,"actorType":"users","actorId":"carol","participantType":1},` +
		`{"attendeeId":2,"actorType":"users","actorId":"alice","participantType":3},` +
		`{"attendeeId":3,"actorType":"users","actorId":"bob","participantType":3},` +
		`{"attendeeId":4,"actorType":"groups","actorId":"staff","participantType":3},` +
		`{"attendeeId":5,"actorType":"users","actorId":"dave","participantType":2}]`
)

// participantCore is a core whose connection may change Talk and names the participant tools.
func participantCore(t *testing.T, targets ...string) *application.Core {
	t.Helper()
	cfg := coreConfig()
	connection := cfg.Connections["reports"]
	connection.Target = ""
	connection.Targets = targets
	connection.Permissions = []config.Permission{config.PermissionRead, config.PermissionCreate,
		config.PermissionUpdate, config.PermissionDelete}
	connection.Tools = []string{talkParticipantsAdd.ID, talkParticipantsRemove.ID, talkParticipantsModerator.ID}
	cfg.Connections["reports"] = connection
	red := &redact.Redactor{}
	return application.New(registry(t), cfg, resolver(red), red)
}

// participantServer answers the capability read and the participant list, and every change with the answer.
func participantServer(t *testing.T, answer func(*http.Request) (*http.Response, error)) *[]call {
	t.Helper()
	return serve(t, func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Path == "/ocs/v2.php/cloud/capabilities":
			return ocsResponse(http.StatusOK, ocsData(capabilitiesBody)), nil
		case request.Method == http.MethodGet && request.URL.Path == participantsPath:
			return ocsResponse(http.StatusOK, ocsData(listBody)), nil
		}
		return answer(request)
	})
}

func participantOK(*http.Request) (*http.Response, error) {
	return ocsResponse(http.StatusOK, ocsData(`[]`)), nil
}

type participantCase struct {
	name, operation, extra, method, path string
	form                                 url.Values
	read                                 string // the one read before the change
}

func participantCases() []participantCase {
	const room = "/ocs/v2.php/apps/spreed/api/v4/room/" + boundRoom
	return []participantCase{
		{"add user", "nextcloud.talkparticipants.add", `,"participant":"erin","source":"users"`, http.MethodPost,
			room + "/participants", url.Values{"newParticipant": {"erin"}, "source": {"users"}}, "/ocs/v2.php/cloud/capabilities"},
		{"add group", "nextcloud.talkparticipants.add", `,"participant":"Team A","source":"groups"`, http.MethodPost,
			room + "/participants", url.Values{"newParticipant": {"Team A"}, "source": {"groups"}}, "/ocs/v2.php/cloud/capabilities"},
		{"remove", "nextcloud.talkparticipants.remove", `,"attendee_id":"3"`, http.MethodDelete, room + "/attendees",
			url.Values{"attendeeId": {"3"}}, participantsPath},
		{"promote", "nextcloud.talkparticipants.moderator", `,"attendee_id":"3","moderator":true`, http.MethodPost,
			room + "/moderators", url.Values{"attendeeId": {"3"}}, participantsPath},
		{"demote", "nextcloud.talkparticipants.moderator", `,"attendee_id":"5","moderator":false`, http.MethodDelete,
			room + "/moderators", url.Values{"attendeeId": {"5"}}, participantsPath},
	}
}

func participantInvoke(t *testing.T, operation, args string, confirmed bool) error {
	t.Helper()
	_, err := participantCore(t, "talk/"+boundRoom).Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: "reports", Arguments: json.RawMessage(args), Confirmed: confirmed,
	})
	return err
}

func TestTalkParticipantChangesNeedConfirmationAndSendOneRequest(t *testing.T) {
	for _, c := range participantCases() {
		t.Run(c.name, func(t *testing.T) {
			refuse(t)
			if err := participantInvoke(t, c.operation, talkArgs(boundRoom, c.extra), false); err == nil ||
				!strings.Contains(err.Error(), "confirm") {
				t.Fatal("ran without confirm")
			}
			calls := participantServer(t, participantOK)
			if err := participantInvoke(t, c.operation, talkArgs(boundRoom, c.extra), true); err != nil {
				t.Fatal(err)
			}
			if len(*calls) != 2 || (*calls)[0].url.Path != c.read || (*calls)[0].method != http.MethodGet {
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
		})
	}
}

func TestTalkParticipantChangesClassifyFailures(t *testing.T) {
	for _, c := range participantCases() {
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
				"time": {func(*http.Request) (*http.Response, error) { return nil, timeoutError{} }, "", true},
				"body": {func(*http.Request) (*http.Response, error) {
					return ocsResponse(http.StatusOK, `{"nope":true}`), nil
				}, provider.ClassInvalidResponse, true},
				"403": {func(*http.Request) (*http.Response, error) { return status(403), nil }, provider.ClassPermission, false},
				"400": {func(*http.Request) (*http.Response, error) { return status(400), nil }, provider.ClassProviderError, false},
				"404": {func(*http.Request) (*http.Response, error) { return status(404), nil }, provider.ClassNotFound, false},
				"302": {func(*http.Request) (*http.Response, error) { return status(302), nil }, provider.ClassProviderError, false},
			}
			for name, a := range answers {
				calls := participantServer(t, a.answer)
				err := participantInvoke(t, c.operation, talkArgs(boundRoom, c.extra), true)
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
				if a.uncertain && !strings.Contains(text, "talkparticipants.list") {
					t.Errorf("%s: no hint to the list in %q", name, text)
				}
				for _, leaked := range []string{bodyCanary, boundRoom} {
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

func TestTalkParticipantChangesRefuseWithoutAMutation(t *testing.T) {
	cases := map[string]struct{ operation, extra string }{
		"foreign attendee":      {"nextcloud.talkparticipants.remove", `,"attendee_id":"99"`},
		"foreign for moderator": {"nextcloud.talkparticipants.moderator", `,"attendee_id":"99","moderator":true`},
		"remove self":           {"nextcloud.talkparticipants.remove", `,"attendee_id":"2"`},
		"remove owner":          {"nextcloud.talkparticipants.remove", `,"attendee_id":"1"`},
		"moderator owner":       {"nextcloud.talkparticipants.moderator", `,"attendee_id":"1","moderator":false`},
		"moderator self":        {"nextcloud.talkparticipants.moderator", `,"attendee_id":"2","moderator":true`},
		"moderator group":       {"nextcloud.talkparticipants.moderator", `,"attendee_id":"4","moderator":true`},
	}
	for name, c := range cases {
		calls := participantServer(t, func(r *http.Request) (*http.Response, error) {
			t.Errorf("%s: a change was sent: %s %s", name, r.Method, r.URL.Path)
			return participantOK(r)
		})
		err := participantInvoke(t, c.operation, talkArgs(boundRoom, c.extra), true)
		if err == nil || len(*calls) != 1 || (*calls)[0].url.Path != participantsPath {
			t.Errorf("%s: err = %v, calls = %+v", name, err, *calls)
			continue
		}
		for _, leaked := range []string{boundRoom, "99", "carol", "bob"} {
			if strings.Contains(err.Error(), leaked) {
				t.Errorf("%s: leaked %q in %q", name, leaked, err)
			}
		}
	}
}

func TestTalkParticipantListNotFound(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) { return status(404), nil })
	err := participantInvoke(t, "nextcloud.talkparticipants.remove", talkArgs(boundRoom, `,"attendee_id":"3"`), true)
	var providerErr *provider.Error
	if !errors.As(err, &providerErr) || providerErr.Class != provider.ClassNotFound || providerErr.Message != messageTalkNoRoom {
		t.Errorf("err = %v", err)
	}
}

func TestTalkParticipantChangesRefuseUnusableArgumentsBeforeAnyIO(t *testing.T) {
	refuse(t)
	cases := map[string][2]string{
		"unbound room":      {"nextcloud.talkparticipants.add", talkArgs(foreignRoom, `,"participant":"erin","source":"users"`)},
		"source email":      {"nextcloud.talkparticipants.add", talkArgs(boundRoom, `,"participant":"erin","source":"emails"`)},
		"source federated":  {"nextcloud.talkparticipants.add", talkArgs(boundRoom, `,"participant":"erin","source":"federated_users"`)},
		"no source":         {"nextcloud.talkparticipants.add", talkArgs(boundRoom, `,"participant":"erin"`)},
		"user with space":   {"nextcloud.talkparticipants.add", talkArgs(boundRoom, `,"participant":"er in","source":"users"`)},
		"user with slash":   {"nextcloud.talkparticipants.add", talkArgs(boundRoom, `,"participant":"a/b","source":"users"`)},
		"mail as user":      {"nextcloud.talkparticipants.add", talkArgs(boundRoom, `,"participant":"a b@c","source":"users"`)},
		"group trailing":    {"nextcloud.talkparticipants.add", talkArgs(boundRoom, `,"participant":"Team ","source":"groups"`)},
		"group leading":     {"nextcloud.talkparticipants.add", talkArgs(boundRoom, `,"participant":" Team","source":"groups"`)},
		"participant long":  {"nextcloud.talkparticipants.add", talkArgs(boundRoom, `,"participant":"`+strings.Repeat("a", 65)+`","source":"users"`)},
		"participant empty": {"nextcloud.talkparticipants.add", talkArgs(boundRoom, `,"participant":"","source":"groups"`)},
		"remove id letters": {"nextcloud.talkparticipants.remove", talkArgs(boundRoom, `,"attendee_id":"3x"`)},
		"remove id empty":   {"nextcloud.talkparticipants.remove", talkArgs(boundRoom, `,"attendee_id":""`)},
		"remove id long":    {"nextcloud.talkparticipants.remove", talkArgs(boundRoom, `,"attendee_id":"`+strings.Repeat("1", 19)+`"`)},
		"remove unbound":    {"nextcloud.talkparticipants.remove", talkArgs(foreignRoom, `,"attendee_id":"3"`)},
		"moderator id":      {"nextcloud.talkparticipants.moderator", talkArgs(boundRoom, `,"attendee_id":"../3","moderator":true`)},
		"moderator missing": {"nextcloud.talkparticipants.moderator", talkArgs(boundRoom, `,"attendee_id":"3"`)},
	}
	for name, c := range cases {
		err := participantInvoke(t, c[0], c[1], true)
		if err == nil || strings.Contains(err.Error(), foreignRoom) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	for _, targets := range [][]string{{"folder/Reports"}, {"account"}} {
		_, err := participantCore(t, targets...).Invoke(context.Background(), application.InvokeRequest{
			Operation: "nextcloud.talkparticipants.remove", Connection: "reports", Confirmed: true,
			Arguments: json.RawMessage(talkArgs(boundRoom, `,"attendee_id":"3"`)),
		})
		if err == nil || strings.Contains(err.Error(), boundRoom) {
			t.Errorf("%v: err = %v", targets, err)
		}
	}
}

func TestTalkAddNeedsItsCapabilityFeature(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		return ocsResponse(http.StatusOK, ocsData(`{"capabilities":{"spreed":{"features":["chat-v2"]}}}`)), nil
	})
	if err := participantInvoke(t, "nextcloud.talkparticipants.add", talkArgs(boundRoom, `,"participant":"erin","source":"users"`), true); err == nil ||
		len(*calls) != 1 {
		t.Errorf("err = %v, calls = %d", err, len(*calls))
	}
}

func TestTalkParticipantToolsHaveTheirRisk(t *testing.T) {
	wantList := map[string]bool{talkParticipantsAdd.ID: false, talkParticipantsRemove.ID: true, talkParticipantsModerator.ID: true}
	found := 0
	for _, got := range registry(t).Provider(Provider) {
		allowlist, ok := wantList[got.ID]
		if !ok {
			continue
		}
		found++
		if got.RequiresToolAllowList != allowlist || got.Group != groupTalk || got.Risk.Confirmation != capability.ConfirmationRequired ||
			got.Risk.DataSensitivity != talkSensitivity || !got.Risk.OpenWorld || got.Risk.Idempotency == "" {
			t.Errorf("%s = %+v", got.ID, got)
		}
	}
	if found != 3 {
		t.Errorf("found %d tools", found)
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, p := range metadata.Profiles {
		for _, id := range p.Tools {
			if _, ok := wantList[id]; ok {
				t.Errorf("profile %s contains %s", p.ID, id)
			}
		}
	}
	if talkParticipantsRemove.Risk.Effect != capability.EffectDelete || talkParticipantsAdd.Risk.Effect != capability.EffectUpdate ||
		talkParticipantsModerator.Risk.Effect != capability.EffectUpdate {
		t.Error("unexpected effects")
	}
	if strings.Contains(string(talkParticipantsAdd.InputSchema), "emails") || !strings.Contains(string(talkParticipantsAdd.InputSchema), `"enum":["users","groups"]`) {
		t.Errorf("schema = %s", talkParticipantsAdd.InputSchema)
	}
}
