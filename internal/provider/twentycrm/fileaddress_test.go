package twentycrm

import (
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const (
	signedToken   = "eyJhbGciOiJIUzI1NiJ9.file-token-canary.sig"
	foreignAddr   = "https://cdn.example.com/avatar.png?token=foreign-token"
	plainAddr     = cloudOrigin + "/page/people"
	signedAddress = cloudOrigin + "/file/person/" + personID + "?token=" + signedToken
)

func fileRecord() string {
	return `{"id":"` + personID + `","city":"Berlin",` +
		`"files":[{"fileId":"` + otherID + `","label":"cv.pdf","extension":".pdf","url":"` + signedAddress + `"}],` +
		`"photo":"` + signedAddress + `","site":"` + foreignAddr + `","plain":"` + plainAddr + `",` +
		`"createdAt":"2026-01-01T00:00:00.000Z"}`
}

func TestRecordValuesNeverShowASignedFileAddress(t *testing.T) {
	schema := strings.Replace(writeSchema, `"readOnly":{"type":"string"},`, `"readOnly":{"type":"string"},`+
		`"files":{"type":"array","items":{"type":"object","properties":{"fileId":{"type":"string"},"label":{"type":"string"},`+
		`"extension":{"type":"string"},"url":{"type":"string"}}}},"photo":{"type":"string"},"site":{"type":"string"},`+
		`"plain":{"type":"string"},`, 1)
	rec := fileRecord()
	page := `{"data":{"people":[` + rec + `]},"pageInfo":{"hasNextPage":false}}`
	ids := idList(personID)
	for name, tt := range map[string]struct {
		handler capability.Handler
		args    string
		answer  string
	}{
		"list":   {invokeRecordsList, `{"object":"person"}`, page},
		"get":    {invokeRecordsGet, `{"object":"person","id":"` + personID + `"}`, `{"data":{"person":` + rec + `}}`},
		"search": {invokeRecordsSearch, `{"object":"person","conditions":[{"field":"city","operator":"eq","value":"Berlin"}]}`, page},
		"duplicates": {invokeRecordsDuplicates, `{"object":"person","ids":` + ids + `}`,
			`{"data":[{"personDuplicates":[` + rec + `],"totalCount":1,"pageInfo":{"hasNextPage":false}}]}`},
		"create":       {invokeRecordsCreate, `{"object":"person","fields":{` + coveredCreate + `}}`, `{"data":{"createPerson":` + rec + `}}`},
		"batch-update": {invokeRecordsBatchUpdate, `{"object":"person","ids":` + ids + `,"fields":{"city":"Berlin"}}`, `{"data":{"updatePeople":[` + rec + `]}}`},
	} {
		t.Run(name, func(t *testing.T) {
			serve(t, func(request *http.Request) (*http.Response, error) {
				if request.URL.Path == schemaPath {
					return jsonResponse(http.StatusOK, schema), nil
				}
				return jsonResponse(http.StatusOK, tt.answer), nil
			})
			stubLimiter(t, cloudKey)
			out, err := runTool(t, tt.handler, tt.args)
			if err != nil {
				t.Fatal(err)
			}
			text := string(out)
			if strings.Contains(text, signedToken) || strings.Contains(text, signedAddress) || strings.Contains(text, `"photo"`) {
				t.Errorf("a signed address reached the output: %s", text)
			}
			for _, kept := range []string{`"fileId":"` + otherID + `"`, `"label":"cv.pdf"`, `"extension":".pdf"`, foreignAddr, plainAddr} {
				if !strings.Contains(text, kept) {
					t.Errorf("%s was dropped: %s", kept, text)
				}
			}
			if strings.Contains(text, `"url"`) {
				t.Errorf("a file url reached the output: %s", text)
			}
		})
	}
}
