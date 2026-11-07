package googlemaps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-connect/internal/application"
	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

const (
	responseUUID = "de22bed8-7f52-44cb-8526-faceac57150a"
	originalHost = "X-Test-Original-Host"
)

// avRewrite sends the Address Validation host to the test server and tells it which host was asked for. It
// refuses every other host, so a request to the Places host would fail the test.
type avRewrite struct {
	target *url.URL
	base   http.RoundTripper
}

func (r avRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "addressvalidation.googleapis.com" || req.URL.Scheme != "https" {
		return nil, errors.New("unexpected host")
	}
	clone := req.Clone(req.Context())
	clone.Header.Set(originalHost, req.URL.Host)
	clone.URL.Scheme, clone.URL.Host, clone.Host = r.target.Scheme, r.target.Host, ""
	return r.base.RoundTrip(clone)
}

func newAddressEnvironment(t *testing.T, f *fakeGoogle) *environment {
	t.Helper()
	server := httptest.NewTLSServer(f)
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	previous := transport
	transport = avRewrite{target: target, base: server.Client().Transport}
	t.Cleanup(func() { transport = previous })
	t.Cleanup(limiters.Replace(keyValue, ratelimit.New(0, time.Now, func(context.Context, time.Duration) error { return nil })))
	reads := 0
	red := &redact.Redactor{}
	lookup := func(name string) string {
		reads++
		if name == keyEnv {
			return keyValue
		}
		return ""
	}
	cfg := coreConfig()
	cfg.Connections["change"] = config.Connection{Service: "maps", Credential: "maps-key",
		Effects: []config.Effect{config.EffectRead, config.EffectCreate}}
	return &environment{fake: f, core: application.New(registry(t), cfg, secret.NewWith(lookup, nil, nil, red), red),
		red: red, reads: &reads, t: t}
}

func (e *environment) confirmed(operation, connection, arguments string) (string, error) {
	response, err := e.core.Invoke(context.Background(), application.InvokeRequest{Operation: operation,
		Connection: connection, Arguments: json.RawMessage(arguments), Confirmed: true})
	return string(response.Result), err
}

const fullAddress = `{"address":{"region_code":"us","address_lines":["1600 Amphitheatre Pkwy","Mountain View, CA 94043"],` +
	`"postal_code":"94043","administrative_area":"CA","locality":"Mountain View","sublocality":"x","sorting_code":"y",` +
	`"language_code":"en"},"enable_usps_cass":true,"session_token":"abc-DEF_123",` +
	`"previous_response_id":"` + responseUUID + `","return_english_latin_address":true}`

const validationAnswer = `{"result":{"verdict":{"inputGranularity":"PREMISE","validationGranularity":"PREMISE",` +
	`"geocodeGranularity":"PREMISE","addressComplete":true,"hasInferredComponents":true,"possibleNextAction":"ACCEPT",` +
	`"surprise":1},"address":{"formattedAddress":"1600 Amphitheatre Pkwy, Mountain View, CA 94043-1351, USA",` +
	`"postalAddress":{"regionCode":"US","postalCode":"94043-1351","addressLines":["1600 Amphitheatre Pkwy"],` +
	`"recipients":["Someone"]},"addressComponents":[{"componentName":{"text":"1600","languageCode":"en"},` +
	`"componentType":"street_number","confirmationLevel":"CONFIRMED","inferred":false}],"missingComponentTypes":[],` +
	`"unresolvedTokens":["x"]},"geocode":{"location":{"latitude":37.42,"longitude":-122.08},` +
	`"plusCode":{"globalCode":"849VCWC8+R9"},"placeId":"ChIJ","placeTypes":["premise"]},` +
	`"metadata":{"business":true,"residential":false},"uspsData":{"dpvConfirmation":"Y","cassProcessed":true,` +
	`"dpvNoStatReasonCode":2,"standardizedAddress":{"firstAddressLine":"1600 AMPHITHEATRE PKWY","state":"CA",` +
	`"zipCode":"94043"},"errorMessage":"stop"},"englishLatinAddress":{"formattedAddress":"English"}},` +
	`"responseId":"` + responseUUID + `"}`

func TestAddressValidateSendsFullBodyAndShapesResult(t *testing.T) {
	env := newAddressEnvironment(t, &fakeGoogle{answer: reply(200, "application/json", validationAnswer)})
	result, err := env.invokeTool(addressValidateID, "read", fullAddress)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	requests := env.fake.recorded()
	if len(requests) != 1 {
		t.Fatalf("requests = %d", len(requests))
	}
	r := requests[0]
	if r.method != http.MethodPost || r.path != "/v1:validateAddress" || r.rawQuery != "" ||
		r.header.Get(originalHost) != "addressvalidation.googleapis.com" || r.header.Get("X-Goog-Api-Key") != keyValue ||
		r.header.Get("X-Goog-FieldMask") != "" || r.header.Get("Content-Type") != "application/json" {
		t.Errorf("request = %+v", r)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(r.body), &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	want := `{"address":{"addressLines":["1600 Amphitheatre Pkwy","Mountain View, CA 94043"],` +
		`"administrativeArea":"CA","languageCode":"en","locality":"Mountain View","postalCode":"94043","regionCode":"US",` +
		`"sortingCode":"y","sublocality":"x"},"enableUspsCass":true,"languageOptions":{"returnEnglishLatinAddress":true},` +
		`"previousResponseId":"` + responseUUID + `","sessionToken":"abc-DEF_123"}`
	if got, _ := json.Marshal(body); string(got) != want {
		t.Errorf("body = %s\nwant   %s", got, want)
	}
	for _, text := range []string{r.path, r.rawQuery, r.body, result} {
		if strings.Contains(text, keyValue) {
			t.Error("the key reached the URL, the body, or the result")
		}
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(result), &got)
	res := got["result"].(map[string]any)
	verdict := res["verdict"].(map[string]any)
	usps := res["uspsData"].(map[string]any)
	if got["responseId"] != responseUUID || verdict["possibleNextAction"] != "ACCEPT" || verdict["addressComplete"] != true ||
		verdict["surprise"] != nil || usps["dpvConfirmation"] != "Y" || usps["cassProcessed"] != true ||
		usps["dpvNoStatReasonCode"] != float64(2) ||
		usps["standardizedAddress"].(map[string]any)["zipCode"] != "94043" ||
		res["englishLatinAddress"] == nil || res["metadata"].(map[string]any)["business"] != true ||
		res["geocode"].(map[string]any)["placeId"] != "ChIJ" {
		t.Errorf("result = %s", result)
	}
	if _, kept := res["address"].(map[string]any)["postalAddress"].(map[string]any)["recipients"]; kept {
		t.Error("recipients, which Google does not document in the result, were kept")
	}
	components := res["address"].(map[string]any)["addressComponents"].([]any)
	if len(components) != 1 || components[0].(map[string]any)["confirmationLevel"] != "CONFIRMED" {
		t.Errorf("components = %v", components)
	}
}

func TestAddressValidateMinimalBodyHasOnlyTheAddress(t *testing.T) {
	env := newAddressEnvironment(t, &fakeGoogle{answer: reply(200, "application/json", `{"result":{},"responseId":"`+responseUUID+`"}`)})
	if _, err := env.invokeTool(addressValidateID, "read", `{"address":{"region_code":"CH","address_lines":["Bahnhofstrasse 1"]}}`); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if body := env.fake.recorded()[0].body; body != `{"address":{"addressLines":["Bahnhofstrasse 1"],"regionCode":"CH"}}` {
		t.Errorf("body = %s", body)
	}
}

func TestAddressValidateRefusesBeforeAnyIO(t *testing.T) {
	lines := `"address_lines":["a"]`
	for name, arguments := range map[string]string{
		"no address":           `{}`,
		"no region":            `{"address":{` + lines + `}}`,
		"bad region":           `{"address":{"region_code":"USA",` + lines + `}}`,
		"no lines":             `{"address":{"region_code":"US"}}`,
		"empty lines":          `{"address":{"region_code":"US","address_lines":[]}}`,
		"blank line":           `{"address":{"region_code":"US","address_lines":["  "]}}`,
		"control character":    `{"address":{"region_code":"US","address_lines":["a\u0000b"]}}`,
		"too many lines":       `{"address":{"region_code":"US","address_lines":["a","a","a","a","a","a","a","a","a","a","a"]}}`,
		"over 280 together":    `{"address":{"region_code":"US","address_lines":["` + strings.Repeat("a", 200) + `"],"locality":"` + strings.Repeat("b", 100) + `"}}`,
		"recipients":           `{"address":{"region_code":"US",` + lines + `,"recipients":["x"]}}`,
		"organization":         `{"address":{"region_code":"US",` + lines + `,"organization":"x"}}`,
		"cass outside US":      `{"address":{"region_code":"DE",` + lines + `},"enable_usps_cass":true}`,
		"bad previous id":      `{"address":{"region_code":"US",` + lines + `},"previous_response_id":"abc"}`,
		"bad session":          `{"address":{"region_code":"US",` + lines + `},"session_token":"a b"}`,
		"bad language":         `{"address":{"region_code":"US",` + lines + `,"language_code":"en us"}}`,
		"key":                  `{"address":{"region_code":"US",` + lines + `},"key":"x"}`,
		"unknown address part": `{"address":{"region_code":"US",` + lines + `,"revision":0}}`,
	} {
		env := newAddressEnvironment(t, &fakeGoogle{})
		if _, err := env.invokeTool(addressValidateID, "read", arguments); err == nil {
			t.Errorf("%s: no error", name)
		}
		if len(env.fake.recorded()) != 0 || *env.reads != 0 {
			t.Errorf("%s: %d requests, %d secret reads", name, len(env.fake.recorded()), *env.reads)
		}
	}
}

func TestAddressHandlersCheckArgumentsThemselves(t *testing.T) {
	resolved := &config.Resolved{BaseURL: apiRoot}
	for name, call := range map[string]func() error{
		"validate": func() error {
			_, err := handleAddressValidate(context.Background(), resolved, nil, nil,
				json.RawMessage(`{"address":{"region_code":"DE","address_lines":["a"]},"enable_usps_cass":true}`))
			return err
		},
		"feedback conclusion": func() error {
			_, err := handleAddressFeedback(context.Background(), resolved, nil, nil,
				json.RawMessage(`{"conclusion":"VALIDATION_CONCLUSION_UNSPECIFIED","response_id":"`+responseUUID+`"}`))
			return err
		},
		"feedback id": func() error {
			_, err := handleAddressFeedback(context.Background(), resolved, nil, nil,
				json.RawMessage(`{"conclusion":"UNUSED","response_id":"../x"}`))
			return err
		},
	} {
		var invalid *provider.InvalidRequestError
		if err := call(); !errors.As(err, &invalid) {
			t.Errorf("%s: err = %v, want an invalid request before any secret access", name, err)
		}
	}
}

func TestAddressToolsRisk(t *testing.T) {
	reg := registry(t)
	check := func(id string, want capability.Risk) {
		t.Helper()
		for _, d := range reg.Provider(Provider) {
			if d.ID != id {
				continue
			}
			if d.Risk != want {
				t.Errorf("%s risk = %+v", id, d.Risk)
			}
			for _, banned := range []string{"absent", "pages"} {
				if strings.Contains(d.Description, banned) {
					t.Errorf("%s description contains %q", id, banned)
				}
			}
			return
		}
		t.Errorf("%s is not registered", id)
	}
	check(addressValidateID, capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
		Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: "google-maps-address-data"})
	check(addressFeedbackID, capability.Risk{Effect: capability.EffectCreate, Idempotency: capability.IdempotencyNonIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: "google-maps-address-data"})
	for _, d := range reg.Provider(Provider) {
		if d.ID == addressValidateID {
			for _, want := range []string{"SKU", "5000", "1000", "attribution", "untrusted", "US and PR", "USPS", "artificially created"} {
				if !strings.Contains(d.Description, want) {
					t.Errorf("validate description lacks %q", want)
				}
			}
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if id == addressFeedbackID {
				t.Errorf("profile %s selects the feedback tool", profile.ID)
			}
		}
	}
}

func TestAddressFeedbackNeedsConfirmationAndSendsOneRequest(t *testing.T) {
	env := newAddressEnvironment(t, &fakeGoogle{answer: reply(200, "application/json", `{}`)})
	arguments := `{"conclusion":"USER_VERSION_USED","response_id":"` + responseUUID + `"}`
	_, err := env.invokeTool(addressFeedbackID, "change", arguments)
	var needed *application.ConfirmationRequiredError
	if !errors.As(err, &needed) {
		t.Fatalf("err = %v, want a confirmation request", err)
	}
	if len(env.fake.recorded()) != 0 || *env.reads != 0 {
		t.Fatalf("an unconfirmed call sent %d requests and read %d secrets", len(env.fake.recorded()), *env.reads)
	}
	result, err := env.confirmed(addressFeedbackID, "change", arguments)
	if err != nil {
		t.Fatalf("confirmed: %v", err)
	}
	requests := env.fake.recorded()
	if len(requests) != 1 {
		t.Fatalf("requests = %d", len(requests))
	}
	r := requests[0]
	if r.method != http.MethodPost || r.path != "/v1:provideValidationFeedback" || r.rawQuery != "" ||
		r.header.Get(originalHost) != "addressvalidation.googleapis.com" || r.header.Get("X-Goog-Api-Key") != keyValue ||
		r.body != `{"conclusion":"USER_VERSION_USED","responseId":"`+responseUUID+`"}` {
		t.Errorf("request = %+v", r)
	}
	if result != `{"accepted":true}` {
		t.Errorf("result = %s", result)
	}
}

func TestAddressFeedbackReportsUncertaintyWithoutRepeating(t *testing.T) {
	arguments := `{"conclusion":"UNUSED","response_id":"` + responseUUID + `"}`
	for name, answer := range map[string]func(http.ResponseWriter, *http.Request){
		"500":          reply(500, "application/json", errorObject(500, "INTERNAL", "X")),
		"503":          reply(503, "", ""),
		"504":          reply(504, "", ""),
		"not json":     reply(200, "text/html", "<html>"),
		"empty body":   reply(200, "application/json", ""),
		"unreadable":   reply(200, "application/json", "{"),
		"redirect":     reply(302, "", ""),
		"null body":    reply(200, "application/json", "null"),
		"wrong format": reply(200, "application/json", "[]"),
	} {
		env := newAddressEnvironment(t, &fakeGoogle{answer: answer})
		_, err := env.confirmed(addressFeedbackID, "change", arguments)
		if err == nil || !strings.Contains(err.Error(), "uncertain") || strings.Contains(err.Error(), detailCanary) ||
			strings.Contains(err.Error(), keyValue) {
			t.Errorf("%s: err = %v", name, err)
		}
		if n := len(env.fake.recorded()); n != 1 {
			t.Errorf("%s: %d requests, want exactly one", name, n)
		}
	}
	for name, cause := range map[string]error{
		"timeout": context.DeadlineExceeded, "reset": fmt.Errorf("read tcp: %w", syscall.ECONNRESET),
		"cancelled": context.Canceled, "unknown": errors.New("boom " + keyValue),
	} {
		env := newAddressEnvironment(t, &fakeGoogle{})
		transport = failingTransport{err: cause}
		_, err := env.confirmed(addressFeedbackID, "change", arguments)
		if err == nil || !strings.Contains(err.Error(), "uncertain") || strings.Contains(err.Error(), keyValue) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestAddressFeedbackKnownRefusalsAreNotUncertain(t *testing.T) {
	arguments := `{"conclusion":"UNUSED","response_id":"` + responseUUID + `"}`
	for name, tc := range map[string]struct {
		status int
		body   string
		class  provider.Class
	}{
		"400": {400, errorObject(400, "INVALID_ARGUMENT", "X"), provider.ClassInvalidResponse},
		"401": {401, errorObject(401, "UNAUTHENTICATED", "CREDENTIALS_MISSING"), provider.ClassAuth},
		"403": {403, errorObject(403, "PERMISSION_DENIED", "X"), provider.ClassPermission},
		"429": {429, errorObject(429, "RESOURCE_EXHAUSTED", "RATE_LIMIT_EXCEEDED"), provider.ClassRateLimited},
	} {
		env := newAddressEnvironment(t, &fakeGoogle{answer: reply(tc.status, "application/json", tc.body)})
		_, err := env.confirmed(addressFeedbackID, "change", arguments)
		if classOf(err) != tc.class || strings.Contains(err.Error(), "uncertain") {
			t.Errorf("%s: err = %v, class %q", name, err, classOf(err))
		}
	}
}

func TestAddressErrorFormsKeepProviderTextApart(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		class  provider.Class
	}{
		{401, errorObject(401, "UNAUTHENTICATED", "CREDENTIALS_MISSING"), provider.ClassAuth},
		{403, errorObject(403, "PERMISSION_DENIED", "SERVICE_DISABLED"), provider.ClassPermission},
		{429, errorObject(429, "RESOURCE_EXHAUSTED", "RATE_LIMIT_EXCEEDED"), provider.ClassRateLimited},
		{400, errorObject(400, "INVALID_ARGUMENT", "X"), provider.ClassInvalidResponse},
	} {
		env := newAddressEnvironment(t, &fakeGoogle{answer: reply(tc.status, "application/json", tc.body)})
		_, err := env.invokeTool(addressValidateID, "read", `{"address":{"region_code":"US","address_lines":["a"]}}`)
		if classOf(err) != tc.class {
			t.Errorf("%d: class = %q, err = %v", tc.status, classOf(err), err)
			continue
		}
		var failed *provider.Error
		_ = errors.As(err, &failed)
		if strings.Contains(failed.Message, detailCanary) || strings.Contains(failed.Message, keyValue) ||
			strings.Contains(err.Error(), keyValue) || strings.Contains(failed.Message, "uncertain") {
			t.Errorf("%d: message = %q", tc.status, failed.Message)
		}
		if tc.status == http.StatusForbidden && !strings.Contains(failed.Message, "Address Validation API") {
			t.Errorf("403 message does not name the API: %q", failed.Message)
		}
	}
}

func TestAddressValidateUnusableAnswers(t *testing.T) {
	for name, answer := range map[string]func(http.ResponseWriter, *http.Request){
		"not json":  reply(200, "text/plain", "x"),
		"no result": reply(200, "application/json", `{"responseId":"`+responseUUID+`"}`),
		"array":     reply(200, "application/json", `[]`),
	} {
		env := newAddressEnvironment(t, &fakeGoogle{answer: answer})
		_, err := env.invokeTool(addressValidateID, "read", `{"address":{"region_code":"US","address_lines":["a"]}}`)
		if classOf(err) != provider.ClassInvalidResponse || strings.Contains(err.Error(), "uncertain") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestAddressHostIsFixedAndTheKeyNeverTravelsElsewhere(t *testing.T) {
	// The Places host is refused by the test transport, so a request there fails instead of succeeding.
	env := newAddressEnvironment(t, &fakeGoogle{answer: reply(200, "application/json", `{"result":{}}`)})
	if _, err := env.invokeTool(addressValidateID, "read", `{"address":{"region_code":"US","address_lines":["a"]}}`); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if got := env.fake.recorded()[0].header.Get(originalHost); got != "addressvalidation.googleapis.com" {
		t.Errorf("host = %q", got)
	}
	if addressValidationRoot != "https://addressvalidation.googleapis.com" {
		t.Errorf("root = %q", addressValidationRoot)
	}
	var metadata = config.ProviderMetadata{}
	reg := registry(t)
	metadata, _ = reg.ProviderMetadata(Provider)
	if metadata.ValidateBaseURL("https://addressvalidation.googleapis.com") == nil {
		t.Error("a connection may not name the Address Validation host as its base URL")
	}
	// A connection with another base URL or a target is refused before the key is read.
	for _, resolved := range []*config.Resolved{
		{BaseURL: "https://addressvalidation.googleapis.com"}, {BaseURL: apiRoot, Target: "x"},
	} {
		if _, err := handleAddressFeedback(context.Background(), resolved, nil, nil,
			json.RawMessage(`{"conclusion":"UNUSED","response_id":"`+responseUUID+`"}`)); err == nil {
			t.Errorf("resolved %+v was accepted", resolved)
		}
	}
}
