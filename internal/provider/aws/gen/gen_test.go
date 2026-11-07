package gen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/awsquery"
)

const pinnedCommit = "1b9ff13fe6c28ea9774a527e53cda6a3b91369dc"

// The extracts match the pin file, which names the pinned commit, the upstream hashes, and the change; the
// upstream LICENSE and NOTICE lie beside them.
func TestPinMatchesTheExtracts(t *testing.T) {
	p, err := LoadPin(ModelDir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Commit != pinnedCommit || p.RetrievedOn != "2026-11-07" ||
		!strings.Contains(p.Modification, "smithy.api#documentation") || !strings.Contains(p.Modification, "smithy.api#examples") {
		t.Errorf("pin = %+v", p)
	}
	want := map[string]string{
		"sts":     "794c8b2142e3a65bc6cc7922005eb85d6b71b02353fef03c0f395e8f902ee2d0",
		"iam":     "c1f74d3eda269e4fa2f00b57d57ed19c0e0a6d7fc7c70d4e351e319d178430f9",
		"account": "c4ce6a205189e3ae2223da18bf426a0eda6aadb347598e787b1cfc1e4bd4b509",
		"sesv2":   "8866fe8e17fe960ab5b7fb3615326fff0a8af73bbebeb5ba87ba6ee1f694e467",
	}
	for _, table := range Tables {
		entry, ok := p.Model(table.Service)
		if !ok || entry.UpstreamSHA256 != want[table.Service] {
			t.Errorf("%s: pin entry %+v", table.Service, entry)
		}
		if _, err := ReadExtract(ModelDir, p, table.Service); err != nil {
			t.Error(err)
		}
	}
	license, err := os.ReadFile(filepath.Join(ModelDir, "LICENSE"))
	if err != nil || !bytes.Contains(license, []byte("Apache License")) {
		t.Errorf("LICENSE: %v", err)
	}
	notice, err := os.ReadFile(filepath.Join(ModelDir, "NOTICE"))
	if err != nil || !bytes.Contains(notice, []byte("Amazon.com")) {
		t.Errorf("NOTICE: %v", err)
	}
}

// A changed extract no longer matches its pin.
func TestPinRefusesAChangedExtract(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{PinFile, "sts.json", "iam.json"} {
		data, err := os.ReadFile(filepath.Join(ModelDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if name == "sts.json" {
			data = append(data, ' ')
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, err := LoadPin(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadExtract(dir, p, "sts"); err == nil {
		t.Error("a changed extract was accepted")
	}
	if _, err := Render(dir, Tables); err == nil {
		t.Error("the generator accepted a changed extract")
	}
}

// The extracts carry no documentation or example trait, and nothing in the form of an AWS access key ID.
func TestExtractsHoldNoExampleKeys(t *testing.T) {
	key := regexp.MustCompile(`(AKIA|ASIA)[0-9A-Z]{16}`)
	for _, table := range Tables {
		data, err := os.ReadFile(filepath.Join(ModelDir, table.Service+".json"))
		if err != nil {
			t.Fatal(err)
		}
		if key.Match(data) {
			t.Errorf("%s.json holds an access key ID literal", table.Service)
		}
		for _, trait := range removedTraits {
			if bytes.Contains(data, []byte(`"`+trait+`"`)) {
				t.Errorf("%s.json still holds %s", table.Service, trait)
			}
		}
	}
}

// The extractor checks the upstream hash, drops the two traits at every depth, keeps the member order, and
// writes the same bytes every time.
func TestExtractIsDeterministic(t *testing.T) {
	upstream := []byte(`{"smithy":"2.0","shapes":{"b#Z":{"type":"string","traits":{"smithy.api#documentation":"d",` +
		`"smithy.api#length":{"min":1}}},"b#A":{"type":"structure","members":{"Z":{"target":"b#Z","traits":` +
		`{"smithy.api#examples":[{"x":1}],"smithy.api#required":{}}},"A":{"target":"b#Z"}}}}}`)
	sum := sha256.Sum256(upstream)
	first, err := Extract(upstream, hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}
	second, _ := Extract(upstream, hex.EncodeToString(sum[:]))
	if !bytes.Equal(first, second) {
		t.Error("two extracts differ")
	}
	text := string(first)
	if strings.Contains(text, "documentation") || strings.Contains(text, "examples") ||
		!strings.Contains(text, `"smithy.api#required": {}`) || strings.Index(text, `"b#Z"`) > strings.Index(text, `"b#A"`) ||
		strings.Index(text, `"Z": {`) > strings.Index(text, `"A": {`) || !strings.HasPrefix(text, "{\n  \"smithy\": \"2.0\",") {
		t.Errorf("extract =\n%s", text)
	}
	if _, err := Extract(upstream, strings.Repeat("0", 64)); err == nil {
		t.Error("a wrong upstream hash was accepted")
	}
}

// Every model operation of every table is a tool, excluded, or deferred, and no table names an operation the
// model lacks.
func TestTablesAreComplete(t *testing.T) {
	p, err := LoadPin(ModelDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range Tables {
		m, err := LoadTableModel(ModelDir, p, table)
		if err != nil {
			t.Fatal(err)
		}
		if err := Complete(table, m); err != nil {
			t.Error(err)
		}
		if err := table.Validate(); err != nil {
			t.Error(err)
		}
	}
	m := stsModel(t)
	missing := stsTable
	missing.Exclude = missing.Exclude[1:]
	if err := Complete(missing, m); err == nil || !strings.Contains(err.Error(), "AssumeRole") {
		t.Errorf("a missing operation: %v", err)
	}
	unknown := stsTable
	unknown.Defer = []Omission{{Operation: "InventedOperation", Reason: "test"}}
	if err := Complete(unknown, m); err == nil || !strings.Contains(err.Error(), "InventedOperation") {
		t.Errorf("an unknown operation: %v", err)
	}
	if _, err := Render(ModelDir, []Table{missing}); err == nil {
		t.Error("the generator accepted an incomplete table")
	}
}

func stsModel(t *testing.T) *Model {
	t.Helper()
	p, err := LoadPin(ModelDir)
	if err != nil {
		t.Fatal(err)
	}
	m, err := LoadTableModel(ModelDir, p, stsTable)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// withOperation returns the STS table with its first operation changed.
func withOperation(change func(*Operation)) Table {
	table := stsTable
	table.Operations = slices.Clone(stsTable.Operations)
	change(&table.Operations[0])
	return table
}

func TestTableRefusesWhatIsNotAllowed(t *testing.T) {
	for _, c := range []struct {
		name  string
		table Table
		want  string
	}{
		{"unknown policy check", withOperation(func(o *Operation) { o.PolicyCheck = "lenient-policy" }), "unknown policy_check"},
		{"unknown target check", withOperation(func(o *Operation) { o.TargetCheck = "region-ish" }), "unknown target_check"},
		{"write without confirm", withOperation(func(o *Operation) { o.Effect, o.Idempotency = "delete", "idempotent" }), "needs confirm"},
		{"read that is not safe", withOperation(func(o *Operation) { o.Idempotency = "idempotent" }), "a read is safe"},
		{"check without arguments", withOperation(func(o *Operation) { o.TargetCheck = "arn-account" }), "needs the arguments"},
		{"hidden without reason", withOperation(func(o *Operation) { o.HiddenOutputs = []Hidden{{Member: "Arn"}} }), "no reason"},
		{"unknown form", withOperation(func(o *Operation) { o.Fields = map[string]Field{"Arn": {Description: "d", Form: "arn-ish"}} }), "unknown form"},
		{"unknown effect", withOperation(func(o *Operation) { o.Effect = "read-write" }), "unknown effect"},
		{"excluded operation as a tool", func() Table {
			table := withOperation(func(*Operation) {})
			table.Operations = append(table.Operations, Operation{Operation: "AssumeRole", Object: "role", Action: "assume",
				Effect: "execute", Confirm: true, Idempotency: "non-idempotent", Title: "t", Description: "d",
				DataSensitivity: "s", TargetCheck: "none", PolicyCheck: "none"})
			return table
		}(), "listed in exclude and in operations"},
		{"exclusion without reason", func() Table {
			table := stsTable
			table.Exclude = append(slices.Clone(stsTable.Exclude[1:]), Omission{Operation: "AssumeRole"})
			return table
		}(), "has no reason"},
		{"wrong geltung", func() Table { table := stsTable; table.Geltung = Global; return table }(), "geltung"},
		{"unmapped segment", func() Table { table := stsTable; table.Segment = "security-token"; return table }(), "segment"},
	} {
		err := c.table.Validate()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an error with %q", c.name, err, c.want)
		}
		if _, err := Render(ModelDir, []Table{c.table, iamTable}); err == nil {
			t.Errorf("%s: the generator accepted the table", c.name)
		}
	}
}

// The checked-in generated files are exactly what the extracts and the tables produce.
func TestGeneratedFilesMatchModelAndTables(t *testing.T) {
	files, err := Render(ModelDir, Tables)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join("..", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is out of date; run go generate ./internal/provider/aws/...", name)
		}
		if !bytes.HasPrefix(got, []byte(Header)) || !regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`).Match(bytes.SplitN(got, []byte("\n"), 2)[0]) {
			t.Errorf("%s lacks the generated-code header", name)
		}
	}
	again, err := Render(ModelDir, Tables)
	if err != nil {
		t.Fatal(err)
	}
	for name := range files {
		if !bytes.Equal(files[name], again[name]) {
			t.Errorf("%s differs between two runs", name)
		}
	}
}

// The generator writes only files that carry its header.
func TestGenerateNeverOverwritesAHandWrittenFile(t *testing.T) {
	dir := t.TempDir()
	hand := []byte("package aws\n\n// hand-written\n")
	if err := os.WriteFile(filepath.Join(dir, ToolsFile), hand, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Generate(ModelDir, dir); err == nil {
		t.Fatal("Generate overwrote a hand-written file")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, ToolsFile)); !bytes.Equal(got, hand) {
		t.Error("the hand-written file changed")
	}
	if err := os.Remove(filepath.Join(dir, ToolsFile)); err != nil {
		t.Fatal(err)
	}
	if err := Generate(ModelDir, dir); err != nil {
		t.Fatal(err)
	}
	if err := Generate(ModelDir, dir); err != nil {
		t.Fatalf("Generate did not overwrite its own files: %v", err)
	}
}

// The generated STS tools keep the contract of the hand-written ones.
func TestSTSToolsKeepTheirContract(t *testing.T) {
	m := stsModel(t)
	byID := map[string]*tool{}
	for _, op := range stsTable.Operations {
		built, err := build(m, &stsTable, op)
		if err != nil {
			t.Fatal(err)
		}
		byID[built.id] = built
	}
	key := byID["aws.sts.access-key.get-info"]
	if key == nil || !strings.Contains(key.inputSchema, `"access_key_id":{"type":"string","minLength":16,"maxLength":128,"pattern":"^[\\w]*$"}`) ||
		!strings.Contains(key.inputSchema, `"required":["access_key_id"]`) || key.input.Fields[0].Name != "AccessKeyId" {
		t.Errorf("access key tool = %+v", key)
	}
	decode := byID["aws.sts.authorization-message.decode"]
	if decode == nil || !strings.Contains(decode.inputSchema, `"encoded_message":{"type":"string","minLength":1,"maxLength":10240}`) ||
		!strings.Contains(decode.outputSchema, `"decoded_message":{"type":"string","maxLength":32768},"truncated":{"type":"boolean"}`) ||
		decode.output.Fields[0].Shape.MaxLen != 32<<10 {
		t.Errorf("decode tool = %+v", decode)
	}
	caller := byID["aws.sts.caller-identity.get"]
	var names []string
	for _, f := range caller.fields {
		names = append(names, f.name)
	}
	if !slices.Equal(names, []string{"region", "user_id", "account", "arn"}) || len(caller.input.Fields) != 0 {
		t.Errorf("caller identity fields = %v", names)
	}
	// Every STS operation that returns credentials is excluded with a reason.
	for _, id := range m.operationIDs() {
		out := m.shapes.obj(m.shapes.obj(id).obj("output").str("target"))
		if out.obj("members").has("Credentials") || out.obj("members").has("WebIdentityToken") {
			if !slices.ContainsFunc(stsTable.Exclude, func(o Omission) bool { return o.Operation == shortName(id) }) {
				t.Errorf("%s returns a credential and is not excluded", shortName(id))
			}
		}
	}
}

// synthetic is a small awsQuery model with the constructs the STS and IAM tables do not use yet.
const synthetic = `{"smithy":"2.0","shapes":{
"com.example#Svc":{"type":"service","version":"2020-01-01","operations":[{"target":"com.example#ListThings"},
 {"target":"com.example#ListMarked"},{"target":"com.example#CreateThing"},{"target":"com.example#GetSecret"},
 {"target":"com.example#Old"}],
 "traits":{"aws.protocols#awsQuery":{},"smithy.api#xmlNamespace":{"uri":"https://example.com/doc/"}}},
"com.example#ListThings":{"type":"operation","input":{"target":"com.example#ListThingsRequest"},
 "output":{"target":"com.example#ListThingsResponse"},
 "traits":{"smithy.api#paginated":{"inputToken":"NextToken","outputToken":"NextToken","pageSize":"MaxResults","items":"Things"}}},
"com.example#ListThingsRequest":{"type":"structure","members":{
 "Prefix":{"target":"com.example#Prefix"},
 "Legacy":{"target":"smithy.api#String","traits":{"smithy.api#deprecated":{}}},
 "Filters":{"target":"com.example#FilterList"},
 "NextToken":{"target":"smithy.api#String"},
 "MaxResults":{"target":"com.example#Size"}}},
"com.example#Prefix":{"type":"string","traits":{"smithy.api#length":{"min":1,"max":64},"smithy.api#pattern":"^[\\u0021-\\u007E]+$"}},
"com.example#Size":{"type":"integer","traits":{"smithy.api#range":{"min":1,"max":100}}},
"com.example#FilterList":{"type":"list","member":{"target":"com.example#Filter"}},
"com.example#Filter":{"type":"structure","members":{"Name":{"target":"com.example#Kind","traits":{"smithy.api#required":{}}},
 "Values":{"target":"com.example#Values","traits":{"smithy.api#xmlFlattened":{},"smithy.api#xmlName":"Value"}}}},
"com.example#Kind":{"type":"enum","members":{"A":{"target":"smithy.api#Unit","traits":{"smithy.api#enumValue":"a"}},
 "B":{"target":"smithy.api#Unit","traits":{"smithy.api#enumValue":"b"}}}},
"com.example#Values":{"type":"list","member":{"target":"smithy.api#String"}},
"com.example#ListThingsResponse":{"type":"structure","members":{
 "Things":{"target":"com.example#ThingList","traits":{"smithy.api#required":{}}},
 "Tags":{"target":"com.example#TagMap"},
 "OldField":{"target":"smithy.api#String","traits":{"smithy.api#deprecated":{}}},
 "NextToken":{"target":"smithy.api#String"}}},
"com.example#ThingList":{"type":"list","member":{"target":"com.example#Thing"},"traits":{"smithy.api#length":{"max":50}}},
"com.example#Thing":{"type":"structure","members":{"ThingName":{"target":"smithy.api#String"},
 "CreateDate":{"target":"smithy.api#Timestamp"},"Count":{"target":"smithy.api#Long"}}},
"com.example#TagMap":{"type":"map","key":{"target":"smithy.api#String"},"value":{"target":"smithy.api#String"}},
"com.example#ListMarked":{"type":"operation","input":{"target":"com.example#ListMarkedRequest"},
 "output":{"target":"com.example#ListMarkedResponse"}},
"com.example#ListMarkedRequest":{"type":"structure","members":{"Marker":{"target":"smithy.api#String"},
 "MaxItems":{"target":"com.example#Size"}}},
"com.example#ListMarkedResponse":{"type":"structure","members":{"Names":{"target":"com.example#Values"},
 "IsTruncated":{"target":"smithy.api#Boolean"},"Marker":{"target":"smithy.api#String"}}},
"com.example#CreateThing":{"type":"operation","input":{"target":"com.example#CreateThingRequest"},
 "output":{"target":"com.example#CreateThingResponse"}},
"com.example#CreateThingRequest":{"type":"structure","members":{"ThingName":{"target":"smithy.api#String","traits":{"smithy.api#required":{}}},
 "ClientToken":{"target":"smithy.api#String","traits":{"smithy.api#idempotencyToken":{}}}}},
"com.example#CreateThingResponse":{"type":"structure","members":{"ThingId":{"target":"smithy.api#String"}}},
"com.example#GetSecret":{"type":"operation","input":{"target":"smithy.api#Unit"},
 "output":{"target":"com.example#GetSecretResponse"}},
"com.example#GetSecretResponse":{"type":"structure","members":{"Name":{"target":"smithy.api#String"},
 "Value":{"target":"com.example#SecretValue"},"SecretAccessKey":{"target":"smithy.api#String"}}},
"com.example#SecretValue":{"type":"string","traits":{"smithy.api#sensitive":{}}},
"com.example#Old":{"type":"operation","traits":{"smithy.api#deprecated":{}}}
}}`

func syntheticModel(t *testing.T) *Model {
	t.Helper()
	m, err := LoadModel([]byte(synthetic))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

var syntheticTable = Table{Service: "sts", Segment: "sts", Geltung: Regional}

func readOp(name string) Operation {
	return Operation{Operation: name, Object: "thing", Action: "list", Effect: "read", Idempotency: "safe",
		Title: "t", Description: "d", DataSensitivity: "s", TargetCheck: "none", PolicyCheck: "none"}
}

func argNames(tl *tool) []string {
	var out []string
	for _, a := range tl.args {
		out = append(out, a.name)
	}
	return out
}

func fieldNames(tl *tool) []string {
	var out []string
	for _, f := range tl.fields {
		out = append(out, f.name)
	}
	return out
}

// Paging comes from the paginated trait: the token and size members become cursor and page_size, the output
// token becomes next_cursor and has_more. Deprecated members are left out; nested shapes keep their traits.
func TestPagingFromThePaginatedTrait(t *testing.T) {
	op := readOp("ListThings")
	op.Arguments = map[string]Argument{"Prefix": {Description: "p"}, "Filters": {Description: "f"}}
	op.Fields = map[string]Field{"Things": {Description: "t"}, "Tags": {Description: "g"}}
	tl, err := build(syntheticModel(t), &syntheticTable, op)
	if err != nil {
		t.Fatal(err)
	}
	if *tl.pager != (awsquery.Pager{TokenIn: "NextToken", TokenOut: "~NextToken", SizeParam: "MaxResults", MaxSize: 100}) {
		t.Errorf("pager = %+v", *tl.pager)
	}
	if got := argNames(tl); !slices.Equal(got, []string{"prefix", "filters", "page_size", "cursor", "region"}) {
		t.Errorf("arguments = %v", got)
	}
	if got := fieldNames(tl); !slices.Equal(got, []string{"region", "things", "tags", "next_cursor", "has_more"}) {
		t.Errorf("fields = %v", got)
	}
	for _, want := range []string{`"prefix":{"type":"string","minLength":1,"maxLength":64,"pattern":"^[\\x{0021}-\\x{007E}]+$"}`,
		`"page_size":{"type":"integer","minimum":1,"maximum":100}`, `"enum":["a","b"]`, `"required":["name"]`} {
		if !strings.Contains(tl.inputSchema, want) {
			t.Errorf("input schema lacks %s: %s", want, tl.inputSchema)
		}
	}
	if strings.Contains(tl.inputSchema, "legacy") || strings.Contains(tl.outputSchema, "old_field") {
		t.Errorf("a deprecated member is offered: %s %s", tl.inputSchema, tl.outputSchema)
	}
	if !strings.Contains(tl.outputSchema, `"maxItems":50`) || !strings.Contains(tl.outputSchema, `"has_more":{"type":"boolean"}`) {
		t.Errorf("output schema = %s", tl.outputSchema)
	}
	filter := tl.input.Fields[1].Shape.Elem
	if filter.Fields[1].Name != "Value" || !filter.Fields[1].Flattened {
		t.Errorf("filter = %+v", filter.Fields[1])
	}
	if tl.sampleForm["Filters.member.1.Name"] != "a" || tl.sampleForm["Filters.member.1.Value.1"] != "a" ||
		tl.sampleForm["Prefix"] != "a" {
		t.Errorf("sample form = %v", tl.sampleForm)
	}
}

// Without a paginated trait, Marker with IsTruncated pages, and MaxItems is the page size.
func TestPagingFromMarkerAndIsTruncated(t *testing.T) {
	op := readOp("ListMarked")
	op.Fields = map[string]Field{"Names": {Description: "n"}}
	tl, err := build(syntheticModel(t), &syntheticTable, op)
	if err != nil {
		t.Fatal(err)
	}
	if *tl.pager != (awsquery.Pager{TokenIn: "Marker", TokenOut: "~Marker", Truncated: "~IsTruncated", SizeParam: "MaxItems", MaxSize: 100}) {
		t.Errorf("pager = %+v", *tl.pager)
	}
	if !strings.Contains(tl.sampleAnswer, "<IsTruncated>false</IsTruncated>") || strings.Contains(tl.sampleAnswer, "<Marker>") {
		t.Errorf("answer = %s", tl.sampleAnswer)
	}
}

// The real IAM ListUsers pages by Marker and IsTruncated with MaxItems up to 1000.
func TestIAMListUsersPaging(t *testing.T) {
	p, err := LoadPin(ModelDir)
	if err != nil {
		t.Fatal(err)
	}
	m, err := LoadTableModel(ModelDir, p, iamTable)
	if err != nil {
		t.Fatal(err)
	}
	op := readOp("ListUsers")
	op.Arguments = map[string]Argument{"PathPrefix": {Description: "p"}}
	op.Fields = map[string]Field{"Users": {Description: "u"}}
	tl, err := build(m, &Table{Service: "iam", Segment: "iam", Geltung: Global}, op)
	if err != nil {
		t.Fatal(err)
	}
	if *tl.pager != (awsquery.Pager{TokenIn: "Marker", TokenOut: "~Marker", Truncated: "~IsTruncated", SizeParam: "MaxItems", MaxSize: 1000}) {
		t.Errorf("pager = %+v", *tl.pager)
	}
	if slices.Contains(argNames(tl), "region") || slices.Contains(fieldNames(tl), "region") {
		t.Error("a global tool takes a region")
	}
}

// An idempotency token is generated by the tool: client_token is an optional argument and a result field.
func TestIdempotencyTokenBecomesClientToken(t *testing.T) {
	op := readOp("CreateThing")
	op.Effect, op.Confirm, op.Idempotency = "create", true, "idempotent"
	op.Arguments = map[string]Argument{"ThingName": {Description: "n"}}
	op.Fields = map[string]Field{"ThingId": {Description: "i"}}
	tl, err := build(syntheticModel(t), &syntheticTable, op)
	if err != nil {
		t.Fatal(err)
	}
	if !tl.hasToken || tl.input.Fields[1] != (awsquery.Field{Key: "client_token", Name: "ClientToken",
		Shape: tl.input.Fields[1].Shape, IdempotencyToken: true}) {
		t.Errorf("input = %+v", tl.input.Fields)
	}
	if !slices.Contains(argNames(tl), "client_token") || !slices.Contains(fieldNames(tl), "client_token") ||
		strings.Contains(tl.inputSchema, `"required":["thing_name","client_token"]`) {
		t.Errorf("arguments %v fields %v schema %s", argNames(tl), fieldNames(tl), tl.inputSchema)
	}
	if _, sent := tl.sampleForm["ClientToken"]; sent {
		t.Errorf("the expected form fixes the random token: %v", tl.sampleForm)
	}
}

// listed_only becomes requires_tool_allow_list, and confirm the required confirmation.
func TestListedOnlyAndConfirmReachTheDescriptor(t *testing.T) {
	op := readOp("CreateThing")
	op.Effect, op.Confirm, op.Idempotency, op.ListedOnly = "create", true, "idempotent", true
	op.Arguments = map[string]Argument{"ThingName": {Description: "n"}}
	op.Fields = map[string]Field{"ThingId": {Description: "i"}}
	tl, err := build(syntheticModel(t), &syntheticTable, op)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	renderTools(&b, []*tool{tl})
	if !strings.Contains(b.String(), "RequiresToolAllowList: true") || !strings.Contains(b.String(), "capability.ConfirmationRequired") ||
		!strings.Contains(b.String(), "capability.EffectCreate") {
		t.Errorf("rendered:\n%s", b.String())
	}
}

// What the model does not bind unambiguously, or what would show a credential, stops the generation and
// names the operation.
func TestBuildRefusesWhatItWouldHaveToGuess(t *testing.T) {
	m := syntheticModel(t)
	secret := readOp("GetSecret")
	secret.Fields = map[string]Field{"Name": {Description: "n"}, "Value": {Description: "v"}, "SecretAccessKey": {Description: "k"}}
	hiddenValue := secret
	hiddenValue.Fields = map[string]Field{"Name": {Description: "n"}, "SecretAccessKey": {Description: "k"}}
	hiddenValue.HiddenOutputs = []Hidden{{Member: "Value", Reason: "the secret itself"}}
	hiddenBoth := hiddenValue
	hiddenBoth.Fields = map[string]Field{"Name": {Description: "n"}}
	hiddenBoth.HiddenOutputs = append(slices.Clone(hiddenValue.HiddenOutputs), Hidden{Member: "SecretAccessKey", Reason: "a credential"})
	noDescription := readOp("ListMarked")
	unknownArgument := readOp("ListMarked")
	unknownArgument.Fields = map[string]Field{"Names": {Description: "n"}}
	unknownArgument.Arguments = map[string]Argument{"Marker": {Description: "m"}}
	accountCheck := readOp("ListMarked")
	accountCheck.Fields, accountCheck.AccountCheck = map[string]Field{"Names": {Description: "n"}}, true
	bucketOwner := readOp("ListMarked")
	bucketOwner.Fields, bucketOwner.TargetCheck, bucketOwner.TargetArgs = map[string]Field{"Names": {Description: "n"}}, "bucket-owner", []string{"Marker"}
	for _, c := range []struct {
		name string
		op   Operation
		want string
	}{
		{"sensitive output", secret, "GetSecret: Value: the member is sensitive"},
		{"credential-like output", hiddenValue, "GetSecret: SecretAccessKey: the member looks like a credential"},
		{"missing description", noDescription, "ListMarked: output member Names has no field entry"},
		{"argument of a paging member", unknownArgument, "argument entry Marker is not an offered input member"},
		{"account check elsewhere", accountCheck, "only STS GetCallerIdentity is the account check"},
		{"bucket owner on awsQuery", bucketOwner, "bucket-owner needs the S3 protocol package"},
		{"deprecated operation", readOp("Old"), "Old: the operation is deprecated"},
	} {
		if _, err := build(m, &syntheticTable, c.op); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
	if _, err := build(m, &syntheticTable, hiddenBoth); err != nil {
		t.Errorf("hidden credentials: %v", err)
	}
	c := converter{m: m, t: &tool{table: &syntheticTable, op: readOp("X")}, path: "P"}
	for _, bad := range []string{`^[\uD800-\uDFFF]$`, `^\u12$`, `(?<=a)b`} {
		if _, err := c.pattern(bad); err == nil {
			t.Errorf("pattern %q was accepted", bad)
		}
	}
	if got, err := c.pattern(`^[\\u0041\u0042]$`); err != nil || got != `^[\\u0041\x{0042}]$` {
		t.Errorf("pattern = %q, %v", got, err)
	}
}

// snake follows the names of the existing tools.
func TestSnake(t *testing.T) {
	for in, want := range map[string]string{"AccessKeyId": "access_key_id", "UserId": "user_id", "Arn": "arn",
		"SAMLAssertion": "saml_assertion", "MaxItems": "max_items", "DecodedMessage": "decoded_message"} {
		if got := snake(in); got != want {
			t.Errorf("snake(%q) = %q, want %q", in, got, want)
		}
	}
}

// No rendered literal is an access key ID: the generator splits it.
func TestLiteralsNeverHoldAnAccessKeyID(t *testing.T) {
	if got := lit(`{"k":"AKIA` + `IOSFODNN7EXAMPLE"}`); accessKeyForm.MatchString(got) {
		t.Errorf("lit = %s", got)
	}
	if got := lit("AKIA" + "IOSFODNN7EXAMPLE"); accessKeyForm.MatchString(got) {
		t.Errorf("lit = %s", got)
	}
}
