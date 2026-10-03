package excalidrawplus

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const textElement = `{"id":"e1","type":"text","x":1,"y":2,"width":30,"height":20,"text":"Hello","expected_version":3}`

func patchArgs(elements ...string) string {
	return `{"scene_id":"s1","elements":[` + strings.Join(elements, ",") + `]}`
}

func sentBody(t *testing.T, request sentRequest) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(request.body), &body); err != nil {
		t.Fatalf("body = %s", request.body)
	}
	return body
}

// echoPatch answers a patch with the elements as the server would hold them: the sent ones at their version.
func echoPatch(t *testing.T, sent *[]sentRequest, newer map[string]int64) func(*http.Request) (*http.Response, error) {
	return manageHandler(map[string]string{"s1": ownCollection, "sf": foreignColl}, sent, func() (*http.Response, error) {
		body := sentBody(t, (*sent)[len(*sent)-1])
		var answer []string
		for _, raw := range body["elements"].([]any) {
			element := raw.(map[string]any)
			id := element["id"].(string)
			version := int64(element["version"].(float64))
			if v, ok := newer[id]; ok {
				answer = append(answer, `{"id":"`+id+`","version":`+strconv.FormatInt(v, 10)+`,"versionNonce":1}`)
				continue
			}
			answer = append(answer, `{"id":"`+id+`","version":`+strconv.FormatInt(version, 10)+`,"versionNonce":`+
				strconv.FormatInt(int64(element["versionNonce"].(float64)), 10)+`,"isDeleted":`+
				strconv.FormatBool(element["isDeleted"].(bool))+`}`)
		}
		return jsonResponse(200, `{"sceneVersion":"9","elements":[`+strings.Join(answer, ",")+`,{"id":"other","version":1}]}`), nil
	})
}

func TestPatchBindsElementVersionAndDeletes(t *testing.T) {
	var calls []call
	var sent []sentRequest
	env := newEnvironment(t, &calls, echoPatch(t, &sent, nil))
	del := `{"id":"e2","type":"rectangle","x":0,"y":0,"width":5,"height":5,"expected_version":7,"is_deleted":true}`
	out, err := env.invokeConfirmed(contentPatch.ID, "one", patchArgs(textElement, del))
	if err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0].method != http.MethodPatch || sent[0].path != apiPath+"/scenes/s1/content" {
		t.Fatalf("sent = %+v", sent)
	}
	elements := sentBody(t, sent[0])["elements"].([]any)
	first, second := elements[0].(map[string]any), elements[1].(map[string]any)
	if first["version"].(float64) != 4 || first["isDeleted"] != false || first["text"] != "Hello" ||
		second["version"].(float64) != 8 || second["isDeleted"] != true {
		t.Fatalf("elements = %+v", elements)
	}
	if !strings.Contains(out, `"sent":2`) || !strings.Contains(out, `"scene_version":"9"`) || strings.Contains(out, "not_applied") {
		t.Fatalf("out = %s", out)
	}
}

func TestPatchReportsSupersededElement(t *testing.T) {
	var calls []call
	var sent []sentRequest
	env := newEnvironment(t, &calls, echoPatch(t, &sent, map[string]int64{"e1": 9}))
	out, err := env.invokeConfirmed(contentPatch.ID, "one", patchArgs(textElement))
	if err != nil || !strings.Contains(out, `"not_applied":["e1"]`) {
		t.Fatalf("out = %s, err = %v", out, err)
	}
}

func TestPatchRequiresExpectedVersion(t *testing.T) {
	var calls []call
	var sent []sentRequest
	env := newEnvironment(t, &calls, echoPatch(t, &sent, nil))
	_, err := env.invokeConfirmed(contentPatch.ID, "one",
		patchArgs(`{"id":"e1","type":"rectangle","x":0,"y":0,"width":1,"height":1}`))
	if err == nil || len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %d, reads = %d", err, len(calls), *env.reads)
	}
}

func TestContentRefusesForeignSceneWithoutChange(t *testing.T) {
	var calls []call
	var sent []sentRequest
	env := newEnvironment(t, &calls, echoPatch(t, &sent, nil))
	element := `{"id":"e1","type":"rectangle","x":0,"y":0,"width":1,"height":1}`
	cases := map[string][3]string{
		contentPatch.ID:   {"one", patchArgs(textElement), "sf"},
		contentReplace.ID: {"listed", `{"scene_id":"sf","elements":[` + element + `]}`, "sf"},
	}
	for id, c := range cases {
		args := strings.Replace(c[1], `"scene_id":"s1"`, `"scene_id":"sf"`, 1)
		_, err := env.invokeConfirmed(id, c[0], args)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), foreignColl) {
			t.Fatalf("%s err = %v", id, err)
		}
	}
	if len(sent) != 0 {
		t.Fatalf("sent = %+v, want no PATCH or PUT", sent)
	}
}

func TestContentRequiresConfirm(t *testing.T) {
	var calls []call
	var sent []sentRequest
	env := newEnvironment(t, &calls, echoPatch(t, &sent, nil))
	for id, args := range map[string]string{contentPatch.ID: patchArgs(textElement),
		contentReplace.ID: `{"scene_id":"s1","elements":[{"id":"e","type":"ellipse","x":0,"y":0,"width":1,"height":1}]}`} {
		if _, err := env.core.Invoke(context.Background(), application.InvokeRequest{Operation: id,
			Connection: "listed", Arguments: json.RawMessage(args)}); err == nil {
			t.Fatalf("%s without confirm succeeded", id)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %d, reads = %d, want none", len(calls), *env.reads)
	}
}

func TestReplaceNeedsToolAllowList(t *testing.T) {
	if !contentReplace.RequiresToolAllowList || contentPatch.RequiresToolAllowList {
		t.Fatal("only replace requires the tools list")
	}
	var calls []call
	var sent []sentRequest
	env := newEnvironment(t, &calls, echoPatch(t, &sent, nil))
	args := `{"scene_id":"s1","elements":[{"id":"e","type":"ellipse","x":0,"y":0,"width":1,"height":1}]}`
	if _, err := env.invokeConfirmed(contentReplace.ID, "one", args); err == nil || len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("replace without tools list: err = %v, calls = %d", err, len(calls))
	}
	if _, err := env.invokeConfirmed(contentReplace.ID, "listed", args); err != nil {
		t.Fatalf("replace with tools list: %v", err)
	}
	if len(sent) != 1 || sent[0].method != http.MethodPut || sent[0].path != apiPath+"/scenes/s1/content" {
		t.Fatalf("sent = %+v", sent)
	}
	body := sentBody(t, sent[0])
	if body["type"] != "excalidraw" || body["files"] == nil || len(body["files"].(map[string]any)) != 0 ||
		body["appState"].(map[string]any)["viewBackgroundColor"] != defaultBackground ||
		body["elements"].([]any)[0].(map[string]any)["version"].(float64) != 1 {
		t.Fatalf("body = %s", sent[0].body)
	}
}

func TestContentLimitsRefusedBeforeIO(t *testing.T) {
	var calls []call
	var sent []sentRequest
	env := newEnvironment(t, &calls, echoPatch(t, &sent, nil))
	rect := func(id string) string {
		return `{"id":"` + id + `","type":"rectangle","x":0,"y":0,"width":1,"height":1,"expected_version":0}`
	}
	var many []string
	for i := 0; i <= maxPatchElements; i++ {
		many = append(many, rect("r"+strconv.Itoa(i)))
	}
	bigText := `{"id":"t","type":"text","x":0,"y":0,"width":1,"height":1,"expected_version":0,"text":"` +
		strings.Repeat("a", maxElementTextLen+1) + `"}`
	var points []string
	for i := 0; i <= maxElementPoints; i++ {
		points = append(points, "[0,0]")
	}
	line := `{"id":"l","type":"line","x":0,"y":0,"width":1,"height":1,"expected_version":0,"points":[` + strings.Join(points, ",") + `]}`
	cases := map[string]string{
		"count":       patchArgs(many...),
		"empty":       patchArgs(),
		"text":        patchArgs(bigText),
		"points":      patchArgs(line),
		"duplicate":   patchArgs(rect("a"), rect("a")),
		"free field":  patchArgs(`{"id":"a","type":"rectangle","x":0,"y":0,"width":1,"height":1,"expected_version":0,"link":"https://x.invalid"}`),
		"embeddable":  patchArgs(`{"id":"a","type":"embeddable","x":0,"y":0,"width":1,"height":1,"expected_version":0}`),
		"text w/o":    patchArgs(`{"id":"a","type":"text","x":0,"y":0,"width":1,"height":1,"expected_version":0}`),
		"font on box": patchArgs(`{"id":"a","type":"rectangle","x":0,"y":0,"width":1,"height":1,"expected_version":0,"font_size":9}`),
		"coordinate":  patchArgs(`{"id":"a","type":"rectangle","x":1e9,"y":0,"width":1,"height":1,"expected_version":0}`),
		"color":       patchArgs(`{"id":"a","type":"rectangle","x":0,"y":0,"width":1,"height":1,"expected_version":0,"stroke_color":"red"}`),
	}
	for name, args := range cases {
		if _, err := env.invokeConfirmed(contentPatch.ID, "one", args); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A body above 1 MiB: 100 texts of 2000 four-byte characters.
	var heavy []string
	for i := 0; i < maxPatchElements; i++ {
		heavy = append(heavy, `{"id":"h`+strconv.Itoa(i)+`","type":"text","x":0,"y":0,"width":1,"height":1,"expected_version":0,"text":"`+
			strings.Repeat("\U0001F600", maxElementTextLen)+`"}`)
	}
	if _, err := env.invokeConfirmed(contentPatch.ID, "one", patchArgs(heavy...)); !isInvalidRequest(err) {
		t.Errorf("heavy body: err = %v", err)
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %d, reads = %d, want none", len(calls), *env.reads)
	}
}

func TestContentChangeUncertainWithoutRetry(t *testing.T) {
	cases := map[string]func() (*http.Response, error){
		"5xx":      func() (*http.Response, error) { return jsonResponse(500, foreignCanary), nil },
		"timeout":  func() (*http.Response, error) { return nil, context.DeadlineExceeded },
		"abort":    func() (*http.Response, error) { return nil, errors.New("connection reset by peer") },
		"unusable": func() (*http.Response, error) { return jsonResponse(200, `not json`), nil },
	}
	for name, change := range cases {
		for _, op := range [][2]string{{contentPatch.ID, patchArgs(textElement)},
			{contentReplace.ID, `{"scene_id":"s1","elements":[{"id":"e","type":"ellipse","x":0,"y":0,"width":1,"height":1}]}`}} {
			var calls []call
			var sent []sentRequest
			env := newEnvironment(t, &calls, manageHandler(map[string]string{"s1": ownCollection}, &sent, change))
			_, err := env.invokeConfirmed(op[0], "listed", op[1])
			var providerErr *provider.Error
			if err == nil || !errors.As(err, &providerErr) || strings.Contains(err.Error(), foreignCanary) ||
				!strings.Contains(err.Error(), "may have taken effect") {
				t.Errorf("%s %s: err = %v", name, op[0], err)
			}
			if len(sent) != 1 {
				t.Errorf("%s %s: %d change requests, want exactly 1", name, op[0], len(sent))
			}
		}
	}
}

func TestContentProviderTextStaysOutOfErrors(t *testing.T) {
	var calls []call
	var sent []sentRequest
	env := newEnvironment(t, &calls, manageHandler(map[string]string{"s1": ownCollection}, &sent,
		func() (*http.Response, error) { return jsonResponse(400, `{"message":"`+foreignCanary+`"}`), nil }))
	_, err := env.invokeConfirmed(contentPatch.ID, "one", patchArgs(textElement))
	if err == nil || strings.Contains(err.Error(), foreignCanary) || strings.Contains(err.Error(), "may have taken effect") {
		t.Fatalf("err = %v", err)
	}
}

func TestContentDescriptorsCarryFullRisk(t *testing.T) {
	for _, d := range []struct {
		name         string
		effect, idem string
		confirm      string
		open         bool
		sens         string
	}{
		{contentPatch.ID, string(contentPatch.Risk.Effect), string(contentPatch.Risk.Idempotency),
			string(contentPatch.Risk.Confirmation), contentPatch.Risk.OpenWorld, contentPatch.Risk.DataSensitivity},
		{contentReplace.ID, string(contentReplace.Risk.Effect), string(contentReplace.Risk.Idempotency),
			string(contentReplace.Risk.Confirmation), contentReplace.Risk.OpenWorld, contentReplace.Risk.DataSensitivity},
	} {
		if d.effect == "" || d.idem != "unknown" || d.confirm != "required" || !d.open || d.sens != dataSensitivity {
			t.Errorf("%s risk = %+v", d.name, d)
		}
	}
	if contentPatch.Version < 1 || contentReplace.Version < 1 {
		t.Fatal("descriptor version missing")
	}
}
