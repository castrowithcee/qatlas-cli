package nextcloud

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// collectionsConnection binds the targets of the reports connection and excludes entries from them.
func collectionsConnection(targets []string, excludes ...string) *config.Resolved {
	r := resolvedConnection("reports", "cloud-reader", aliceUserEnv, aliceTokenEnv, mainInstance, "")
	r.Targets, r.Exclude = targets, excludes
	return r
}

// exampleArguments returns the example arguments of a tool with one argument replaced.
func exampleArguments(t *testing.T, id, key, value string) (handlerFunc, string) {
	t.Helper()
	descriptor, handler, ok := registry(t).Lookup(id)
	if !ok || len(descriptor.Examples) == 0 {
		t.Fatalf("no tool or example for %s", id)
	}
	args := map[string]any{}
	if err := json.Unmarshal(descriptor.Examples[0].Arguments, &args); err != nil {
		t.Fatal(err)
	}
	if _, ok := args[key]; !ok {
		t.Fatalf("%s has no argument %s", id, key)
	}
	args[key] = value
	raw, _ := json.Marshal(args)
	return handler, string(raw)
}

func wantDeckUnbound(t *testing.T, err error) {
	t.Helper()
	if perr := providerFailure(t, err); perr.Class != provider.ClassPermission || perr.Message != messageDeckNotBound {
		t.Errorf("err = %+v, want the answer for an unbound board", perr)
	}
}

// toolsWithArgument lists the tools of a group whose example names the argument; the create tools of the
// collections take none.
func toolsWithArgument(t *testing.T, group, key string) []string {
	t.Helper()
	var ids []string
	for _, d := range registry(t).Provider(Provider) {
		if strings.Contains(d.ID, group) && len(d.Examples) > 0 && strings.Contains(string(d.Examples[0].Arguments), `"`+key+`"`) {
			ids = append(ids, d.ID)
		}
	}
	if len(ids) < 5 {
		t.Fatalf("only %d tools take %s", len(ids), key)
	}
	return ids
}

func TestExcludedCalendarToolsAnswerAsMissingBeforeSecretsAndRequests(t *testing.T) {
	for _, id := range toolsWithArgument(t, ".calendar.", "calendar") {
		for _, spelling := range []string{"work", "WORK", "Work", "wörk", "WÖRK"} {
			t.Run(id+" "+spelling, func(t *testing.T) {
				refuse(t)
				handler, args := exampleArguments(t, id, "calendar", spelling)
				r := collectionsConnection([]string{"calendar"}, "calendar/work", "calendar/wörk")
				_, touched, err := callTool(handler, r, args)
				wantMissing(t, err)
				if touched {
					t.Error("a secret was read")
				}
			})
		}
	}
}

func TestExcludedAddressbookToolsAnswerAsMissingBeforeSecretsAndRequests(t *testing.T) {
	for _, id := range toolsWithArgument(t, ".contacts.", "addressbook") {
		for _, spelling := range []string{"clients", "CLIENTS", "Clients"} {
			t.Run(id+" "+spelling, func(t *testing.T) {
				refuse(t)
				handler, args := exampleArguments(t, id, "addressbook", spelling)
				r := collectionsConnection([]string{"addressbook"}, "addressbook/clients")
				_, touched, err := callTool(handler, r, args)
				wantMissing(t, err)
				if touched {
					t.Error("a secret was read")
				}
			})
		}
	}
}

func TestExcludedDeckToolsAnswerAsUnboundBoardBeforeSecretsAndRequests(t *testing.T) {
	for _, id := range toolsWithArgument(t, ".deck.", "board_id") {
		for _, spelling := range []string{"7", "07", "0007"} {
			t.Run(id+" "+spelling, func(t *testing.T) {
				refuse(t)
				handler, args := exampleArguments(t, id, "board_id", spelling)
				r := collectionsConnection([]string{"deck"}, "deck/7")
				_, touched, err := callTool(handler, r, args)
				wantDeckUnbound(t, err)
				if touched {
					t.Error("a secret was read")
				}
			})
		}
	}
}

func TestExclusionsOfOtherObjectsStayReachable(t *testing.T) {
	for _, c := range []struct {
		id, key, value, target, exclude string
	}{
		{"nextcloud.calendar.events.get", "calendar", "workshop", "calendar", "calendar/work"},
		{"nextcloud.contacts.contacts.get", "addressbook", "clients2", "addressbook", "addressbook/clients"},
		{"nextcloud.deck.boards.get", "board_id", "70", "deck", "deck/7"},
	} {
		calls := serve(t, func(*http.Request) (*http.Response, error) { return status(http.StatusNotFound), nil })
		handler, args := exampleArguments(t, c.id, c.key, c.value)
		callTool(handler, collectionsConnection([]string{c.target}, c.exclude), args)
		if len(*calls) != 1 {
			t.Errorf("%s: calls = %d, want the request to go out", c.id, len(*calls))
		}
	}
}

func TestCalendarListsLeaveOutExcludedCalendars(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, homeListing()), nil
	})
	raw, _, err := callTool(invokeCalendarsList, collectionsConnection([]string{"calendar"}, "calendar/PERSONAL"), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var listed CalendarsResult
	if err := json.Unmarshal([]byte(mustJSON(t, raw)), &listed); err != nil || listed.Count != 1 ||
		listed.Calendars[0].ID != "team_shared_by_bob" || strings.Contains(mustJSON(t, raw), "Personal") {
		t.Errorf("result = %s, %v", mustJSON(t, raw), err)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestAddressbookListLeavesOutExcludedBooks(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, bookListing()), nil
	})
	raw, _, err := callTool(invokeAddressbooksList, collectionsConnection([]string{"addressbook"}, "addressbook/Contacts"), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	out := mustJSON(t, raw)
	if !strings.Contains(out, `"count":1`) || !strings.Contains(out, `"team"`) || strings.Contains(out, "Contacts") {
		t.Errorf("result = %s", out)
	}
}

func TestCalendarTrashListLeavesOutExcludedCalendars(t *testing.T) {
	trashListFixture(t)
	raw, _, err := callTool(invokeCalendarTrashList, collectionsConnection([]string{"calendar"}, "calendar/Projects"), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	got := mustJSON(t, raw)
	if !strings.Contains(got, `"count":1`) || !strings.Contains(got, "w.ics-d3") || strings.Contains(got, "projects") {
		t.Errorf("result = %s", got)
	}
}

func TestCalendarTrashChangeOfAnExcludedCalendarIsRefusedBeforeAMutation(t *testing.T) {
	for _, op := range []string{"restore", "delete"} {
		calls := serve(t, func(r *http.Request) (*http.Response, error) {
			if r.Method != methodPropfind {
				t.Errorf("%s: a mutation was sent", op)
			}
			return xmlResponse(http.StatusMultiStatus, calMultistatus(
				trashNode(calTrashObjects+"a.ics-d1", "Projects", typeEvent, "1767312000"))), nil
		})
		handler := invokeCalendarTrashRestore
		if op == "delete" {
			handler = invokeCalendarTrashDelete
		}
		_, _, err := callTool(handler, collectionsConnection([]string{"calendar"}, "calendar/projects"), `{"id":"a.ics-d1"}`)
		if err == nil || !strings.Contains(err.Error(), messageTrashObjectNone) || len(*calls) != 1 {
			t.Errorf("%s: err = %v, calls = %d", op, err, len(*calls))
		}
	}
}

func deckBoards() string {
	return `[` + board("7", "Secret") + `,` + board("8", "Open") + `]`
}

func TestDeckBoardListLeavesOutExcludedBoardsWithAndWithoutDetails(t *testing.T) {
	for _, args := range []string{`{}`, `{"details":true}`} {
		for _, spelling := range []string{"7", "07"} {
			serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, deckBoards()), nil })
			raw, _, err := callTool(invokeDeckBoardsList, collectionsConnection([]string{"deck"}, "deck/"+spelling), args)
			if err != nil {
				t.Fatal(err)
			}
			out := mustJSON(t, raw)
			if !strings.Contains(out, `"count":1`) || !strings.Contains(out, "Open") || strings.Contains(out, "Secret") {
				t.Errorf("%s %s: result = %s", args, spelling, out)
			}
		}
	}
}

func TestDeckBoardListWithExclusionsSendsNoPreconditionAndReportsNoTag(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		response := jsonResponse(http.StatusOK, deckBoards())
		response.Header.Set("ETag", `"covers-every-board"`)
		return response, nil
	})
	raw, _, err := callTool(invokeDeckBoardsList, collectionsConnection([]string{"deck"}, "deck/7"),
		`{"etag":"\"abc\"","modified_since":"2026-01-01T00:00:00Z"}`)
	if err != nil {
		t.Fatal(err)
	}
	header := (*calls)[0].header
	if header.Get("If-None-Match") != "" || header.Get("If-Modified-Since") != "" || strings.Contains(mustJSON(t, raw), "etag") {
		t.Errorf("header = %v, result = %s", header, mustJSON(t, raw))
	}
}

func TestExclusionsDoNotLimitTheCreateTools(t *testing.T) {
	for _, c := range []struct {
		targets, exclude string
		tool             string
	}{
		{"calendar", "calendar/work", "nextcloud.calendar.calendars.create"},
		{"addressbook", "addressbook/clients", "nextcloud.contacts.address-books.create"},
		{"deck", "deck/7", "nextcloud.deck.boards.create"},
	} {
		with, _ := reaches(c.tool, []string{c.targets})
		if !with {
			t.Errorf("%s is not offered under the kind alone", c.tool)
		}
		s, err := parseScopeExcluding([]string{c.targets}, []string{c.exclude})
		if err != nil || !s.calendars.all && !s.addressbooks.all && !s.decks.all {
			t.Errorf("%s: scope = %+v, %v", c.tool, s, err)
		}
	}
}
