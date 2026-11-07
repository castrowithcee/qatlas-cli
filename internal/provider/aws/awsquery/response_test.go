package awsquery

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/provider"
)

// outputShape follows the XmlLists, XmlMaps and XmlNamespaces outputs of the Smithy protocol tests.
func outputShape() *Shape {
	return &Shape{Kind: KindStruct, Fields: []Field{
		{Key: "name", Name: "name", Shape: str()},
		{Key: "count", Name: "count", Shape: &Shape{Kind: KindInteger}},
		{Key: "on", Name: "on", Shape: &Shape{Kind: KindBoolean}},
		{Key: "ratio", Name: "ratio", Shape: &Shape{Kind: KindFloat}},
		{Key: "created", Name: "created", Shape: &Shape{Kind: KindTimestamp}},
		{Key: "list", Name: "myList", Shape: &Shape{Kind: KindList, Elem: str()}},
		{Key: "items", Name: "items", Shape: &Shape{Kind: KindList, Elem: str(), ElemName: "item"}},
		{Key: "flat", Name: "flattenedList", Shape: &Shape{Kind: KindList, Elem: str()}, Flattened: true},
		{Key: "structs", Name: "structs", Shape: &Shape{Kind: KindList, Elem: &Shape{Kind: KindStruct, Fields: []Field{
			{Key: "a", Name: "a", Shape: str()}, {Key: "b", Name: "b", Shape: str()}}}}},
		{Key: "map", Name: "myMap", Shape: &Shape{Kind: KindMap, Elem: str()}},
		{Key: "named", Name: "namedMap", Shape: &Shape{Kind: KindMap, Elem: str(), KeyName: "K", ValueName: "V"}},
		{Key: "flat_map", Name: "flatMap", Shape: &Shape{Kind: KindMap, Elem: &Shape{Kind: KindStruct, Fields: []Field{
			{Key: "hi", Name: "hi", Shape: str()}}}}, Flattened: true},
		{Key: "nested", Name: "nested", Shape: &Shape{Kind: KindStruct, Fields: []Field{
			{Key: "inner", Name: "inner", Shape: str()}}}},
		{Key: "grid", Name: "grid", Shape: &Shape{Kind: KindList, Elem: &Shape{Kind: KindList, Elem: str()}}},
	}}
}

const namespacedXML = `<XmlTestResponse xmlns="https://example.com/">
  <XmlTestResult>
    <name xmlns="https://member.example.com">Hello!</name>
    <count>7</count><on>true</on><ratio>2.5</ratio><created>2026-11-06T14:00:00+02:00</created>
    <myList xmlns="https://list.example.com"><member xmlns="https://m.example.com">a</member><member>b</member></myList>
    <items><item>x</item><item>y</item></items>
    <flattenedList xmlns="https://flat.example.com">hi</flattenedList>
    <flattenedList xmlns="https://flat.example.com">bye</flattenedList>
    <structs><member><a>1</a><b xmlns="https://s.example.com">2</b><extra>ignored</extra></member></structs>
    <myMap xmlns="https://map.example.com">
      <entry><key>foo</key><value>Foo</value></entry>
      <entry><key>bar</key><value>Bar</value></entry>
    </myMap>
    <namedMap><entry><K>k1</K><V>v1</V></entry></namedMap>
    <flatMap xmlns="https://fm.example.com"><key>foo</key><value><hi>Foo</hi></value></flatMap>
    <flatMap><key>bar</key><value><hi>Bar</hi></value></flatMap>
    <nested xmlns="https://n.example.com"><inner>deep</inner></nested>
    <grid><member><member>1</member><member>2</member></member><member><member>3</member></member></grid>
    <unknownMember>ignored</unknownMember>
  </XmlTestResult>
  <ResponseMetadata><RequestId>c6104cbe-af31-11e0-8154-cbc7ccf896c7</RequestId></ResponseMetadata>
</XmlTestResponse>`

func decode(t *testing.T, body string, shape *Shape) (map[string]any, Meta, *provider.Error) {
	t.Helper()
	var out map[string]any
	meta, failure := client(answer(200, body), &fakeSigner{}).Do(context.Background(), "op",
		Call{Action: "XmlTest", Version: "2020-01-08", Output: shape}, &out)
	return out, meta, failure
}

func TestDecodeByShapeWithNamespaces(t *testing.T) {
	out, meta, failure := decode(t, namespacedXML, outputShape())
	if failure != nil {
		t.Fatal(failure)
	}
	want := map[string]any{
		"name": "Hello!", "count": int64(7), "on": true, "ratio": 2.5, "created": "2026-11-06T12:00:00Z",
		"list": []any{"a", "b"}, "items": []any{"x", "y"}, "flat": []any{"hi", "bye"},
		"structs":  []any{map[string]any{"a": "1", "b": "2"}},
		"map":      map[string]any{"foo": "Foo", "bar": "Bar"},
		"named":    map[string]any{"k1": "v1"},
		"flat_map": map[string]any{"foo": map[string]any{"hi": "Foo"}, "bar": map[string]any{"hi": "Bar"}},
		"nested":   map[string]any{"inner": "deep"},
		"grid":     []any{[]any{"1", "2"}, []any{"3"}},
	}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("out differs:\n got  %#v\n want %#v", out, want)
	}
	if meta.RequestID != "c6104cbe-af31-11e0-8154-cbc7ccf896c7" || meta.Truncated {
		t.Errorf("meta = %+v", meta)
	}
}

func TestDecodeEmptyAndMissingCollectionsAreEmptyNotErrors(t *testing.T) {
	for name, body := range map[string]string{
		"empty elements": `<XmlTestResponse><XmlTestResult><myList/><myMap></myMap><items></items></XmlTestResult></XmlTestResponse>`,
		"absent":         `<XmlTestResponse><XmlTestResult></XmlTestResult></XmlTestResponse>`,
	} {
		out, _, failure := decode(t, body, outputShape())
		if failure != nil {
			t.Fatalf("%s: %v", name, failure)
		}
		list, isList := out["list"].([]any)
		flat, isFlat := out["flat"].([]any)
		m, isMap := out["map"].(map[string]any)
		if !isList || len(list) != 0 || !isFlat || len(flat) != 0 || !isMap || len(m) != 0 {
			t.Errorf("%s: out = %#v", name, out)
		}
		if _, set := out["name"]; set {
			t.Errorf("%s: an absent scalar must stay absent", name)
		}
	}
}

func TestDecodeRefusesWhatItWouldHaveToGuess(t *testing.T) {
	wrap := func(inner string) string {
		return `<XmlTestResponse><XmlTestResult>` + inner + `</XmlTestResult></XmlTestResponse>`
	}
	for name, body := range map[string]string{
		"integer text":      wrap(`<count>seven</count>`),
		"boolean text":      wrap(`<on>yes</on>`),
		"timestamp text":    wrap(`<created>soon</created>`),
		"float infinity":    wrap(`<ratio>Infinity</ratio>`),
		"repeated member":   wrap(`<name>a</name><name>b</name>`),
		"repeated key":      wrap(`<myMap><entry><key>k</key><value>1</value></entry><entry><key>k</key><value>2</value></entry></myMap>`),
		"entry without key": wrap(`<myMap><entry><value>1</value></entry></myMap>`),
		"two list holders":  wrap(`<myList><member>a</member></myList><myList><member>b</member></myList>`),
		"truncated xml":     `<XmlTestResponse><XmlTestResult><name>x`,
		"wrong root":        `<Other><XmlTestResult/></Other>`,
		"invalid utf-8":     wrap("<name>\xff</name>"),
	} {
		if _, _, failure := decode(t, body, outputShape()); failure == nil || failure.Class != provider.ClassInvalidResponse {
			t.Errorf("%s: failure = %v", name, failure)
		}
	}
}

func TestDecodeCapsStringsListsAndDepth(t *testing.T) {
	shape := &Shape{Kind: KindStruct, Fields: []Field{
		{Key: "text", Name: "text", Shape: &Shape{Kind: KindString, MaxLen: 4}},
		{Key: "list", Name: "list", Shape: &Shape{Kind: KindList, Elem: str(), MaxItems: 2}},
	}}
	wrap := func(inner string) string {
		return `<XmlTestResponse><XmlTestResult>` + inner + `</XmlTestResult></XmlTestResponse>`
	}
	out, meta, failure := decode(t, wrap(`<text>abcéé</text>`), shape)
	// Cut at a rune boundary: "abc" plus half of é would be invalid, so three bytes remain.
	if failure != nil || out["text"] != "abc" || !meta.Truncated {
		t.Errorf("text = %q truncated = %v (%v)", out["text"], meta.Truncated, failure)
	}
	if _, _, failure = decode(t, wrap(`<list><member>1</member><member>2</member><member>3</member></list>`), shape); failure == nil {
		t.Error("a list over its cap was accepted; cutting it would present a partial list as complete")
	}
	if _, _, failure = decode(t, wrap(`<list><member>1</member><member>2</member></list>`), shape); failure != nil {
		t.Errorf("a list at its cap: %v", failure)
	}
	deep := wrap(strings.Repeat("<a>", 80) + strings.Repeat("</a>", 80))
	if _, _, failure = decode(t, deep, shape); failure == nil {
		t.Error("deeply nested result was accepted")
	}
	var big strings.Builder
	for i := 0; i <= maxNodes; i++ {
		big.WriteString("<x/>")
	}
	big.WriteString("")
	if _, _, failure = decode(t, wrap(big.String()), shape); failure == nil {
		t.Error("a result with too many elements was accepted")
	}
}

func TestDecodeNeedsAMapToDecodeInto(t *testing.T) {
	var wrong struct{}
	_, failure := client(answer(200, namespacedXML), &fakeSigner{}).Do(context.Background(), "op",
		Call{Action: "XmlTest", Version: "2020-01-08", Output: outputShape()}, &wrong)
	if failure == nil || failure.Class != provider.ClassProviderError {
		t.Errorf("failure = %v", failure)
	}
	bad := &Shape{Kind: KindStruct, Fields: []Field{{Key: "a", Name: "a"}}}
	if _, _, failure := decode(t, namespacedXML, bad); failure == nil || failure.Class != provider.ClassProviderError {
		t.Errorf("faulty shape: failure = %v, want a defect, not an invalid response", failure)
	}
}

// ---- pagination ----

// iamList is a Marker/IsTruncated list; sqsList a NextToken list.
func listShape() *Shape {
	return &Shape{Kind: KindStruct, Fields: []Field{
		{Key: "names", Name: "Names", Shape: &Shape{Kind: KindList, Elem: str()}},
		{Key: "is_truncated", Name: "IsTruncated", Shape: &Shape{Kind: KindBoolean}},
		{Key: "marker", Name: "Marker", Shape: str()},
		{Key: "next_token", Name: "NextToken", Shape: str()},
	}}
}

var (
	markerPager = Pager{TokenIn: "Marker", TokenOut: "marker", Truncated: "is_truncated", SizeParam: "MaxItems", MaxSize: 1000}
	tokenPager  = Pager{TokenIn: "NextToken", TokenOut: "next_token", SizeParam: "MaxResults", MaxSize: 100}
)

// pages answers a list by the token of the request: the form it received decides the page.
func pages(t *testing.T, bodies map[string]string, seen *[]url.Values) Doer {
	return roundTrip(func(r *http.Request) (*http.Response, error) {
		data, _ := io.ReadAll(r.Body)
		values, _ := url.ParseQuery(string(data))
		*seen = append(*seen, values)
		token := values.Get("Marker") + values.Get("NextToken")
		body, ok := bodies[token]
		if !ok {
			t.Errorf("unexpected token %q", token)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
}

func page(names []string, tail string) string {
	var b strings.Builder
	b.WriteString(`<ListResponse xmlns="https://iam.amazonaws.com/doc/2010-05-08/"><ListResult><Names>`)
	for _, n := range names {
		b.WriteString("<member>" + n + "</member>")
	}
	b.WriteString("</Names>" + tail + "</ListResult></ListResponse>")
	return b.String()
}

// walk reads a list to its end with the given pager, as a generated tool does, and returns every name and
// the number of requests.
func walk(t *testing.T, p Pager, bodies map[string]string, size int) ([]string, []url.Values) {
	t.Helper()
	var seen []url.Values
	c := client(pages(t, bodies, &seen), &fakeSigner{})
	binding := Binding("aws.iam.role.list", map[string]any{"region": "eu-central-1", ArgPageSize: size})
	var names []string
	cursor := ""
	for range 10 {
		pg, err := p.Start(binding, size, cursor)
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		var out map[string]any
		if _, failure := c.Do(context.Background(), "list", Call{Action: "List", Version: "2010-05-08",
			Params: pg.Params(), Output: listShape()}, &out); failure != nil {
			t.Fatal(failure)
		}
		for _, n := range out["names"].([]any) {
			names = append(names, n.(string))
		}
		next, failure := pg.Next("list", out)
		if failure != nil {
			t.Fatal(failure)
		}
		if next == "" {
			return names, seen
		}
		cursor = next
	}
	t.Fatal("the list did not end")
	return nil, nil
}

func TestPaginationOverThreePagesWithNextToken(t *testing.T) {
	names, seen := walk(t, tokenPager, map[string]string{
		"":   page([]string{"a", "b"}, `<NextToken>t1</NextToken>`),
		"t1": page([]string{"c", "d"}, `<NextToken>t2</NextToken>`),
		"t2": page([]string{"e"}, ``),
	}, 2)
	if !reflect.DeepEqual(names, []string{"a", "b", "c", "d", "e"}) || len(seen) != 3 {
		t.Fatalf("names = %v, requests = %d", names, len(seen))
	}
	if seen[0].Get("MaxResults") != "2" || seen[0].Has("NextToken") || seen[1].Get("NextToken") != "t1" || seen[2].Get("NextToken") != "t2" {
		t.Errorf("requests = %v", seen)
	}
}

func TestPaginationOverThreePagesWithMarkerAndIsTruncated(t *testing.T) {
	names, seen := walk(t, markerPager, map[string]string{
		"":   page([]string{"a"}, `<IsTruncated>true</IsTruncated><Marker>m1</Marker>`),
		"m1": page([]string{"b"}, `<IsTruncated>true</IsTruncated><Marker>m2</Marker>`),
		// The last page repeats a Marker but says it is not truncated: the end is IsTruncated, not the Marker.
		"m2": page([]string{"c"}, `<IsTruncated>false</IsTruncated><Marker>stale</Marker>`),
	}, 0)
	if !reflect.DeepEqual(names, []string{"a", "b", "c"}) || len(seen) != 3 || seen[0].Has("MaxItems") {
		t.Fatalf("names = %v, requests = %d, first = %v", names, len(seen), seen[0])
	}
}

func TestAFullPageIsNeverTheEnd(t *testing.T) {
	// The page has exactly page_size entries and a token: the list goes on.
	pg, _ := tokenPager.Start(Binding("t", nil), 2, "")
	next, failure := pg.Next("op", map[string]any{"names": []any{"a", "b"}, "next_token": "t1"})
	if failure != nil || next == "" {
		t.Fatalf("next = %q (%v)", next, failure)
	}
	// And a short page without a token is the end.
	if next, _ := pg.Next("op", map[string]any{"names": []any{"a"}}); next != "" {
		t.Errorf("end expected, got %q", next)
	}
}

func TestEmptyListEndsWithoutAnError(t *testing.T) {
	names, seen := walk(t, markerPager, map[string]string{"": page(nil, `<IsTruncated>false</IsTruncated>`)}, 0)
	if len(names) != 0 || len(seen) != 1 {
		t.Errorf("names = %v, requests = %d", names, len(seen))
	}
	names, _ = walk(t, tokenPager, map[string]string{"": `<ListResponse><ListResult/></ListResponse>`}, 0)
	if names != nil && len(names) != 0 {
		t.Errorf("names = %v", names)
	}
}

func TestManipulatedCursorsAreRefused(t *testing.T) {
	binding := Binding("aws.iam.role.list", map[string]any{"region": "eu-central-1"})
	pg, _ := tokenPager.Start(binding, 0, "")
	good, _ := pg.Next("op", map[string]any{"next_token": "t1"})
	if _, err := tokenPager.Start(binding, 0, good); err != nil {
		t.Fatalf("the cursor it issued: %v", err)
	}
	flipped := []byte(good)
	flipped[len(flipped)-1] ^= 1
	forged := cursorFor(t, []byte("not-the-binding"), "t1")
	for name, cursor := range map[string]string{
		"altered":        string(flipped),
		"truncated":      good[:len(good)-3],
		"not base64":     "%%%",
		"other binding":  forged,
		"too long":       strings.Repeat("A", MaxCursorLength+1),
		"control chars":  cursorFor(t, binding, "t\n1"),
		"empty provider": cursorFor(t, binding, ""),
	} {
		_, err := tokenPager.Start(binding, 0, cursor)
		if _, ok := err.(*provider.InvalidRequestError); !ok {
			t.Errorf("%s: err = %v, want invalid-request", name, err)
		}
	}
	// A cursor of another call (other arguments or another tool) is refused, a changed page size is not.
	other := Binding("aws.iam.role.list", map[string]any{"region": "us-east-1"})
	if _, err := tokenPager.Start(other, 0, good); err == nil {
		t.Error("cursor accepted for other arguments")
	}
	if _, err := tokenPager.Start(Binding("aws.iam.user.list", map[string]any{"region": "eu-central-1"}), 0, good); err == nil {
		t.Error("cursor accepted for another tool")
	}
	same := Binding("aws.iam.role.list", map[string]any{"region": "eu-central-1", ArgPageSize: 5, ArgCursor: good})
	if _, err := tokenPager.Start(same, 7, good); err != nil {
		t.Errorf("page size and cursor must stay out of the binding: %v", err)
	}
}

func cursorFor(t *testing.T, binding []byte, inner string) string {
	t.Helper()
	return provider.EncodeCursor(binding, inner)
}

func TestPaginationRefusesInconsistentAnswers(t *testing.T) {
	pg, _ := markerPager.Start(Binding("t", nil), 0, "")
	for name, out := range map[string]map[string]any{
		"truncated without marker": {"is_truncated": true},
		"empty marker":             {"is_truncated": true, "marker": ""},
		"non-boolean flag":         {"is_truncated": "true", "marker": "m"},
		"non-string marker":        {"is_truncated": true, "marker": 5},
		"control character":        {"is_truncated": true, "marker": "a\x00b"},
		"oversized marker":         {"is_truncated": true, "marker": strings.Repeat("m", MaxTokenLength+1)},
	} {
		if _, failure := pg.Next("op", out); failure == nil || failure.Class != provider.ClassInvalidResponse {
			t.Errorf("%s: failure = %v", name, failure)
		}
	}
	// A token the request already carried would loop forever.
	again, _ := markerPager.Start(Binding("t", nil), 0, mustCursor(t, markerPager, "m1"))
	if _, failure := again.Next("op", map[string]any{"is_truncated": true, "marker": "m1"}); failure == nil {
		t.Error("a repeated token was accepted")
	}
}

func mustCursor(t *testing.T, p Pager, token string) string {
	t.Helper()
	pg, _ := p.Start(Binding("t", nil), 0, "")
	cursor, failure := pg.Next("op", map[string]any{"is_truncated": true, "marker": token, "next_token": token})
	if failure != nil {
		t.Fatal(failure)
	}
	return cursor
}

func TestPageSizeFollowsWhatTheServiceKnows(t *testing.T) {
	binding := Binding("t", nil)
	for _, size := range []int{-1, 101} {
		if _, err := tokenPager.Start(binding, size, ""); err == nil {
			t.Errorf("page_size %d accepted", size)
		}
	}
	if pg, err := tokenPager.Start(binding, 100, ""); err != nil || pg.Params()["MaxResults"] != "100" {
		t.Errorf("page_size 100: %v %v", pg.Params(), err)
	}
	none := Pager{TokenIn: "NextToken", TokenOut: "next_token"}
	if _, err := none.Start(binding, 5, ""); err == nil {
		t.Error("page_size accepted for a service without one")
	}
	if pg, err := none.Start(binding, 0, ""); err != nil || len(pg.Params()) != 0 {
		t.Errorf("no page size: %v %v", pg.Params(), err)
	}
}

func TestDoMapsIAMErrorsAndNeverShowsTheMessage(t *testing.T) {
	for _, test := range []struct {
		status int
		code   string
		class  provider.Class
	}{
		{404, "NoSuchEntity", provider.ClassNotFound},
		{403, "AccessDenied", provider.ClassPermission},
		{409, "EntityAlreadyExists", provider.ClassProviderError},
		{400, "ProvisionedThroughputExceededException", provider.ClassRateLimited},
	} {
		body := `<ErrorResponse xmlns="https://iam.amazonaws.com/doc/2010-05-08/"><Error><Type>Sender</Type><Code>` + test.code +
			`</Code><Message>role Secret-Name-4711 not found</Message></Error><RequestId>11111111-2222-3333-4444-555555555555</RequestId></ErrorResponse>`
		_, failure := client(answer(test.status, body, "Retry-After", "3"), &fakeSigner{}).Do(context.Background(), "get role",
			Call{Action: "GetRole", Version: "2010-05-08", Output: outputShape()}, new(map[string]any))
		if failure == nil || failure.Class != test.class || strings.Contains(failure.Error(), "Secret-Name") ||
			failure.Original == nil || failure.Original.Entries[0].Code != test.code {
			t.Errorf("%s: failure = %+v", test.code, failure)
		}
	}
	// A body that is not XML at all stays a classified failure by status.
	_, failure := client(answer(500, "<<not xml"), &fakeSigner{}).Do(context.Background(), "op", identityCall, nil)
	if failure == nil || failure.Class != provider.ClassProviderError {
		t.Errorf("unreadable body: %v", failure)
	}
}
