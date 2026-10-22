package twentycrm

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// objectsOutputDocument carries every member objects.get can report: a selection field with a description,
// a composite field whose part is a multi-selection field, and one list the tool leaves out.
var objectsOutputDocument = objectsDocument(
	`"stage":{"type":"string","description":"Sales stage","enum":["NEW","WON"]},` +
		`"oddStage":{"type":"string","enum":["has space"]},` +
		`"flags":{"type":"object","properties":{"kinds":{"type":"array","description":"Kinds","items":{"type":"string","enum":["A","B"]}},` +
		`"odd":{"type":"string","enum":["x y"]}}}`)

func objectsDocument(props string) string {
	return `{"paths":{"/people":{"post":{"operationId":"createOnePerson"}}},"components":{"schemas":{` +
		`"Person":{"type":"object","properties":{}},` +
		`"PersonForResponse":{"type":"object","properties":{` + props + `}}}}}`
}

func getObjectOf(t *testing.T, document string) (ObjectInfo, string) {
	t.Helper()
	serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, document), nil })
	stubLimiter(t, cloudKey)
	out, err := runObjectsTool(t, invokeObjectsGet, `{"object":"person"}`)
	if err != nil {
		t.Fatal(err)
	}
	var info ObjectInfo
	if err := json.Unmarshal(out, &info); err != nil {
		t.Fatal(err)
	}
	return info, string(out)
}

func TestObjectsGetReportsEnumValuesAndDescriptions(t *testing.T) {
	info, _ := getObjectOf(t, objectsOutputDocument)
	fields := map[string]FieldInfo{}
	for _, field := range info.Fields {
		fields[field.Name] = field
	}
	stage := fields["stage"]
	if stage.Description != "Sales stage" || strings.Join(stage.Enum, ",") != "NEW,WON" {
		t.Errorf("stage = %+v", stage)
	}
	flags := fields["flags"]
	if len(flags.Subfields) != 2 || flags.Subfields[0].Name != "kinds" ||
		flags.Subfields[0].Description != "Kinds" || strings.Join(flags.Subfields[0].Enum, ",") != "A,B" {
		t.Errorf("flags = %+v", flags)
	}
	if strings.Join(info.OmittedFields, ",") != "fields.flags.subfields.odd.enum,fields.oddStage.enum" &&
		strings.Join(info.OmittedFields, ",") != "fields.oddStage.enum,fields.flags.subfields.odd.enum" {
		t.Errorf("omitted_fields = %v", info.OmittedFields)
	}
	if len(fields["oddStage"].Enum) != 0 || len(flags.Subfields[1].Enum) != 0 {
		t.Error("an enum outside the plain form was reported")
	}
}

func TestObjectsGetEnumLimit(t *testing.T) {
	values := func(n int) string {
		list := make([]string, n)
		for i := range list {
			list[i] = fmt.Sprintf(`"V%d"`, i)
		}
		return "[" + strings.Join(list, ",") + "]"
	}
	info, _ := getObjectOf(t, objectsDocument(
		`"edge":{"type":"string","enum":`+values(500)+`},"over":{"type":"string","enum":`+values(501)+`}`))
	for _, field := range info.Fields {
		switch field.Name {
		case "edge":
			if len(field.Enum) != 500 {
				t.Errorf("edge holds %d values", len(field.Enum))
			}
		case "over":
			if len(field.Enum) != 0 {
				t.Error("501 values were reported")
			}
		}
	}
	if len(info.OmittedFields) != 1 || info.OmittedFields[0] != "fields.over.enum" {
		t.Errorf("omitted_fields = %v", info.OmittedFields)
	}
}

func TestObjectsGetCleansAndCapsDescriptions(t *testing.T) {
	long := strings.Repeat("a", 1200)
	info, _ := getObjectOf(t, objectsDocument(`"note":{"type":"string","description":"x\u0000y‮z\n","enum":["OK"]},`+
		`"long":{"type":"string","description":"`+long+`"},"plain":{"type":"string"}`))
	for _, field := range info.Fields {
		switch field.Name {
		case "note":
			if field.Description != "xyz" {
				t.Errorf("note description = %q", field.Description)
			}
		case "long":
			if len([]rune(field.Description)) != 1000 {
				t.Errorf("long description has %d runes", len([]rune(field.Description)))
			}
		}
	}
	if len(info.OmittedFields) != 0 {
		t.Errorf("omitted_fields = %v", info.OmittedFields)
	}
	_, raw := getObjectOf(t, objectsDocument(`"plain":{"type":"string"}`))
	if strings.Contains(raw, `"enum"`) || strings.Contains(raw, `"description"`) || strings.Contains(raw, "omitted_fields") {
		t.Errorf("output without the new members changed: %s", raw)
	}
}
