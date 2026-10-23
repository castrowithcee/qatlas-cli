package nextcloud

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func aclRule(id string) string {
	return `<nc:acl><nc:acl-mapping-type>group</nc:acl-mapping-type><nc:acl-mapping-id>` + id +
		`</nc:acl-mapping-id><nc:acl-mapping-display-name>Team ` + id + `</nc:acl-mapping-display-name>` +
		`<nc:acl-mask>31</nc:acl-mask><nc:acl-permissions>1</nc:acl-permissions></nc:acl>`
}

func appFile(props string) string {
	return `<d:response><d:href>` + aliceRoot + `/q1.pdf</d:href><d:propstat><d:prop>` +
		`<d:displayname>q1.pdf</d:displayname><d:resourcetype/><d:getcontentlength>1</d:getcontentlength>` +
		props + `</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
}

func statApp(t *testing.T, body string) *Entry {
	t.Helper()
	serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, multistatus(body)), nil
	})
	c, _ := client(t)
	got, err := c.StatFile(context.Background(), "q1.pdf")
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestAppAndServerFilePropertiesAreReportedExactly(t *testing.T) {
	got := statApp(t, appFile(
		`<nc:group-folder-id>12</nc:group-folder-id><nc:acl-enabled>true</nc:acl-enabled>`+
			`<nc:acl-can-manage>false</nc:acl-can-manage>`+
			`<nc:acl-list>`+aclRule("ops")+aclRule("dev")+`</nc:acl-list>`+
			`<nc:inherited-acl-list>`+aclRule("all")+`</nc:inherited-acl-list>`+
			`<nc:lock>1</nc:lock><nc:lock-owner-type>0</nc:lock-owner-type><nc:lock-owner>ada</nc:lock-owner>`+
			`<nc:lock-owner-displayname>Ada</nc:lock-owner-displayname><nc:lock-owner-editor>text</nc:lock-owner-editor>`+
			`<nc:lock-time>1772359200</nc:lock-time><nc:lock-timeout>0</nc:lock-timeout>`+
			`<nc:lock-token>opaquetoken</nc:lock-token><nc:rich-workspace>secret text</nc:rich-workspace>`+
			`<nc:rich-workspace-file>77</nc:rich-workspace-file><nc:metadata-files-live-photo>x</nc:metadata-files-live-photo>`+
			`<nc:reminder-due-date>2026-03-01T10:00:00+01:00</nc:reminder-due-date>`+
			`<nc:version-label>final</nc:version-label><nc:version-author>ada</nc:version-author>`))
	yes, no := true, false
	var zero, owner, gid int64 = 0, 0, 12
	rule := func(id, name string) map[string]string {
		return map[string]string{"acl_mapping_type": "group", "acl_mapping_id": id,
			"acl_mapping_display_name": name, "acl_mask": "31", "acl_permissions": "1"}
	}
	want := Entry{
		Path: "q1.pdf", Name: "q1.pdf", Type: typeFile, Size: 1, Readable: true,
		GroupFolderID: &gid, ACLEnabled: &yes, ACLCanManage: &no,
		ACLList:          []map[string]string{rule("ops", "Team ops"), rule("dev", "Team dev")},
		InheritedACLList: []map[string]string{rule("all", "Team all")},
		Locked:           &yes, LockOwnerType: &owner, LockOwner: "ada", LockOwnerDisplayName: "Ada",
		LockOwnerEditor: "text", LockTime: 1772359200, LockTimeout: &zero, RichWorkspaceFile: "77",
		ReminderDueDate: "2026-03-01T09:00:00Z", VersionLabel: "final", VersionAuthor: "ada",
	}
	if !reflect.DeepEqual(*got, want) {
		gj, _ := json.Marshal(got)
		wj, _ := json.Marshal(want)
		t.Errorf("entry =\n%s\nwant\n%s", gj, wj)
	}
	raw, _ := json.Marshal(got)
	for _, never := range []string{"opaquetoken", "secret text", "live", "lock_token", "rich_workspace\""} {
		if strings.Contains(string(raw), never) {
			t.Errorf("the output carries %q: %s", never, raw)
		}
	}
}

func TestAppPropertiesAbsentOrRefusedLeaveTheOutputUnchanged(t *testing.T) {
	plain := statApp(t, appFile(""))
	refused := statApp(t, `<d:response><d:href>`+aliceRoot+`/q1.pdf</d:href><d:propstat><d:prop>`+
		`<d:displayname>q1.pdf</d:displayname><d:resourcetype/><d:getcontentlength>1</d:getcontentlength>`+
		`</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat><d:propstat><d:prop>`+
		`<nc:lock/><nc:lock-timeout>5</nc:lock-timeout><nc:acl-list>`+aclRule("ops")+`</nc:acl-list>`+
		`<nc:version-label>x</nc:version-label></d:prop><d:status>HTTP/1.1 404 Not Found</d:status></d:propstat></d:response>`)
	if !reflect.DeepEqual(plain, refused) {
		t.Errorf("refused = %+v", refused)
	}
	raw, _ := json.Marshal(plain)
	want := `{"path":"q1.pdf","name":"q1.pdf","type":"file","size":1,"readable":true}`
	if string(raw) != want {
		t.Errorf("output = %s", raw)
	}
}

func TestACLListsOverTheBoundsAreLeftOutAndNamed(t *testing.T) {
	var rules strings.Builder
	for i := 0; i <= maxACLRules; i++ {
		rules.WriteString(aclRule("g" + strconv.Itoa(i)))
	}
	var members strings.Builder
	for i := 0; i <= maxACLMembers; i++ {
		members.WriteString("<nc:m" + strconv.Itoa(i) + ">v</nc:m" + strconv.Itoa(i) + ">")
	}
	got := statApp(t, appFile(`<nc:acl-list>`+rules.String()+`</nc:acl-list>`+
		`<nc:inherited-acl-list><nc:acl>`+members.String()+`</nc:acl></nc:inherited-acl-list>`))
	if got.ACLList != nil || got.InheritedACLList != nil {
		t.Errorf("lists = %+v %+v", got.ACLList, got.InheritedACLList)
	}
	if !reflect.DeepEqual(got.OmittedFields, []string{fieldACLList, fieldInhACL}) {
		t.Errorf("omitted = %v", got.OmittedFields)
	}

	var exact strings.Builder
	for i := 0; i < maxACLRules; i++ {
		exact.WriteString(aclRule("g"))
	}
	got = statApp(t, appFile(`<nc:acl-list>`+exact.String()+`</nc:acl-list>`))
	if len(got.ACLList) != maxACLRules || got.OmittedFields != nil {
		t.Errorf("list = %d, omitted %v", len(got.ACLList), got.OmittedFields)
	}
}

func TestSearchDoesNotRequestOrReportTheAppProperties(t *testing.T) {
	for _, p := range []string{"acl-", "lock", "group-folder", "rich-workspace", "reminder", "version-"} {
		if strings.Contains(fileSearchProps, p) {
			t.Errorf("search asks for %s", p)
		}
	}
	for _, never := range []string{"lock-token", "<nc:rich-workspace/>", "metadata-"} {
		if strings.Contains(filePropfindBody, never) || strings.Contains(fileFavoritesBody, never) {
			t.Errorf("a body asks for %s", never)
		}
	}
}

func TestEveryAppPropertyFieldIsDeclaredForTheFourFileTools(t *testing.T) {
	yes := true
	n := int64(1)
	e := Entry{
		GroupFolderID: &n, ACLEnabled: &yes, ACLCanManage: &yes, ACLList: []map[string]string{{"a": "b"}},
		InheritedACLList: []map[string]string{{"a": "b"}}, Locked: &yes, LockOwnerType: &n, LockOwner: "a",
		LockOwnerDisplayName: "a", LockOwnerEditor: "a", LockTime: 1, LockTimeout: &n, RichWorkspaceFile: "1",
		ReminderDueDate: "x", VersionLabel: "a", VersionAuthor: "a",
	}
	assertEntryFieldsDeclared(t, filesStat, e, false)
	assertEntryFieldsDeclared(t, filesList, e, true)
	assertEntryFieldsDeclared(t, filesSearch, e, true)
	assertEntryFieldsDeclared(t, favoritesList, e, true)
}
