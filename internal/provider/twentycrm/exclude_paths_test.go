package twentycrm

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

func runExcluding(handler capability.Handler, args string, excludes ...string) (string, error) {
	red := &redact.Redactor{}
	result, err := handler(context.Background(), excludeConnection(excludes), resolver(red), red, json.RawMessage(args))
	out, _ := json.Marshal(result)
	return string(out), err
}

func wantUnavailable(t *testing.T, name string, err error) {
	t.Helper()
	var refusal *provider.InvalidRequestError
	if !asInvalid(err, &refusal) || refusal == nil {
		t.Errorf("%s: err = %v, want an invalid request", name, err)
		return
	}
	for _, word := range []string{"company", "rocket", "person", "deal", "exclu"} {
		if strings.Contains(strings.ToLower(refusal.Message), word) {
			t.Errorf("%s: the refusal names %q: %s", name, word, refusal.Message)
		}
	}
}

func TestExcludedRelationIDsAreRefusedForFilterGroupAndWrite(t *testing.T) {
	const company = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	requests := searchRequests(t, `[]`)
	for name, call := range map[string]struct {
		handler capability.Handler
		args    string
	}{
		"filter": {invokeRecordsSearch, `{"object":"person","conditions":[{"field":"companyId","operator":"eq","value":"` +
			company + `"}]}`},
		"group": {invokeRecordsGroupBy, `{"object":"person","group_by":["companyId"]}`},
	} {
		_, err := runExcluding(call.handler, call.args, "object/company")
		wantUnavailable(t, name, err)
	}
	if got := dataRequests(*requests); len(got) != 0 {
		t.Errorf("a data request was sent: %v", got)
	}
	// Without the exclusion of company the same calls pass, so the refusal comes from the exclusion.
	if _, err := runExcluding(invokeRecordsGroupBy, `{"object":"person","group_by":["companyId"]}`, "object/company"); err == nil {
		t.Fatal("group by an excluded relation id was accepted")
	}
	if _, err := runExcluding(invokeRecordsGroupBy, `{"object":"person","group_by":["companyId"]}`); err != nil {
		t.Errorf("group by companyId without exclusions = %v", err)
	}

	calls := serveWrites(t, http.StatusOK, createdAnswer("update"))
	args := `{"object":"person","id":"` + personID + `","fields":{"companyId":"` + otherID + `"}}`
	_, err := runExcluding(invokeRecordsUpdate, args, "object/company")
	wantUnavailable(t, "write", err)
	if len(writes(*calls)) != 0 {
		t.Errorf("a write was sent: %+v", *calls)
	}
	if _, err := runExcluding(invokeRecordsUpdate, args, "object/rocket"); err != nil {
		t.Errorf("write with an unrelated exclusion = %v", err)
	}
}

func TestForeignKeyOutputStaysAnIdentifier(t *testing.T) {
	recordRequests(t, personPage)
	out, err := runExcluding(invokeRecordsList, `{"object":"person"}`, "object/company")
	if err != nil {
		t.Fatal(err)
	}
	var list RecordList
	if json.Unmarshal([]byte(out), &list) != nil || list.Records[0].Fields["companyId"] != otherID {
		t.Errorf("list = %s", out)
	}
	if strings.Contains(out, "embedded") {
		t.Errorf("the embedded relation was returned: %s", out)
	}
}

func TestSearchAllWithoutObjectsSendsReachableMinusExcluded(t *testing.T) {
	answer := `{"data":{"search":{"edges":[
 {"node":{"recordId":"` + hitOne + `","objectNameSingular":"person","label":"Ada"}},
 {"node":{"recordId":"` + hitTwo + `","objectNameSingular":"company","label":"Acme-canary"}}
],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}`
	calls := searchAllServe(t, http.StatusOK, answer)
	out, err := runExcluding(invokeRecordsSearchAll, `{"text":"a"}`, "object/company")
	if err != nil {
		t.Fatal(err)
	}
	sent := searchCalls(*calls)
	if len(sent) != 1 {
		t.Fatalf("requests = %+v", sent)
	}
	included, _ := sent[0].variables["includedObjectNameSingulars"].([]any)
	for _, name := range included {
		if name == "company" || name == "workspaceMember" {
			t.Errorf("included = %v", included)
		}
	}
	if len(included) != 3 || sent[0].variables["excludedObjectNameSingulars"] != nil {
		t.Errorf("variables = %v", sent[0].variables)
	}
	// A hit of an excluded object is dropped and counted even if Twenty returned it.
	if strings.Contains(out, "Acme-canary") || !strings.Contains(out, `"omitted":1`) || !strings.Contains(out, "Ada") {
		t.Errorf("out = %s", out)
	}
}

func TestSearchAllIgnoresExcludedObjectsInArguments(t *testing.T) {
	calls := searchAllServe(t, http.StatusOK, `{"data":{"search":{"edges":[],"pageInfo":{"hasNextPage":false}}}}`)
	if _, err := runExcluding(invokeRecordsSearchAll, `{"text":"a","objects":["company","person"]}`, "object/company"); err != nil {
		t.Fatal(err)
	}
	if _, err := runExcluding(invokeRecordsSearchAll, `{"text":"a","exclude_objects":["company"]}`, "object/company"); err != nil {
		t.Fatal(err)
	}
	sent := searchCalls(*calls)
	if len(sent) != 2 || !reflect.DeepEqual(sent[0].variables["includedObjectNameSingulars"], []any{"person"}) ||
		sent[1].variables["excludedObjectNameSingulars"] != nil {
		t.Errorf("requests = %+v", sent)
	}
	refuse(t)
	for _, args := range []string{`{"text":"a","objects":["company"]}`, `{"text":"a","objects":["company","company"]}`} {
		_, err := runExcluding(invokeRecordsSearchAll, args, "object/company")
		wantUnavailable(t, args, err)
	}
	// With targets, an excluded target leaves no searched object.
	red := &redact.Redactor{}
	_, err := invokeRecordsSearchAll(context.Background(), excludeConnection([]string{"object/company"}, "object/company"),
		countingResolver(t), red, json.RawMessage(`{"text":"a"}`))
	wantUnavailable(t, "targets all excluded", err)
}

func TestSearchAllCursorDoesNotCrossExclusions(t *testing.T) {
	searchAllServe(t, http.StatusOK, hitsAnswer)
	first, err := runExcluding(invokeRecordsSearchAll, `{"text":"Ada"}`, "object/company")
	if err != nil {
		t.Fatal(err)
	}
	var page SearchAllResult
	if json.Unmarshal([]byte(first), &page) != nil || page.NextCursor == "" {
		t.Fatalf("page = %s", first)
	}
	args := `{"text":"Ada","cursor":"` + page.NextCursor + `"}`
	if _, err := runExcluding(invokeRecordsSearchAll, args, "object/company"); err != nil {
		t.Fatal(err)
	}
	for name, excludes := range map[string][]string{"none": nil, "other": {"object/rocket"}, "more": {"object/company", "object/rocket"}} {
		refuse(t)
		if _, err := runExcluding(invokeRecordsSearchAll, args, excludes...); err == nil {
			t.Errorf("%s: the cursor crossed another exclusion set", name)
		}
	}
}

func TestExcludedObjectLinksAreHiddenAndRefused(t *testing.T) {
	page := `{"data":{"noteTargets":[` + linkRow(`"targetPersonId":"`+recordID+`"`) + `,` +
		`{"id":"` + foreignID + `","noteId":"` + noteID + `","rocketId":"` + recordID + `"}]},"pageInfo":{"hasNextPage":false}}`
	calls := serveLinks(t, func(call linkCall) (int, string) {
		if call.Method == http.MethodGet && strings.HasSuffix(call.Path, "/"+foreignID) {
			return http.StatusOK, `{"data":{"noteTarget":{"id":"` + foreignID + `","noteId":"` + noteID +
				`","rocketId":"` + recordID + `"}}}`
		}
		return http.StatusOK, page
	})
	out, err := runExcluding(invokeActivityTargetsList, `{"activity":"note","activity_id":"`+noteID+`"}`, "object/rocket")
	if err != nil {
		t.Fatal(err)
	}
	var list ActivityTargetList
	if json.Unmarshal([]byte(out), &list) != nil || len(list.Links) != 1 || list.Omitted != 1 || list.Links[0].Object != "person" {
		t.Errorf("list = %s", out)
	}
	n := len(*calls)
	object := `"object":"rocket","record_id":"` + recordID + `"`
	for name, call := range map[string]struct {
		handler capability.Handler
		args    string
	}{
		"list by record": {invokeActivityTargetsList, `{"activity":"note",` + object + `}`},
		"create":         {invokeActivityTargetsCreate, `{"activity":"note","activity_id":"` + noteID + `",` + object + `}`},
	} {
		_, err := call.handler(context.Background(), excludeConnection([]string{"object/rocket"}), countingResolver(t),
			&redact.Redactor{}, json.RawMessage(call.args))
		wantUnavailable(t, name, err)
	}
	_, err = runExcluding(invokeActivityTargetsDelete, `{"activity":"note","id":"`+foreignID+`"}`, "object/rocket")
	var refusal *provider.InvalidRequestError
	if !asInvalid(err, &refusal) || refusal.Message != errLinkUnavailable {
		t.Errorf("delete = %v", err)
	}
	for _, call := range (*calls)[n:] {
		if call.Method != http.MethodGet {
			t.Errorf("a change was sent: %+v", call)
		}
	}
	// An excluded activity object closes its link tools.
	for name, handler := range map[string]capability.Handler{"list": invokeActivityTargetsList,
		"create": invokeActivityTargetsCreate, "delete": invokeActivityTargetsDelete} {
		_, err := handler(context.Background(), excludeConnection([]string{"object/note"}), countingResolver(t),
			&redact.Redactor{}, json.RawMessage(`{"activity":"note","activity_id":"`+noteID+`","id":"`+linkID+`",`+
				`"object":"person","record_id":"`+recordID+`"}`))
		wantUnavailable(t, "excluded note "+name, err)
	}
}

func TestExcludedObjectAttachmentsAreHiddenAndRefused(t *testing.T) {
	rocketTarget := `"targetRocketId":"` + attOtherID + `"`
	fake := okFake("x")
	fake.listed = []string{attachmentRowJSON(personTarget(), fileEntry(goodURL())),
		attachmentRowJSON(rocketTarget, fileEntry(goodURL()))}
	serveAttachments(t, fake)
	resolved := excludeConnection([]string{"object/rocket"})
	out, err := runAttachment(t, t.Context(), resolved, false, `{"object":"person","record_id":"`+attPersonID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	var list AttachmentList
	if json.Unmarshal([]byte(out), &list) != nil || len(list.Attachments) != 1 || list.Omitted != 1 {
		t.Errorf("list = %s", out)
	}
	// An attachment of an excluded record reads as missing and is never downloaded.
	fake = okFake("x")
	fake.row = attachmentRowJSON(rocketTarget, fileEntry(goodURL()))
	serveAttachments(t, fake)
	_, err = runAttachment(t, t.Context(), resolved, true, getArgs(""))
	if classOf(err) != provider.ClassNotFound || fake.downloads() != 0 {
		t.Errorf("get = %v, downloads %d", err, fake.downloads())
	}
	refuse(t)
	_, err = runAttachment(t, t.Context(), resolved, false, `{"object":"rocket","record_id":"`+attOtherID+`"}`)
	wantUnavailable(t, "list", err)
	_, err = invokeAttachmentsUpload(t.Context(), resolved, countingResolver(t), &redact.Redactor{},
		json.RawMessage(`{"object":"rocket","record_id":"`+attOtherID+`","name":"a.txt","content_base64":"eA=="}`))
	wantUnavailable(t, "upload", err)
}

// metaExcludeServe serves the given metadata reads and records every request.
func metaExcludeServe(t *testing.T, reads map[string]string) *[]metaWriteCall {
	return serveMetaWrite(t, reads, metaAnswer(`{}`))
}

func metaOtherObject() string {
	return strings.Replace(metaObjectBody(metaOtherID, true, false), `"deal"`, `"person"`, 1)
}

func TestMetaObjectWritesRefuseExcludedObjectsAfterTheReadOnly(t *testing.T) {
	path := metadataObjectsPath + "/" + metaObjID
	for name, call := range map[string]struct {
		handler capability.Handler
		args    string
	}{
		"update": {invokeMetaObjectsUpdate, `{"id":"` + metaObjID + `","label_singular":"Offer"}`},
		"delete": {invokeMetaObjectsDelete, `{"id":"` + metaObjID + `"}`},
	} {
		calls := metaExcludeServe(t, map[string]string{path: metaObjectBody(metaObjID, true, false)})
		_, err := runExcluding(call.handler, call.args, "object/deal")
		wantUnavailable(t, name, err)
		if len(*calls) != 1 || (*calls)[0].Method != http.MethodGet || len(metaWrites(calls)) != 0 {
			t.Errorf("%s: calls = %+v", name, *calls)
		}
	}
}

func TestMetaFieldWritesRefuseFieldsOfExcludedObjects(t *testing.T) {
	fieldPath := metadataFieldsPath + "/" + metaFieldID
	reads := map[string]string{fieldPath: metaFieldBody("TEXT", true, false),
		metadataObjectsPath + "/" + metaObjID: metaObjectBody(metaObjID, true, false)}
	for name, call := range map[string]struct {
		handler capability.Handler
		args    string
	}{
		"update": {invokeMetaFieldsUpdate, `{"id":"` + metaFieldID + `","label":"x"}`},
		"delete": {invokeMetaFieldsDelete, `{"id":"` + metaFieldID + `"}`},
	} {
		calls := metaExcludeServe(t, reads)
		_, err := runExcluding(call.handler, call.args, "object/deal")
		wantUnavailable(t, name, err)
		if len(metaWrites(calls)) != 0 || len(*calls) != 2 {
			t.Errorf("%s: calls = %+v", name, *calls)
		}
	}
	// A field of another object passes, after the extra read of its owner.
	calls := serveMetaWrite(t, reads, metaAnswer(metaFieldBody("TEXT", true, false)))
	if _, err := runExcluding(invokeMetaFieldsUpdate, `{"id":"`+metaFieldID+`","label":"x"}`, "object/rocket"); err != nil {
		t.Errorf("update of a free field = %v", err)
	}
	if len(metaWrites(calls)) != 1 {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestMetaFieldWritesRefuseRelationsIntoExcludedObjects(t *testing.T) {
	fieldPath := metadataFieldsPath + "/" + metaFieldID
	byID := `"relation":{"type":"MANY_TO_ONE","targetObjectMetadata":{"id":"` + metaOtherID + `"}}`
	byName := `"relation":{"type":"MANY_TO_ONE","targetObjectMetadata":{"id":"` + metaOtherID + `","nameSingular":"person"}}`
	for name, relation := range map[string]string{"target by id": byID, "target by name": byName} {
		field := strings.Replace(metaFieldBody("RELATION", true, false), `"isActive":true`, `"isActive":true,`+relation, 1)
		reads := map[string]string{fieldPath: field, metadataObjectsPath + "/" + metaObjID: metaObjectBody(metaObjID, true, false),
			metadataObjectsPath + "/" + metaOtherID: metaOtherObject()}
		for tool, call := range map[string]struct {
			handler capability.Handler
			args    string
		}{
			"update": {invokeMetaFieldsUpdate, `{"id":"` + metaFieldID + `","label":"x"}`},
			"delete": {invokeMetaFieldsDelete, `{"id":"` + metaFieldID + `"}`},
		} {
			calls := metaExcludeServe(t, reads)
			_, err := runExcluding(call.handler, call.args, "object/person")
			wantUnavailable(t, name+" "+tool, err)
			if len(metaWrites(calls)) != 0 {
				t.Errorf("%s %s: a change was sent: %+v", name, tool, *calls)
			}
		}
	}
	// A relation the read cannot place is refused as well.
	reads := map[string]string{fieldPath: metaFieldBody("RELATION", true, false),
		metadataObjectsPath + "/" + metaObjID: metaObjectBody(metaObjID, true, false)}
	calls := metaExcludeServe(t, reads)
	_, err := runExcluding(invokeMetaFieldsUpdate, `{"id":"`+metaFieldID+`","label":"x"}`, "object/person")
	wantUnavailable(t, "relation without ends", err)
	if len(metaWrites(calls)) != 0 {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestMetaFieldCreateRefusesExcludedOwnerAndTarget(t *testing.T) {
	reads := map[string]string{metadataObjectsPath + "/" + metaObjID: metaObjectBody(metaObjID, true, false),
		metadataObjectsPath + "/" + metaOtherID: metaOtherObject()}
	plain := `{"object_id":"` + metaObjID + `","type":"TEXT","name":"note","label":"Note"}`
	for name, test := range map[string]struct{ args, exclude string }{
		"owner":  {plain, "object/deal"},
		"target": {relationCreateArgs("MANY_TO_ONE"), "object/person"},
	} {
		calls := metaExcludeServe(t, reads)
		_, err := runExcluding(invokeMetaFieldsCreate, test.args, test.exclude)
		wantUnavailable(t, name, err)
		if len(metaWrites(calls)) != 0 {
			t.Errorf("%s: a change was sent: %+v", name, *calls)
		}
	}
	// Without a matching exclusion the field is created; without any exclusion no object is read.
	calls := serveMetaWrite(t, reads, metaAnswer(metaFieldBody("TEXT", true, false)))
	if _, err := runExcluding(invokeMetaFieldsCreate, `{"object_id":"`+metaObjID+`","type":"TEXT","name":"stage","label":"Stage"}`,
		"object/rocket"); err != nil {
		t.Errorf("create = %v", err)
	}
	if len(*calls) != 2 {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestMetaReadsStillShowExcludedObjects(t *testing.T) {
	serveMetaWrite(t, map[string]string{
		metadataObjectsPath + "/" + metaObjID:  metaObjectBody(metaObjID, true, false),
		metadataFieldsPath + "/" + metaFieldID: metaFieldBody("TEXT", true, false),
	}, metaAnswer(`{}`))
	if out, err := runExcluding(invokeMetaObjectsGet, `{"id":"`+metaObjID+`"}`, "object/deal"); err != nil ||
		!strings.Contains(out, `"deal"`) {
		t.Errorf("meta-objects.get = %s, %v", out, err)
	}
	if _, err := runExcluding(invokeMetaFieldsGet, `{"id":"`+metaFieldID+`"}`, "object/deal"); err != nil {
		t.Errorf("meta-fields.get = %v", err)
	}
}
