package telegram

import (
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

func TestBusinessProfileToolsSendTheExactBodies(t *testing.T) {
	for name, c := range map[string]struct {
		tool string
		args map[string]any
		want string
	}{
		"name":              {"setname", map[string]any{"business": "b1", "first_name": "Ann", "last_name": "Ö"}, `setBusinessAccountName {"business_connection_id":"b1","first_name":"Ann","last_name":"Ö"}`},
		"name without last": {"setname", map[string]any{"business": "b1", "first_name": "Ann"}, `setBusinessAccountName {"business_connection_id":"b1","first_name":"Ann"}`},
		"name empty last":   {"setname", map[string]any{"business": "b1", "first_name": "Ann", "last_name": ""}, `setBusinessAccountName {"business_connection_id":"b1","first_name":"Ann"}`},
		"username":          {"setusername", map[string]any{"business": "b1", "username": "Ann_01"}, `setBusinessAccountUsername {"business_connection_id":"b1","username":"Ann_01"}`},
		"username removal":  {"setusername", map[string]any{"business": "b1"}, `setBusinessAccountUsername {"business_connection_id":"b1"}`},
		"username empty":    {"setusername", map[string]any{"business": "b1", "username": ""}, `setBusinessAccountUsername {"business_connection_id":"b1"}`},
		"bio":               {"setbio", map[string]any{"business": "b1", "bio": "Hi"}, `setBusinessAccountBio {"business_connection_id":"b1","bio":"Hi"}`},
		"bio removal":       {"setbio", map[string]any{"business": "b1", "bio": ""}, `setBusinessAccountBio {"business_connection_id":"b1"}`},
		"bio max runes":     {"setbio", map[string]any{"business": "b1", "bio": strings.Repeat("ä", 140)}, `setBusinessAccountBio {"business_connection_id":"b1","bio":"` + strings.Repeat("ä", 140) + `"}`},
	} {
		t.Run(name, func(t *testing.T) {
			env := newBusinessEnv("-100", "business/b1")
			got, err := env.run(c.tool, c.args)
			if err != nil {
				t.Fatal(err)
			}
			if m, _ := got.(map[string]any); m["set"] != true {
				t.Errorf("result = %v", got)
			}
			if len(env.calls) != 1 || env.calls[0] != c.want {
				t.Fatalf("calls = %q, want %q", env.calls, c.want)
			}
		})
	}
}

func TestBusinessProfileToolsRejectBeforeSecretAccess(t *testing.T) {
	valid := map[string]map[string]any{
		"setname":     {"business": "b1", "first_name": "Ann"},
		"setusername": {"business": "b1", "username": "ann_x"},
		"setbio":      {"business": "b1", "bio": "x"},
	}
	with := func(tool string, extra map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range valid[tool] {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	both := []string{"-100", "business/b1", "business/b2"}
	type tc struct {
		targets []string
		tool    string
		args    map[string]any
	}
	cases := map[string]tc{
		"name empty first":    {both, "setname", with("setname", map[string]any{"first_name": ""})},
		"name first too long": {both, "setname", with("setname", map[string]any{"first_name": strings.Repeat("a", 65)})},
		"name last too long":  {both, "setname", with("setname", map[string]any{"last_name": strings.Repeat("ä", 65)})},
		"name missing first":  {both, "setname", map[string]any{"business": "b1"}},
		"username short":      {both, "setusername", with("setusername", map[string]any{"username": "abcd"})},
		"username long":       {both, "setusername", with("setusername", map[string]any{"username": strings.Repeat("a", 33)})},
		"username at sign":    {both, "setusername", with("setusername", map[string]any{"username": "@ann_x"})},
		"username dash":       {both, "setusername", with("setusername", map[string]any{"username": "ann-xy"})},
		"username non ascii":  {both, "setusername", with("setusername", map[string]any{"username": "annä_x"})},
		"bio too long":        {both, "setbio", with("setbio", map[string]any{"bio": strings.Repeat("ä", 141)})},
	}
	for _, tool := range []string{"setname", "setusername", "setbio"} {
		cases[tool+" no business target"] = tc{[]string{"-100", "bot"}, tool, valid[tool]}
		cases[tool+" foreign business"] = tc{both, tool, with(tool, map[string]any{"business": "FOREIGNID"})}
		cases[tool+" invalid business"] = tc{both, tool, with(tool, map[string]any{"business": "a/b"})}
		cases[tool+" empty business"] = tc{both, tool, with(tool, map[string]any{"business": ""})}
		cases[tool+" unknown argument"] = tc{both, tool, with(tool, map[string]any{"text": "x"})}
		cases[tool+" passthrough"] = tc{both, tool, with(tool, map[string]any{"business_connection_id": "b2"})}
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			env := newBusinessEnv(c.targets...)
			if _, err := env.run(c.tool, c.args); err == nil {
				t.Fatal("accepted")
			}
			if env.lookups != 0 || len(env.calls) != 0 {
				t.Fatalf("secret lookups = %d, requests = %q", env.lookups, env.calls)
			}
			for _, leak := range []string{"FOREIGNID", "b2", testToken} {
				if strings.Contains(err2(env, c.tool, c.args), leak) {
					t.Fatalf("error leaks %q", leak)
				}
			}
		})
	}
}

func err2(env *businessEnv, tool string, args map[string]any) string {
	_, err := env.run(tool, args)
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestBusinessProfileMutationsSendOneRequestAndReportUncertainty(t *testing.T) {
	for tool, args := range map[string]map[string]any{
		"setname":     {"business": "b1", "first_name": "Ann"},
		"setusername": {"business": "b1", "username": "ann_x"},
		"setbio":      {"business": "b1", "bio": "x"},
	} {
		for name, setup := range map[string]func(*businessEnv){
			"500":     func(e *businessEnv) { e.status = 500 },
			"502":     func(e *businessEnv) { e.status = 502 },
			"timeout": func(e *businessEnv) { e.fail = &timeoutError{} },
			"false":   func(e *businessEnv) { e.body = `{"ok":true,"result":false}` },
		} {
			env := newBusinessEnv("business/b1")
			setup(env)
			_, err := env.run(tool, args)
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") ||
				strings.Contains(err.Error(), "SECRETDESC") || len(env.calls) != 1 {
				t.Errorf("%s %s: err = %v, calls = %d", tool, name, err, len(env.calls))
			}
		}
	}
}

func TestBusinessProfileDescriptors(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		d    capability.Descriptor
		list bool
	}{{businessSetName, false}, {businessSetUsername, true}, {businessSetBio, false}} {
		r := c.d.Risk
		if r.Effect != capability.EffectUpdate || r.Idempotency != capability.IdempotencyIdempotent ||
			r.Confirmation != capability.ConfirmationRequired || !r.OpenWorld ||
			r.DataSensitivity != "telegram-business-content" || c.d.RequiresToolAllowList != c.list ||
			c.d.Group != groupBusiness {
			t.Errorf("%s = %+v", c.d.ID, c.d)
		}
		if _, _, ok := reg.Lookup(c.d.ID); !ok {
			t.Errorf("%s is not registered", c.d.ID)
		}
	}
}
