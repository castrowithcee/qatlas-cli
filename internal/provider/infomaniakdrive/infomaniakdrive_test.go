package infomaniakdrive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The canaries stand for the token, for an account and drive outside the connection, and for a name a
// provider body should never leak. No test reaches Infomaniak: every request is answered by the package's
// own transport seam.
const (
	tokenValue = "canary0infomaniak0token0123456789abcdef01"
	tokenEnv   = "TEST_INFOMANIAK_TOKEN"

	ownAccount    int64 = 1001
	otherAccount  int64 = 1002
	ownDrive      int64 = 5001
	otherOwnDrive int64 = 5002
	foreignDrive  int64 = 9001
	foreignCanary       = "foreign-drive-canary-4b7e"
	rootID        int64 = 1
	childFolderID int64 = 42
	childFileID   int64 = 43
	storageHost         = "storage.foreign.invalid"
	uploadHost          = "upload.kdrive.infomaniak.com"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

// call is one request the adapter produced.
type call struct {
	method, host, path, auth string
	query                    url.Values
	body, contentType        string
}

// serve replaces the package transport for one test and records every request. handler answers requests to
// api.infomaniak.com; foreign, when not nil, answers requests to storageHost, so a redirect test can prove
// what a foreign host actually received.
func serve(t *testing.T, calls *[]call, handler func(*http.Request) (*http.Response, error),
	foreign func(*http.Request) (*http.Response, error)) {
	t.Helper()
	previous := transport
	transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := ""
		if request.Body != nil {
			raw, _ := io.ReadAll(request.Body)
			body = string(raw)
		}
		*calls = append(*calls, call{
			method: request.Method, host: request.URL.Host, path: request.URL.Path,
			auth: request.Header.Get("Authorization"), query: request.URL.Query(),
			body: body, contentType: request.Header.Get("Content-Type"),
		})
		if request.URL.Host == storageHost {
			if foreign == nil {
				return nil, errors.New("unexpected foreign request")
			}
			return foreign(request)
		}
		if request.URL.Host != apiHost && request.URL.Host != uploadHost {
			return nil, errors.New("unexpected host")
		}
		return handler(request)
	})
	t.Cleanup(func() { transport = previous })
	t.Cleanup(limiters.Replace(tokenValue, freeLimiter()))
}

// freeLimiter spaces nothing and never sleeps.
func freeLimiter() *ratelimit.Limiter {
	return ratelimit.New(0, time.Now, func(context.Context, time.Duration) error { return nil })
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status, Body: httpBody(body),
		Header: http.Header{"Content-Type": {"application/json"}},
	}
}

func httpBody(body string) *httpBodyCloser { return &httpBodyCloser{strings.NewReader(body)} }

type httpBodyCloser struct{ *strings.Reader }

func (httpBodyCloser) Close() error { return nil }

func envelopeSuccess(data string) string { return `{"result":"success","data":` + data + `}` }

func driveJSONOf(id, account int64, name string) string {
	return fmt.Sprintf(`{"id":%d,"name":%q,"size":1000000,"used_size":500000,"in_maintenance":false,"account_id":%d}`,
		id, name, account)
}

func fileJSONOf(id, parent int64, name, kind string) string {
	return fmt.Sprintf(`{"id":%d,"name":%q,"type":%q,"parent_id":%d,"status":"ok",`+
		`"created_at":1735689600,"last_modified_at":1735776000}`, id, name, kind, parent)
}

// ownershipPath is the drive detail endpoint every files.* call checks the named drive's account_id against
// before it ever reaches the file endpoint itself.
func ownershipPath(driveID int64) string { return fmt.Sprintf("/2/drive/%d", driveID) }

// withOwnership answers the ownership check of driveID as belonging to accountID, and delegates every other
// request to next. Every files.list, files.stat, and files.get fake in this package's tests must answer
// this request, exactly once per invocation, before its own endpoint is reached.
func withOwnership(driveID, accountID int64, next func(*http.Request) (*http.Response, error)) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == ownershipPath(driveID) {
			return jsonResponse(200, envelopeSuccess(driveJSONOf(driveID, accountID, "canary-drive"))), nil
		}
		return next(r)
	}
}

func resolver(red *redact.Redactor, reads *int) *secret.Resolver {
	return secret.NewWith(func(name string) string {
		if reads != nil {
			*reads++
		}
		if name == tokenEnv {
			return tokenValue
		}
		return ""
	}, nil, nil, red)
}

func registry(t *testing.T) *capability.Registry {
	t.Helper()
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatalf("Register() = %v", err)
	}
	return reg
}

func coreConfig() *config.Config {
	credential := config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleToken: tokenEnv}}
	accountTarget := "account/" + strconv.FormatInt(ownAccount, 10)
	return &config.Config{
		Version:     1,
		Services:    map[string]config.Service{"im": {Provider: Provider, BaseURL: apiRoot}},
		Credentials: map[string]config.Credential{"im-reader": credential},
		Connections: map[string]config.Connection{
			"account": {Service: "im", Credential: "im-reader", Target: accountTarget, Permissions: changePermissions},
			"drive": {Service: "im", Credential: "im-reader", Permissions: changePermissions,
				Targets: []string{accountTarget, "drive/" + strconv.FormatInt(ownDrive, 10)}},
			"readonly": {Service: "im", Credential: "im-reader", Targets: []string{accountTarget}},
			// The allow-list itself is local configuration and can name a drive that, in fact, belongs to
			// another account; only the live ownership check catches that.
			"driveforeign": {Service: "im", Credential: "im-reader", Permissions: changePermissions,
				Targets: []string{accountTarget, "drive/" + strconv.FormatInt(foreignDrive, 10)}},
			// Delete is never offered by permission alone: a trash tool must be listed to be offered.
			"trash": {Service: "im", Credential: "im-reader", Permissions: deletePermissions, Tools: trashTools,
				Targets: []string{accountTarget, "drive/" + strconv.FormatInt(ownDrive, 10)}},
			"trashforeign": {Service: "im", Credential: "im-reader", Permissions: deletePermissions, Tools: trashTools,
				Targets: []string{accountTarget, "drive/" + strconv.FormatInt(foreignDrive, 10)}},
			"links": {Service: "im", Credential: "im-reader", Permissions: linkPermissions, Tools: linkTools,
				Targets: []string{accountTarget, "drive/" + strconv.FormatInt(ownDrive, 10)}},
			"linksforeign": {Service: "im", Credential: "im-reader", Permissions: linkPermissions, Tools: linkTools,
				Targets: []string{accountTarget, "drive/" + strconv.FormatInt(foreignDrive, 10)}},
			// Without a tools list, no link change is offered, whatever permissions the connection holds.
			"linksunlisted": {Service: "im", Credential: "im-reader", Permissions: linkPermissions,
				Targets: []string{accountTarget, "drive/" + strconv.FormatInt(ownDrive, 10)}},
			"dropbox": {Service: "im", Credential: "im-reader", Permissions: linkPermissions, Tools: dropboxTools,
				Targets: []string{accountTarget, "drive/" + strconv.FormatInt(ownDrive, 10)}},
			"dropboxforeign": {Service: "im", Credential: "im-reader", Permissions: linkPermissions, Tools: dropboxTools,
				Targets: []string{accountTarget, "drive/" + strconv.FormatInt(foreignDrive, 10)}},
			// Without a tools list, no dropbox tool is offered, whatever permissions the connection holds.
			"dropboxunlisted": {Service: "im", Credential: "im-reader", Permissions: linkPermissions,
				Targets: []string{accountTarget, "drive/" + strconv.FormatInt(ownDrive, 10)}},
			"access": {Service: "im", Credential: "im-reader", Permissions: linkPermissions, Tools: accessTools,
				Targets: []string{accountTarget, "drive/" + strconv.FormatInt(ownDrive, 10)}},
			"accessforeign": {Service: "im", Credential: "im-reader", Permissions: linkPermissions, Tools: accessTools,
				Targets: []string{accountTarget, "drive/" + strconv.FormatInt(foreignDrive, 10)}},
			// Without a tools list, neither the access read nor an access change is offered.
			"accessunlisted": {Service: "im", Credential: "im-reader", Permissions: linkPermissions,
				Targets: []string{accountTarget, "drive/" + strconv.FormatInt(ownDrive, 10)}},
			"trashunlisted": {Service: "im", Credential: "im-reader", Permissions: deletePermissions,
				Targets: []string{accountTarget, "drive/" + strconv.FormatInt(ownDrive, 10)}},
		},
	}
}

var deletePermissions = []config.Permission{config.PermissionRead, config.PermissionUpdate, config.PermissionDelete}

var linkPermissions = []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate,
	config.PermissionDelete}

var linkTools = []string{linksGet.ID, linksList.ID, linksDelete.ID}

var dropboxTools = []string{dropboxGet.ID, dropboxCreate.ID, dropboxUpdate.ID, dropboxDelete.ID}

var accessTools = []string{accessGet.ID, accessGrant.ID, accessUpdate.ID, accessRevoke.ID}

var trashTools = []string{filesTrash.ID, trashListDescriptor.ID, trashRestore.ID, trashDelete.ID, trashEmpty.ID}

// changePermissions lets a connection use the folder and file changes; "readonly" keeps the default.
var changePermissions = []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate}

type environment struct {
	core  *application.Core
	red   *redact.Redactor
	reads *int
}

func newEnvironment(t *testing.T, calls *[]call, handler func(*http.Request) (*http.Response, error),
	foreign func(*http.Request) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler, foreign)
	reads := 0
	red := &redact.Redactor{}
	return &environment{core: application.New(registry(t), coreConfig(), resolver(red, &reads), red), red: red, reads: &reads}
}

func (e *environment) invoke(operation, connection, arguments string) (string, error) {
	response, err := e.core.Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: connection, Arguments: json.RawMessage(arguments),
	})
	return string(response.Result), err
}

// invokeConfirmed is invoke with the confirmation a change needs.
func (e *environment) invokeConfirmed(operation, connection, arguments string) (string, error) {
	response, err := e.core.Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: connection, Arguments: json.RawMessage(arguments), Confirmed: true,
	})
	return string(response.Result), err
}

func classOf(err error) provider.Class {
	var providerErr *provider.Error
	if errors.As(err, &providerErr) {
		return providerErr.Class
	}
	return ""
}

func isInvalidRequest(err error) bool {
	var invalid *provider.InvalidRequestError
	return errors.As(err, &invalid)
}

func TestRegisterPublishesMetadataAndTools(t *testing.T) {
	reg := registry(t)
	metadata, ok := reg.ProviderMetadata(Provider)
	if !ok || metadata.Name != "Infomaniak kDrive" || metadata.DefaultBaseURL != apiRoot ||
		len(metadata.SecretRoles) != 1 || metadata.SecretRoles[0].Name != roleToken || !metadata.Target.Required ||
		!metadata.Target.Multiple || len(metadata.Target.Kinds) != 2 {
		t.Fatalf("metadata = %+v", metadata)
	}
	if len(metadata.Tools) != 26 {
		t.Fatalf("tools = %+v, want 26", metadata.Tools)
	}
	if want := []config.Permission{config.PermissionRead}; !reflect.DeepEqual(metadata.DefaultPermissions, want) {
		t.Fatalf("default permissions = %v, want read only", metadata.DefaultPermissions)
	}
	if len(metadata.Profiles) != 3 || metadata.Profiles[0].ID != "read" || len(metadata.Profiles[0].Tools) != 6 ||
		metadata.Profiles[1].ID != "write" || len(metadata.Profiles[1].Tools) != 9 ||
		metadata.Profiles[2].ID != "upload" || len(metadata.Profiles[2].Tools) != 6 {
		t.Fatalf("profiles = %+v", metadata.Profiles)
	}
}

// A connection needs exactly one account target; zero, two, or a malformed one are all refused before any
// connection can be used, and the drive allow-list is optional and repeatable.
func TestTargetValidation(t *testing.T) {
	cases := []struct {
		name    string
		values  []string
		wantErr bool
	}{
		{"one account", []string{"account/1"}, false},
		{"account and drives", []string{"account/1", "drive/2", "drive/3"}, false},
		{"no account", []string{"drive/2"}, true},
		{"two accounts", []string{"account/1", "account/2"}, true},
		{"duplicate drive", []string{"account/1", "drive/2", "drive/2"}, true},
		{"malformed account", []string{"account/abc"}, true},
		{"leading zero", []string{"account/01"}, true},
		{"free-form value", []string{"1"}, true},
		{"path traversal attempt", []string{"account/../1"}, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseAllowlist(tt.values)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseAllowlist(%v) = %v, wantErr=%t", tt.values, err, tt.wantErr)
			}
		})
	}
}

// resolvedConnection builds a *config.Resolved for the package-level TestConnection function, the same way
// this repository's other providers test it directly, below the application core.
func resolvedConnection(targets ...string) *config.Resolved {
	return &config.Resolved{Name: "im", Provider: Provider, BaseURL: apiRoot, Targets: targets, Credential: "im-reader",
		Secrets: config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleToken: tokenEnv}}}
}

// TestConnection proves the token is accepted with the smallest safe read, and reports the classified
// failure of a rejected token without ever leaking the request body.
func TestTestConnection(t *testing.T) {
	var calls []call
	serve(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/2/drive" {
			return jsonResponse(200, envelopeSuccess("[]")), nil
		}
		return jsonResponse(404, `{"result":"error"}`), nil
	}, nil)
	red := &redact.Redactor{}
	target := "account/" + strconv.FormatInt(ownAccount, 10)
	class, err := TestConnection(context.Background(), resolvedConnection(target), resolver(red, nil), red)
	if err != nil || class != provider.ClassOK {
		t.Fatalf("TestConnection() = %q, %v, want ok", class, err)
	}
	if len(calls) != 1 || calls[0].query.Get("account_id") != strconv.FormatInt(ownAccount, 10) {
		t.Fatalf("calls = %+v", calls)
	}

	calls = nil
	serve(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(401, `{"result":"error"}`), nil
	}, nil)
	class, err = TestConnection(context.Background(), resolvedConnection(target), resolver(red, nil), red)
	if err != nil || class != provider.ClassAuth {
		t.Fatalf("TestConnection() with a rejected token = %q, %v, want auth", class, err)
	}
}

// A drive outside the connection's allow-list is refused as an invalid request before any request reaches
// Infomaniak, and a permission failure Infomaniak itself reports is classified and never echoes its body.
func TestFilesListRefusesForeignDriveAndClassifiesPermissionFailure(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	}, nil)
	_, err := env.invoke(filesList.ID, "drive", fmt.Sprintf(`{"drive_id":%d}`, foreignDrive))
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an invalid request for a drive outside the allow-list", err)
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %+v, want none before the foreign drive was refused", calls)
	}
	if *env.reads != 0 {
		t.Fatalf("secret reads = %d, want none for a refused drive", *env.reads)
	}

	calls = nil
	env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(403, `{"result":"error","error":{"code":"forbidden_error","description":`+
			`"`+foreignCanary+`"}}`), nil
	}, nil)
	_, err = env.invoke(filesList.ID, "drive", fmt.Sprintf(`{"drive_id":%d}`, ownDrive))
	if classOf(err) != provider.ClassPermission {
		t.Fatalf("class = %q, want permission", classOf(err))
	}
	if err != nil && strings.Contains(err.Error(), foreignCanary) {
		t.Fatalf("error %v leaked the provider body", err)
	}
}

// A rate-limited response is classified and holds this connection's limiter, so an immediately following
// request waits instead of retrying the exhausted budget at once.
func TestRateLimitIsClassifiedAndHeld(t *testing.T) {
	var calls []call
	first := true
	env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
		if first {
			first = false
			response := jsonResponse(429, `{"result":"error"}`)
			response.Header.Set("Retry-After", "5")
			return response, nil
		}
		return jsonResponse(200, envelopeSuccess("[]")), nil
	}), nil)
	_, err := env.invoke(filesList.ID, "drive", fmt.Sprintf(`{"drive_id":%d}`, ownDrive))
	if classOf(err) != provider.ClassRateLimited {
		t.Fatalf("class = %q, want rate-limited", classOf(err))
	}
	if err != nil && !strings.Contains(err.Error(), "60 requests per minute") {
		t.Fatalf("error %v does not name the confirmed 60 requests per minute limit", err)
	}
	// The retry-after Infomaniak reported held this connection's own limiter, proven by the ratelimit
	// package's own tests; a second call on the same environment succeeds once the fake server allows it,
	// showing the connection recovers rather than staying rate-limited forever.
	result, err := env.invoke(filesList.ID, "drive", fmt.Sprintf(`{"drive_id":%d}`, ownDrive))
	if err != nil {
		t.Fatalf("invoke() after the rate limit = %v, want it to succeed once Infomaniak answers again", err)
	}
	var page FolderPage
	if err := json.Unmarshal([]byte(result), &page); err != nil || page.DriveID != ownDrive {
		t.Fatalf("result = %s, %v", result, err)
	}
}

// A redirect from the content download to a foreign storage host is followed once, but the Authorization
// header never reaches that host; a redirect to a non-https location is refused outright.
func TestDownloadRedirectNeverForwardsTheCredentialToAForeignHost(t *testing.T) {
	var calls []call
	var foreignAuth string
	env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/download") {
			return &http.Response{StatusCode: http.StatusFound, Header: http.Header{
				"Location": {"https://" + storageHost + "/blob/1"},
			}, Body: httpBody("")}, nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	}), func(r *http.Request) (*http.Response, error) {
		foreignAuth = r.Header.Get("Authorization")
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/plain"}},
			Body: httpBody("file content")}, nil
	})
	result, err := env.invoke(filesGet.ID, "drive", fmt.Sprintf(`{"drive_id":%d,"file_id":%d}`, ownDrive, childFileID))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if foreignAuth != "" {
		t.Fatalf("the Authorization header reached the foreign host: %q", foreignAuth)
	}
	var content Content
	if err := json.Unmarshal([]byte(result), &content); err != nil || content.ContentBase64 == "" {
		t.Fatalf("result = %s, %v", result, err)
	}

	// A redirect to plain http is refused outright, never followed.
	calls = nil
	env = newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusFound,
			Header: http.Header{"Location": {"http://" + storageHost + "/blob/1"}}, Body: httpBody("")}, nil
	}), nil)
	_, err = env.invoke(filesGet.ID, "drive", fmt.Sprintf(`{"drive_id":%d,"file_id":%d}`, ownDrive, childFileID))
	if err == nil {
		t.Fatal("a redirect to plain http was followed")
	}
}
