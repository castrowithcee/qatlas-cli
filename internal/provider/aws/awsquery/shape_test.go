package awsquery

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func str() *Shape { return &Shape{Kind: KindString} }

// inputShape follows the QueryLists and QueryMaps inputs of the Smithy protocol tests.
func inputShape() *Shape {
	greeting := &Shape{Kind: KindStruct, Fields: []Field{{Key: "hi", Name: "hi", Shape: str()}}}
	stringList := &Shape{Kind: KindList, Elem: str()}
	itemList := &Shape{Kind: KindList, Elem: str(), ElemName: "item"}
	return &Shape{Kind: KindStruct, Fields: []Field{
		{Key: "list_arg", Name: "ListArg", Shape: stringList},
		{Key: "complex_list", Name: "ComplexListArg", Shape: &Shape{Kind: KindList, Elem: greeting}},
		{Key: "flat_list", Name: "FlattenedListArg", Shape: stringList, Flattened: true},
		{Key: "item_list", Name: "ListArgWithXmlNameMember", Shape: itemList},
		{Key: "flat_named", Name: "Hi", Shape: itemList, Flattened: true},
		{Key: "map_arg", Name: "MapArg", Shape: &Shape{Kind: KindMap, Elem: str()}},
		{Key: "renamed_map", Name: "reNamed", Shape: &Shape{Kind: KindMap, Elem: str()}},
		{Key: "complex_map", Name: "ComplexMapArg", Shape: &Shape{Kind: KindMap, Elem: greeting}},
		{Key: "named_map", Name: "MapWithXmlMemberName", Shape: &Shape{Kind: KindMap, Elem: str(), KeyName: "K", ValueName: "V"}},
		{Key: "flat_map", Name: "FlattenedMap", Shape: &Shape{Kind: KindMap, Elem: str()}, Flattened: true},
		{Key: "flat_named_map", Name: "FlattenedMapWithXmlName", Shape: &Shape{Kind: KindMap, Elem: str(), KeyName: "K", ValueName: "V"}, Flattened: true},
		{Key: "nested", Name: "Nested", Shape: &Shape{Kind: KindStruct, Fields: []Field{
			{Key: "name", Name: "Name", Shape: str()},
			{Key: "tags", Name: "Tags", Shape: &Shape{Kind: KindList, Elem: &Shape{Kind: KindStruct, Fields: []Field{
				{Key: "key", Name: "Key", Shape: str()}, {Key: "value", Name: "Value", Shape: str()}}}}},
			{Key: "grid", Name: "Grid", Shape: &Shape{Kind: KindList, Elem: &Shape{Kind: KindList, Elem: &Shape{Kind: KindInteger}}}},
		}}},
		{Key: "flag", Name: "Flag", Shape: &Shape{Kind: KindBoolean}},
		{Key: "count", Name: "Count", Shape: &Shape{Kind: KindInteger}},
		{Key: "ratio", Name: "Ratio", Shape: &Shape{Kind: KindFloat}},
		{Key: "since", Name: "Since", Shape: &Shape{Kind: KindTimestamp}},
		{Key: "blob", Name: "Blob", Shape: &Shape{Kind: KindBlob}},
	}}
}

func j(t *testing.T, text string) map[string]any {
	t.Helper()
	d := json.NewDecoder(strings.NewReader(text))
	d.UseNumber()
	var m map[string]any
	if err := d.Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

func form(t *testing.T, r Request) string {
	t.Helper()
	body, err := EncodeBody("Test", "2020-01-08", r.Params)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestEncodeFollowsTheSmithyTraits(t *testing.T) {
	args := j(t, `{
	  "list_arg": ["foo","bar"], "complex_list": [{"hi":"hello"},{"hi":"hola"}],
	  "flat_list": ["A","B"], "item_list": ["A","B"], "flat_named": ["A","B"],
	  "map_arg": {"foo":"Foo","bar":"Bar"}, "renamed_map": {"foo":"Foo"},
	  "complex_map": {"foo":{"hi":"Foo"}}, "named_map": {"bar":"Bar"},
	  "flat_map": {"foo":"Foo","bar":"Bar"}, "flat_named_map": {"bar":"Bar"},
	  "nested": {"name":"n","tags":[{"key":"k","value":"v"}],"grid":[[1,2],[3]]},
	  "flag": true, "count": 12, "ratio": 1.5, "since": "2026-11-06T14:00:00+02:00", "blob": "aGk="
	}`)
	req, err := Encode(inputShape(), args, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"ListArg.member.1": "foo", "ListArg.member.2": "bar",
		"ComplexListArg.member.1.hi": "hello", "ComplexListArg.member.2.hi": "hola",
		"FlattenedListArg.1": "A", "FlattenedListArg.2": "B",
		"ListArgWithXmlNameMember.item.1": "A", "ListArgWithXmlNameMember.item.2": "B",
		"Hi.1": "A", "Hi.2": "B",
		"MapArg.entry.1.key": "bar", "MapArg.entry.1.value": "Bar", "MapArg.entry.2.key": "foo", "MapArg.entry.2.value": "Foo",
		"reNamed.entry.1.key": "foo", "reNamed.entry.1.value": "Foo",
		"ComplexMapArg.entry.1.key": "foo", "ComplexMapArg.entry.1.value.hi": "Foo",
		"MapWithXmlMemberName.entry.1.K": "bar", "MapWithXmlMemberName.entry.1.V": "Bar",
		"FlattenedMap.1.key": "bar", "FlattenedMap.1.value": "Bar", "FlattenedMap.2.key": "foo", "FlattenedMap.2.value": "Foo",
		"FlattenedMapWithXmlName.1.K": "bar", "FlattenedMapWithXmlName.1.V": "Bar",
		"Nested.Name": "n", "Nested.Tags.member.1.Key": "k", "Nested.Tags.member.1.Value": "v",
		"Nested.Grid.member.1.member.1": "1", "Nested.Grid.member.1.member.2": "2", "Nested.Grid.member.2.member.1": "3",
		"Flag": "true", "Count": "12", "Ratio": "1.5", "Since": "2026-11-06T12:00:00Z", "Blob": "aGk=",
	}
	if !reflect.DeepEqual(req.Params, want) {
		t.Fatalf("params differ:\n got  %v\n want %v", req.Params, want)
	}
	if req.Token != "" || req.Generated {
		t.Errorf("no token member, got %q", req.Token)
	}
	// The expected form body of a small input, as a server receives it.
	small := &Shape{Kind: KindStruct, Fields: inputShape().Fields[:3]}
	req, _ = Encode(small, j(t, `{"list_arg":["foo","b&r"],"flat_list":["A"]}`), nil)
	if got, want := form(t, req), "Action=Test&Version=2020-01-08&FlattenedListArg.1=A&ListArg.member.1=foo&ListArg.member.2=b%26r"; got != want {
		t.Errorf("body = %s\nwant   %s", got, want)
	}
}

func TestEncodeEmptyCollections(t *testing.T) {
	req, err := Encode(inputShape(), j(t, `{"list_arg":[],"map_arg":{},"flag":false}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	// An empty list is ListArg=, an empty map is not serialized, as the protocol tests define it.
	if !reflect.DeepEqual(req.Params, map[string]string{"ListArg": "", "Flag": "false"}) {
		t.Errorf("params = %v", req.Params)
	}
	if _, err := Encode(inputShape(), j(t, `{"flat_list":[]}`), nil); !errors.Is(err, ErrValue) {
		t.Errorf("empty flattened list = %v, want a refusal instead of a guess", err)
	}
}

func TestEncodeRefusesWhatItWouldHaveToGuess(t *testing.T) {
	for name, args := range map[string]string{
		"unknown argument":     `{"nope":1}`,
		"unknown member":       `{"nested":{"nope":1}}`,
		"string for list":      `{"list_arg":"x"}`,
		"number for string":    `{"nested":{"name":1}}`,
		"fraction for integer": `{"count":1.5}`,
		"string for boolean":   `{"flag":"true"}`,
		"bad timestamp":        `{"since":"yesterday"}`,
		"bad blob":             `{"blob":"%%%"}`,
		"null list element":    `{"list_arg":["a",null]}`,
		"object for list":      `{"list_arg":{"a":"b"}}`,
		"list for map":         `{"map_arg":["a"]}`,
		"struct for scalar":    `{"flag":{}}`,
		"too large an integer": `{"count":99999999999999999999}`,
	} {
		if _, err := Encode(inputShape(), j(t, args), nil); !errors.Is(err, ErrValue) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := Encode(nil, nil, nil); !errors.Is(err, ErrShape) {
		t.Errorf("nil shape = %v", err)
	}
}

func TestErrorsNameTheMemberNotTheValue(t *testing.T) {
	_, err := Encode(inputShape(), j(t, `{"count":"s3cret-value"}`), nil)
	if err == nil || strings.Contains(err.Error(), "s3cret") || !strings.Contains(err.Error(), "count") {
		t.Errorf("err = %v", err)
	}
}

func TestValidateRejectsFaultyShapes(t *testing.T) {
	if err := inputShape().Validate(); err != nil {
		t.Fatalf("valid shape: %v", err)
	}
	dup := &Shape{Kind: KindStruct, Fields: []Field{{Key: "a", Name: "A", Shape: str()}, {Key: "b", Name: "A", Shape: str()}}}
	flatScalar := &Shape{Kind: KindStruct, Fields: []Field{{Key: "a", Name: "A", Shape: str(), Flattened: true}}}
	nestedToken := &Shape{Kind: KindStruct, Fields: []Field{{Key: "n", Name: "N", Shape: &Shape{Kind: KindStruct,
		Fields: []Field{{Key: "t", Name: "ClientToken", Shape: str(), IdempotencyToken: true}}}}}}
	listToken := &Shape{Kind: KindStruct, Fields: []Field{{Key: "t", Name: "ClientToken", Shape: &Shape{Kind: KindList, Elem: str()}, IdempotencyToken: true}}}
	badName := &Shape{Kind: KindStruct, Fields: []Field{{Key: "a", Name: "A.B", Shape: str()}}}
	for name, s := range map[string]*Shape{"duplicate wire name": dup, "flattened scalar": flatScalar,
		"nested token": nestedToken, "token on a list": listToken, "dotted name": badName,
		"no kind": {}, "list without element": {Kind: KindList}} {
		if err := s.Validate(); !errors.Is(err, ErrShape) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func tokenShape() *Shape {
	return &Shape{Kind: KindStruct, Fields: []Field{
		{Key: "name", Name: "Name", Shape: str()},
		{Key: "client_token", Name: "ClientToken", Shape: str(), IdempotencyToken: true},
	}}
}

func TestEncodeGeneratesTheIdempotencyToken(t *testing.T) {
	req, err := Encode(tokenShape(), j(t, `{"name":"x"}`), nil)
	if err != nil || !req.Generated || len(req.Token) != 36 || req.Params["ClientToken"] != req.Token {
		t.Fatalf("req = %+v, err = %v", req, err)
	}
	again, _ := Encode(tokenShape(), j(t, `{"name":"x"}`), nil)
	if again.Token == req.Token {
		t.Error("two calls must not share a generated token")
	}
	fixed, _ := Encode(tokenShape(), j(t, `{"name":"x"}`), bytes.NewReader(make([]byte, 16)))
	if fixed.Token != "00000000-0000-4000-8000-000000000000" {
		t.Errorf("token from a given source = %s", fixed.Token)
	}
}

func TestEncodeUsesAGivenToken(t *testing.T) {
	req, err := Encode(tokenShape(), j(t, `{"name":"x","client_token":"mine-1"}`), nil)
	if err != nil || req.Generated || req.Token != "mine-1" || req.Params["ClientToken"] != "mine-1" {
		t.Fatalf("req = %+v, err = %v", req, err)
	}
	for _, bad := range []string{`{"client_token":"two words"}`, `{"client_token":7}`} {
		if _, err := Encode(tokenShape(), j(t, bad), nil); err == nil {
			t.Errorf("%s was accepted", bad)
		}
	}
	// The form carries the token like any member.
	body := form(t, req)
	values, _ := url.ParseQuery(body)
	if values.Get("ClientToken") != "mine-1" {
		t.Errorf("body = %s", body)
	}
}
