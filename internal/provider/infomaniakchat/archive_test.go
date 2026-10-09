package infomaniakchat

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

const (
	chanE = "chane000000000000000000e5"
	chanF = "chanf000000000000000000f6"
)

const archivedAt = `,"delete_at":1735689700000`

// archiveFixture is a fake instance: chanA (public) and chanC (private) belong to teamA and are active,
// chanE (public) is archived in teamA, chanB belongs to teamB, chanD is a direct channel.
type archiveFixture struct {
	t        *testing.T
	listing  string
	mutate   func(*http.Request) (*http.Response, error)
	channels map[string]string
}

func newArchiveFixture(t *testing.T, mutate func(*http.Request) (*http.Response, error)) *archiveFixture {
	if mutate == nil {
		mutate = func(r *http.Request) (*http.Response, error) {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}
	return &archiveFixture{t: t, mutate: mutate, channels: map[string]string{
		chanA: channelWith(chanA, teamA, "O", ""), chanC: channelWith(chanC, teamA, "P", ""),
		chanE: channelWith(chanE, teamA, "O", archivedAt), chanB: channelWith(chanB, teamB, "O", archivedAt),
		chanD: channelWith(chanD, "", "D", ""),
	}}
}

func (f *archiveFixture) handle(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodGet {
		return f.mutate(r)
	}
	if r.URL.Path == "/api/v4/teams/"+teamA+"/channels/deleted" {
		return jsonResponse(200, f.listing), nil
	}
	for id, body := range f.channels {
		if r.URL.Path == "/api/v4/channels/"+id {
			return jsonResponse(200, body), nil
		}
	}
	f.t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
	return nil, nil
}

func (f *archiveFixture) env(calls *[]call) *environment {
	return newEnvironment(f.t, calls, f.handle)
}

func TestArchivedListKeepsOnlyReachableArchivedTeamChannels(t *testing.T) {
	f := newArchiveFixture(t, nil)
	f.listing = "[" + strings.Join([]string{
		channelWith(chanE, teamA, "O", archivedAt),
		channelWith(chanC, teamA, "P", archivedAt),
		channelWith(chanB, teamB, "O", archivedAt),
		channelWith(chanD, "", "D", archivedAt),
		channelWith(chanF, teamA, "G", archivedAt),
		channelWith(chanA, teamA, "O", ""),
	}, ",") + "]"
	var calls []call
	env := f.env(&calls)

	result, err := env.invoke(archivedChannelsList.ID, "team", `{"team_id":"`+teamA+`","page":2,"per_page":6}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	for _, id := range []string{chanE, chanC} {
		if !strings.Contains(result, id) {
			t.Fatalf("result = %s, want %s", result, id)
		}
	}
	for _, id := range []string{chanB, chanD, chanF, chanA} {
		if strings.Contains(result, id) {
			t.Fatalf("result = %s, want %s dropped", result, id)
		}
	}
	if !strings.Contains(result, `"count":2`) || !strings.Contains(result, `"has_more":true`) || len(calls) != 1 ||
		calls[0].query.Get("page") != "1" || calls[0].query.Get("per_page") != "6" {
		t.Fatalf("result = %s, calls = %+v, want one request for kChat page 2", result, calls)
	}

	// The channel allow-list narrows the listing to chanA, which is not archived here.
	result, err = env.invoke(archivedChannelsList.ID, "channel", `{"team_id":"`+teamA+`"}`)
	if err != nil || !strings.Contains(result, `"count":0`) {
		t.Fatalf("result = %s, %v, want an empty page", result, err)
	}

	calls, *env.reads = nil, 0
	if _, err := env.invoke(archivedChannelsList.ID, "team", `{"team_id":"`+teamB+`"}`); !isInvalidRequest(err) ||
		len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %+v, reads = %d, want a local refusal", err, calls, *env.reads)
	}
}

func TestArchiveNeedsToolListConfirmationAndSendsOneDelete(t *testing.T) {
	f := newArchiveFixture(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v4/channels/"+chanA {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(200, `{"status":"OK"}`), nil
	})
	var calls []call
	env := f.env(&calls)
	args := `{"channel_id":"` + chanA + `"}`
	if _, err := env.confirmed(channelsArchive.ID, "memberno", args); err == nil || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want a refusal without the tool in the tools list", err, calls)
	}
	if _, err := env.invoke(channelsArchive.ID, "archiver", args); !isConfirmationRequired(err) || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want confirmation-required before any request", err, calls)
	}
	result, err := env.confirmed(channelsArchive.ID, "archiver", args)
	if err != nil || !strings.Contains(result, `"archived":true`) {
		t.Fatalf("result = %s, %v", result, err)
	}
	if len(calls) != 2 || calls[0].method != http.MethodGet || calls[1].method != http.MethodDelete || calls[1].body != "" {
		t.Fatalf("calls = %+v, want one read and then exactly one DELETE", calls)
	}
}

func TestArchiveRefusesForeignAndInactiveTargetsBeforeTheDelete(t *testing.T) {
	f := newArchiveFixture(t, nil)
	var calls []call
	env := f.env(&calls)

	// Outside the allow-list: refused before the secret is read.
	if _, err := env.confirmed(channelsArchive.ID, "archivech", `{"channel_id":"`+chanC+`"}`); !isInvalidRequest(err) ||
		len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %+v, reads = %d, want a local refusal", err, calls, *env.reads)
	}
	if _, err := env.confirmed(channelsArchive.ID, "archiver", `{"channel_id":"../`+chanA+`"}`); err == nil || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want a refusal of a malformed identifier", err, calls)
	}
	// A foreign team's channel, a direct channel, and an already archived channel are refused by the live read.
	for _, id := range []string{chanB, chanD, chanE} {
		calls = nil
		_, err := env.confirmed(channelsArchive.ID, "archiver", `{"channel_id":"`+id+`"}`)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), id) || strings.Contains(err.Error(), teamB) {
			t.Fatalf("%s: err = %v", id, err)
		}
		if len(calls) != 1 || calls[0].method != http.MethodGet {
			t.Fatalf("%s: calls = %+v, want only the channel read", id, calls)
		}
	}
}

func TestRestoreAcceptsOnlyAnArchivedChannelAndSendsOnePost(t *testing.T) {
	f := newArchiveFixture(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v4/channels/"+chanE+"/restore" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(200, channelWith(chanE, teamA, "O", "")), nil
	})
	var calls []call
	env := f.env(&calls)
	args := `{"channel_id":"` + chanE + `"}`
	if _, err := env.confirmed(channelsRestore.ID, "memberno", args); err == nil || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want a refusal without the tool in the tools list", err, calls)
	}
	if _, err := env.invoke(channelsRestore.ID, "restorer", args); !isConfirmationRequired(err) || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want confirmation-required", err, calls)
	}
	result, err := env.confirmed(channelsRestore.ID, "restorer", args)
	if err != nil || !strings.Contains(result, chanE) {
		t.Fatalf("result = %s, %v", result, err)
	}
	if len(calls) != 2 || calls[1].method != http.MethodPost || calls[1].body != "" {
		t.Fatalf("calls = %+v, want one read and exactly one POST", calls)
	}
	// An active, a foreign, and a direct channel are refused after the live read.
	for _, id := range []string{chanA, chanB, chanD} {
		calls = nil
		if _, err := env.confirmed(channelsRestore.ID, "restorer", `{"channel_id":"`+id+`"}`); !isInvalidRequest(err) ||
			len(calls) != 1 {
			t.Fatalf("%s: err = %v, calls = %+v", id, err, calls)
		}
	}
}

func TestPrivacyAcceptsOnlyOAndPAndNeedsTheToolList(t *testing.T) {
	f := newArchiveFixture(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v4/channels/"+chanA+"/privacy" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(200, channelWith(chanA, teamA, "P", "")), nil
	})
	var calls []call
	env := f.env(&calls)
	for _, privacy := range []string{"D", "G", "o", "", "public"} {
		args := `{"channel_id":"` + chanA + `","privacy":"` + privacy + `"}`
		if _, err := env.confirmed(channelsPrivacy.ID, "privater", args); err == nil {
			t.Fatalf("privacy %q was accepted", privacy)
		}
	}
	args := `{"channel_id":"` + chanA + `","privacy":"P"}`
	if _, err := env.confirmed(channelsPrivacy.ID, "memberno", args); err == nil {
		t.Fatal("privacy was reachable without the tool in the tools list")
	}
	if _, err := env.invoke(channelsPrivacy.ID, "privater", args); !isConfirmationRequired(err) {
		t.Fatalf("err = %v, want confirmation-required", err)
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d, want local refusals", calls, *env.reads)
	}
	result, err := env.confirmed(channelsPrivacy.ID, "privater", args)
	if err != nil || !strings.Contains(result, `"type":"P"`) {
		t.Fatalf("result = %s, %v", result, err)
	}
	if len(calls) != 2 || calls[1].method != http.MethodPut || calls[1].body != `{"privacy":"P"}` {
		t.Fatalf("calls = %+v, want one read and exactly one PUT with the typed body", calls)
	}
	// An archived, a foreign, and a direct channel are refused after the live read.
	for _, id := range []string{chanE, chanB, chanD} {
		calls = nil
		if _, err := env.confirmed(channelsPrivacy.ID, "privater", `{"channel_id":"`+id+`","privacy":"O"}`); !isInvalidRequest(err) ||
			len(calls) != 1 {
			t.Fatalf("%s: err = %v, calls = %+v", id, err, calls)
		}
	}
}

func TestChannelStateChangesAreNeverRetriedAfterAnUnclearResult(t *testing.T) {
	for _, c := range []struct {
		operation, connection, arguments, method, hint string
	}{
		{channelsArchive.ID, "archiver", `{"channel_id":"` + chanA + `"}`, http.MethodDelete, "may have been archived"},
		{channelsRestore.ID, "restorer", `{"channel_id":"` + chanE + `"}`, http.MethodPost, "may have been restored"},
		{channelsPrivacy.ID, "privater", `{"channel_id":"` + chanA + `","privacy":"P"}`, http.MethodPut,
			"visibility may have been changed"},
	} {
		for name, fail := range map[string]func(*http.Request) (*http.Response, error){
			"5xx": func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusInternalServerError, `{"message":"`+messageCanary+`"}`), nil
			},
			"timeout": func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded },
			"unreadable": func(*http.Request) (*http.Response, error) {
				return jsonResponse(200, `{not json`), nil
			},
		} {
			if c.method == http.MethodDelete && name == "unreadable" {
				continue
			}
			var calls []call
			env := newArchiveFixture(t, fail).env(&calls)
			_, err := env.confirmed(c.operation, c.connection, c.arguments)
			if err == nil || !strings.Contains(err.Error(), c.hint) || strings.Contains(err.Error(), messageCanary) {
				t.Fatalf("%s %s: err = %v", c.operation, name, err)
			}
			if countMethod(calls, c.method) != 1 || calls[len(calls)-1].method != c.method {
				t.Fatalf("%s %s: calls = %+v, want exactly one change request", c.operation, name, calls)
			}
		}
	}
}

func TestChannelStateChangeRejectsAnAnswerForAnotherChannel(t *testing.T) {
	f := newArchiveFixture(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, channelWith(chanB, teamB, "O", "")), nil
	})
	var calls []call
	env := f.env(&calls)
	_, err := env.confirmed(channelsRestore.ID, "restorer", `{"channel_id":"`+chanE+`"}`)
	if err == nil || classOf(err) != "invalid-provider-response" || !strings.Contains(err.Error(), "may have been restored") {
		t.Fatalf("err = %v (%s)", err, classOf(err))
	}
}
