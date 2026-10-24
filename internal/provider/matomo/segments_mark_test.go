package matomo

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func markAnswers() answers {
	return answers{
		"SegmentEditor.delete": func(call) *http.Response { return ok(`{"result":"success","message":"ok"}`) },
		"SegmentEditor.star": func(call) *http.Response {
			return ok(`{"result":true,"starred":1,"starred_by":"admin"}`)
		},
		"SegmentEditor.unstar": func(call) *http.Response { return ok(`{"starred":0,"result":true}`) },
	}
}

func TestSegmentsDeleteReadsOnceThenDeletes(t *testing.T) {
	var calls []call
	env := newSegmentsEnvironment(t, &calls, segmentToolAnswers(markAnswers()).handler(t))
	if _, err := env.change("matomo.segments.delete", "seg-two", `{"idSegment":2}`); err == nil {
		t.Fatal("a connection with an effects list offered segments.delete")
	}
	if _, err := env.invoke("matomo.segments.delete", "seg-deleter", `{"idSegment":2}`); err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Fatalf("without confirm: err = %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("refused calls caused %d requests", len(calls))
	}
	result, err := env.change("matomo.segments.delete", "seg-deleter", `{"idSegment":2}`)
	if err != nil || result != `{"deleted":true,"idSegment":2,"name":"Site one"}` {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := methods(calls); got != "API.getMatomoVersion API.isPluginActivated SegmentEditor.get SegmentEditor.delete" {
		t.Errorf("requests = %s", got)
	}
	if sent := calls[len(calls)-1]; sent.form.Get("idSegment") != "2" {
		t.Errorf("request = %v", sent.form)
	}
}

func TestSegmentMarksReadOnceThenChange(t *testing.T) {
	var calls []call
	env := newSegmentsEnvironment(t, &calls, segmentToolAnswers(markAnswers()).handler(t))
	result, err := env.change("matomo.segments.star", "seg-deleter", `{"idSegment":2}`)
	if err != nil || result != `{"idSegment":2,"starred":true,"starred_by":"admin"}` {
		t.Fatalf("star: result = %s, err = %v", result, err)
	}
	if got := methods(calls); got != "API.getMatomoVersion API.isPluginActivated SegmentEditor.get SegmentEditor.star" {
		t.Errorf("requests = %s", got)
	}
	result, err = env.change("matomo.segments.unstar", "seg-deleter", `{"idSegment":2}`)
	if err != nil || result != `{"idSegment":2,"starred":false}` {
		t.Fatalf("unstar: result = %s, err = %v", result, err)
	}
	if _, err := env.invoke("matomo.segments.star", "seg-deleter", `{"idSegment":2}`); err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Errorf("without confirm: err = %v", err)
	}
}

func TestSegmentMarksAndDeleteRefuseSegmentsOutsideTheTargets(t *testing.T) {
	for _, tool := range []string{"delete", "star", "unstar"} {
		var calls []call
		env := newSegmentsEnvironment(t, &calls, segmentToolAnswers(markAnswers()).handler(t))
		for name, args := range map[string]string{
			"missing":       `{"idSegment":99}`,
			"third website": `{"idSegment":4}`,
			"all websites":  `{"idSegment":1}`,
			"other segment": `{"idSegment":8}`,
			"no ID":         `{}`,
			"extra":         `{"idSegment":2,"idSite":1}`,
		} {
			calls = nil
			_, err := env.change("matomo.segments."+tool, "seg-deleter", args)
			if err == nil {
				t.Errorf("%s %s: accepted", tool, name)
			}
			if countMethod(calls, "SegmentEditor."+tool) != 0 {
				t.Errorf("%s %s: the change was sent", tool, name)
			}
			if err != nil && (strings.Contains(err.Error(), "site/") || strings.Contains(err.Error(), "Site three")) {
				t.Errorf("%s %s: err = %v quotes a value", tool, name, err)
			}
		}
		calls = nil
		if _, err := env.change("matomo.segments."+tool, "seg-deleter", `{"idSegment":99}`); classOf(err) != provider.ClassNotFound {
			t.Errorf("%s: err = %v, want not-found", tool, err)
		}
	}
}

func TestSegmentMarkAndDeleteFailuresAreNotRepeated(t *testing.T) {
	for _, tool := range []string{"delete", "star", "unstar"} {
		var calls []call
		env := newSegmentsEnvironment(t, &calls, func(c call) (*http.Response, error) {
			if c.api() == "SegmentEditor."+tool {
				return nil, context.DeadlineExceeded
			}
			return segmentToolAnswers(markAnswers()).handler(t)(c)
		})
		_, err := env.change("matomo.segments."+tool, "seg-deleter", `{"idSegment":2}`)
		if classOf(err) != provider.ClassTimeout || !strings.Contains(err.Error(), "never repeated") ||
			countMethod(calls, "SegmentEditor."+tool) != 1 {
			t.Errorf("%s timeout: err = %v", tool, err)
		}
		for name, body := range map[string]string{"empty": `{}`, "wrong state": `{"result":true,"starred":7}`,
			"false": `{"result":false,"starred":1}`} {
			var calls2 []call
			env2 := newSegmentsEnvironment(t, &calls2, segmentToolAnswers(answers{
				"SegmentEditor." + tool: func(call) *http.Response { return ok(body) },
			}).handler(t))
			if _, err := env2.change("matomo.segments."+tool, "seg-deleter", `{"idSegment":2}`); err == nil ||
				!strings.Contains(err.Error(), "never repeated") {
				t.Errorf("%s %s: err = %v", tool, name, err)
			}
		}
		// Matomo's refusal of the right is a clean permission error.
		var calls3 []call
		env3 := newSegmentsEnvironment(t, &calls3, segmentToolAnswers(answers{
			"SegmentEditor." + tool: func(call) *http.Response {
				return ok(`{"result":"error","message":"You can only edit segments created by a Super User"}`)
			},
		}).handler(t))
		if _, err := env3.change("matomo.segments."+tool, "seg-deleter", `{"idSegment":2}`); err == nil ||
			strings.Contains(err.Error(), "Super User") {
			t.Errorf("%s refusal: err = %v", tool, err)
		}
	}
}
