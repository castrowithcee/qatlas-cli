package provider

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

var testTexts = StatusTexts{Subject: "Demo", Auth: "Demo rejected the token", Permission: "no", NotFound: "gone"}

func TestErrorOriginalFormats(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		fields OriginalFields
		want   Original
	}{
		{"message", `{"message":"Bad things","documentation_url":"https://d/x"}`,
			OriginalFields{Message: "message", Documentation: "documentation_url"},
			Original{Status: 422, Documentation: "https://d/x", Entries: []OriginalEntry{{Message: "Bad things"}}}},
		{"error_msg", `{"error_msg":"oops"}`, OriginalFields{Message: "error_msg"},
			Original{Status: 422, Entries: []OriginalEntry{{Message: "oops"}}}},
		{"nested with object list", `{"error":{"code":"E1","description":"top","errors":[{"field":"a.b","message":"m1"},{"field":"c","message":"m2"},"skip"]}}`,
			OriginalFields{Message: "error.description", Code: "error.code", List: "error.errors",
				Item: &OriginalFields{Path: "field", Message: "message"}},
			Original{Status: 422, Entries: []OriginalEntry{
				{Code: "E1", Message: "top"}, {Path: "a.b", Message: "m1"}, {Path: "c", Message: "m2"}}}},
		{"text list", `{"messages":["one","two"],"error":"Bad Request"}`,
			OriginalFields{Message: "error", Type: "", List: "messages"},
			Original{Status: 422, Entries: []OriginalEntry{{Message: "Bad Request"}, {Message: "one"}, {Message: "two"}}}},
		{"number code", `{"ok":false,"error_code":400,"description":"Bad Request: x"}`,
			OriginalFields{Code: "error_code", Message: "description"},
			Original{Status: 422, Entries: []OriginalEntry{{Code: "400", Message: "Bad Request: x"}}}},
		{"unnamed json is dropped", `{"request":{"token":"secret"},"other":"x"}`,
			OriginalFields{Message: "message"}, Original{Status: 422}},
		{"object value is not scalar", `{"message":{"token":"secret"}}`,
			OriginalFields{Message: "message"}, Original{Status: 422}},
		{"json array is dropped", `["a"]`, OriginalFields{Message: "message"}, Original{Status: 422}},
		{"plain text", "  <html>Bad Gateway</html>\n", OriginalFields{Message: "message"},
			Original{Status: 422, Entries: []OriginalEntry{{Message: "<html>Bad Gateway</html>"}}}},
		{"empty", " \n", OriginalFields{Message: "message"}, Original{Status: 422}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ErrorOriginal(422, []byte(tt.body), tt.fields)
			if !reflect.DeepEqual(*got, tt.want) {
				t.Fatalf("got %+v, want %+v", *got, tt.want)
			}
		})
	}
}

type countingReader struct{ read int }

func (c *countingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	c.read += len(p)
	return len(p), nil
}

func TestClassifyStatusBodyReadsLimitedAndKeepsMessage(t *testing.T) {
	reader := &countingReader{}
	got := ClassifyStatusBody("demo.op", 500, testTexts, reader, OriginalFields{Message: "message"})
	if reader.read > ErrorBodyLimit {
		t.Fatalf("read %d bytes, limit %d", reader.read, ErrorBodyLimit)
	}
	if got.Original == nil || len(got.Original.Entries) != 1 || len(got.Original.Entries[0].Message) != ErrorBodyLimit {
		t.Fatalf("unexpected original %+v", got.Original)
	}
	want := ClassifyStatus("demo.op", 500, testTexts)
	if got.Class != want.Class || got.Message != want.Message || got.Error() != want.Error() {
		t.Fatalf("classification changed: %+v vs %+v", got, want)
	}
}

func TestClassifyStatusBodyErrorOmitsProviderText(t *testing.T) {
	body := `{"message":"secret provider words"}`
	got := ClassifyStatusBody("demo.op", 401, testTexts, strings.NewReader(body), OriginalFields{Message: "message"})
	if got.Original.Entries[0].Message != "secret provider words" {
		t.Fatalf("original missing: %+v", got.Original)
	}
	if strings.Contains(got.Error(), "secret") {
		t.Fatalf("Error() leaks provider text: %q", got.Error())
	}
	var asErr *Error
	if !errors.As(error(got), &asErr) {
		t.Fatal("not an *Error")
	}
}

func TestClassifyStatusBodyNilBody(t *testing.T) {
	got := ClassifyStatusBody("demo.op", 404, testTexts, nil, OriginalFields{})
	if got.Original == nil || got.Original.Status != 404 || len(got.Original.Entries) != 0 {
		t.Fatalf("unexpected %+v", got.Original)
	}
}
