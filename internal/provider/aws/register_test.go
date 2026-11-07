package aws

import (
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/endpoints"
)

var wantTools = []string{
	"aws.sts.access-key.get-info", "aws.sts.authorization-message.decode", "aws.sts.caller-identity.get",
}

func TestRegisterPublishesMetadataAndTools(t *testing.T) {
	reg := registered(t)
	metadata, ok := reg.ProviderMetadata(Provider)
	if !ok || metadata.Name != "Amazon Web Services" || !metadata.ServiceLevel || metadata.DocPage != "aws.md" {
		t.Fatalf("metadata = %+v", metadata)
	}
	var ids []string
	for _, tool := range metadata.Tools {
		ids = append(ids, tool.ID)
		if tool.Effect != config.EffectRead || tool.RequiresToolAllowList || tool.Group != "sts" {
			t.Errorf("tool %+v, want an open read of the sts group", tool)
		}
	}
	if !reflect.DeepEqual(ids, wantTools) {
		t.Errorf("tools = %v, want %v", ids, wantTools)
	}
	if !reflect.DeepEqual(metadata.SupportedEffects, []config.Effect{config.EffectRead}) ||
		!reflect.DeepEqual(metadata.DefaultEffects, []config.Effect{config.EffectRead}) {
		t.Errorf("effects = %v / %v, want read only", metadata.SupportedEffects, metadata.DefaultEffects)
	}
	profile, ok := metadata.RecommendedProfile()
	if !ok || profile.ID != "read" || !reflect.DeepEqual(append([]string{}, profile.Tools...), []string{
		"aws.sts.caller-identity.get", "aws.sts.access-key.get-info", "aws.sts.authorization-message.decode"}) {
		t.Errorf("profile = %+v", profile)
	}
	if !metadata.Target.Required || !metadata.Target.Multiple || metadata.Target.Wildcard != "" {
		t.Errorf("target = %+v, want required, multiple, and no wildcard", metadata.Target)
	}
}

func TestSecretRoles(t *testing.T) {
	reg := registered(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	got := map[string]bool{}
	var order []string
	for _, role := range metadata.SecretRoles {
		got[role.Name] = role.Optional
		order = append(order, role.Name)
	}
	want := map[string]bool{"access_key_id": false, "secret_access_key": false, "session_token": true,
		"role_arn": true, "external_id": true}
	if !reflect.DeepEqual(got, want) || len(order) != 5 {
		t.Errorf("roles = %v, want %v", got, want)
	}
}

func TestBaseURLIsFixed(t *testing.T) {
	reg := registered(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, good := range []string{"https://aws.amazon.com", "https://aws.amazon.com/"} {
		if err := metadata.ValidateBaseURL(good); err != nil {
			t.Errorf("ValidateBaseURL(%q) = %v", good, err)
		}
	}
	for _, bad := range []string{"https://sts.amazonaws.com", "http://aws.amazon.com", "https://aws.amazon.com/x",
		"https://evil.example", (&url.URL{Scheme: "https", User: url.UserPassword("u", "p"), Host: "aws.amazon.com"}).String(), "https://aws.amazon.com?x=1"} {
		if err := metadata.ValidateBaseURL(bad); err == nil {
			t.Errorf("ValidateBaseURL(%q) = nil, want a refusal", bad)
		}
	}
}

// Every tool is a credential-free read with the full risk and the strict schema. The AWS actions that return a
// credential or token are no tools, and the actions the tools send are exactly the three STS reads.
func TestSTSToolsAreTheCredentialFreeActions(t *testing.T) {
	reg := registered(t)
	for _, d := range reg.Provider(Provider) {
		want := capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
			Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: d.Risk.DataSensitivity}
		if d.Risk != want || d.Risk.DataSensitivity == "" {
			t.Errorf("%s risk = %+v", d.ID, d.Risk)
		}
		if !strings.Contains(string(d.InputSchema), `"additionalProperties":false`) {
			t.Errorf("%s input schema is open", d.ID)
		}
		for _, banned := range []string{"assume", "session", "federation", "token", "root", "delegated", "saml", "web-identity"} {
			if strings.Contains(d.ID, banned) {
				t.Errorf("%s names a credential action (%s)", d.ID, banned)
			}
		}
	}
	for _, action := range excludedSTSActions {
		for _, d := range reg.Provider(Provider) {
			if strings.EqualFold(strings.ReplaceAll(d.ID, "-", ""), "aws.sts."+action) {
				t.Errorf("%s is the excluded action %s", d.ID, action)
			}
		}
	}
	if len(excludedSTSActions) != 8 {
		t.Errorf("the model has eight credential actions, the list holds %d", len(excludedSTSActions))
	}

	f := direct(func(r sent, _ int) reply {
		switch r.action {
		case "GetAccessKeyInfo":
			return ok(`<GetAccessKeyInfoResponse><GetAccessKeyInfoResult><Account>210987654321</Account></GetAccessKeyInfoResult></GetAccessKeyInfoResponse>`)
		case "DecodeAuthorizationMessage":
			return ok(`<DecodeAuthorizationMessageResponse><DecodeAuthorizationMessageResult><DecodedMessage>{}</DecodedMessage></DecodeAuthorizationMessageResult></DecodeAuthorizationMessageResponse>`)
		}
		return reply{status: 500}
	})
	e := newEnv(t, f, baseValues())
	for _, call := range [][2]string{
		{"aws.sts.caller-identity.get", `{}`},
		{"aws.sts.access-key.get-info", `{"access_key_id":"` + testKeyID + `"}`},
		{"aws.sts.authorization-message.decode", `{"encoded_message":"abc"}`},
	} {
		if _, err := e.invoke(call[0], call[1]); err != nil {
			t.Fatalf("%s: %v", call[0], err)
		}
	}
	allowed := map[string]bool{"GetCallerIdentity": true, "GetAccessKeyInfo": true, "DecodeAuthorizationMessage": true}
	for _, action := range f.actions() {
		if !allowed[action] {
			t.Errorf("the tools sent %s", action)
		}
	}
}

// Every tool's service segment has an endpoint and a segment table entry; a tool of a service without one
// would be a registration error.
func TestToolSegmentsAreMapped(t *testing.T) {
	reg := registered(t)
	for _, d := range reg.Provider(Provider) {
		segment := strings.Split(d.ID, ".")[1]
		if _, err := endpoints.ModelOf(segment); err != nil {
			t.Errorf("%s: segment %q is not in the segment table: %v", d.ID, segment, err)
		}
		if _, err := endpoints.IsGlobal(endpoints.Service(segment)); err != nil {
			t.Errorf("%s: segment %q has no endpoint: %v", d.ID, segment, err)
		}
	}
}

var sessionNameForm = regexp.MustCompile(`^[\w+=,.@-]{2,64}$`)

func TestSessionNameAndAgent(t *testing.T) {
	name := sessionName("prod")
	if !sessionNameForm.MatchString(name) || !strings.HasPrefix(name, "qatlas-") || name == sessionName("other") ||
		strings.Contains(name, "prod") {
		t.Errorf("sessionName = %q", name)
	}
	for in, want := range map[string]string{
		"1.2.3": "qatlas/1.2.3", "v0.4.0-rc.1+build": "qatlas/v0.4.0-rc.1+build", "dev": "qatlas/dev",
		"": "qatlas/unknown", "1 2\r\nX: y": "qatlas/12Xy", strings.Repeat("9", 65): "qatlas/unknown",
	} {
		if got := agentFor(in); got != want {
			t.Errorf("agentFor(%q) = %q, want %q", in, got, want)
		}
	}
}
