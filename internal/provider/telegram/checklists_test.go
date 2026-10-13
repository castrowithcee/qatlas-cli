package telegram

import (
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

func validChecklist() map[string]any {
	return map[string]any{"title": "Packing", "others_can_add_tasks": true,
		"tasks": []map[string]any{{"id": 1, "text": "Passport"}, {"id": 2, "text": "Charger"}}}
}

func checklistArgsFor(tool string, checklist any, extra map[string]any) map[string]any {
	args := map[string]any{"business": "b1", "chat": businessRef("business/b1", "777"), "checklist": checklist}
	if tool == "checklistedit" {
		args["message_id"] = 91
	}
	for k, v := range extra {
		args[k] = v
	}
	return args
}

func TestChecklistToolsSendTheExactBodies(t *testing.T) {
	const list = `{"title":"Packing","tasks":[{"id":1,"text":"Passport"},{"id":2,"text":"Charger"}],"others_can_add_tasks":true}`
	env := newBusinessEnv("-100", "business/b1")
	env.body = `{"ok":true,"result":{"message_id":5,"date":1700000000,"chat":{"id":777},"checklist":{"title":"SECRETTITLE"}}}`
	got, err := env.run("checklistsend", checklistArgsFor("checklistsend", validChecklist(),
		map[string]any{"disable_notification": true, "protect_content": true}))
	if err != nil {
		t.Fatal(err)
	}
	want := `sendChecklist {"business_connection_id":"b1","chat_id":777,"checklist":` + list +
		`,"disable_notification":true,"protect_content":true}`
	if len(env.calls) != 1 || env.calls[0] != want {
		t.Fatalf("calls = %q, want %q", env.calls, want)
	}
	if m := got.(map[string]any); len(m) != 2 || m["message_id"] != int64(5) || m["date"] != int64(1700000000) {
		t.Errorf("got = %v", got)
	}

	env = newBusinessEnv("business/b1")
	env.body = `{"ok":true,"result":{"message_id":91,"chat":{"id":777}}}`
	got, err = env.run("checklistedit", checklistArgsFor("checklistedit", validChecklist(), nil))
	if err != nil {
		t.Fatal(err)
	}
	want = `editMessageChecklist {"business_connection_id":"b1","chat_id":777,"message_id":91,"checklist":` + list + `}`
	if len(env.calls) != 1 || env.calls[0] != want {
		t.Fatalf("calls = %q, want %q", env.calls, want)
	}
	if m := got.(map[string]any); len(m) != 1 || m["message_id"] != int64(91) {
		t.Errorf("got = %v", got)
	}
}

func TestChecklistToolsRejectBeforeSecretAccess(t *testing.T) {
	tasks := func(n int) []map[string]any {
		out := make([]map[string]any, n)
		for i := range out {
			out[i] = map[string]any{"id": i + 1, "text": "t"}
		}
		return out
	}
	one := tasks(1)
	list := func(title string, ts any) map[string]any { return map[string]any{"title": title, "tasks": ts} }
	both := []string{"-100", "business/b1", "business/b2"}
	type testCase struct {
		targets []string
		args    func(tool string) map[string]any
	}
	cases := map[string]testCase{}
	withList := func(name string, l any) {
		cases[name] = testCase{both, func(tool string) map[string]any { return checklistArgsFor(tool, l, nil) }}
	}
	withArgs := func(name string, targets []string, extra map[string]any) {
		cases[name] = testCase{targets, func(tool string) map[string]any { return checklistArgsFor(tool, validChecklist(), extra) }}
	}
	withList("zero tasks", list("x", []map[string]any{}))
	withList("31 tasks", list("x", tasks(31)))
	withList("duplicate id", list("x", []map[string]any{{"id": 1, "text": "a"}, {"id": 1, "text": "b"}}))
	withList("zero id", list("x", []map[string]any{{"id": 0, "text": "a"}}))
	withList("negative id", list("x", []map[string]any{{"id": -1, "text": "a"}}))
	withList("long title", list(strings.Repeat("x", 256), one))
	withList("empty title", list("", one))
	withList("long text", list("x", []map[string]any{{"id": 1, "text": strings.Repeat("ä", 101)}}))
	withList("empty text", list("x", []map[string]any{{"id": 1, "text": ""}}))
	withList("unknown checklist field", map[string]any{"title": "x", "tasks": one, "parse_mode": "HTML"})
	withList("unknown task field", list("x", []map[string]any{{"id": 1, "text": "a", "text_entities": []int{}}}))
	withList("checklist string", "x")
	withArgs("no business target", []string{"-100", "bot"}, nil)
	withArgs("foreign business", both, map[string]any{"business": "FOREIGNID"})
	withArgs("invalid business", both, map[string]any{"business": "a/b"})
	withArgs("other business ref", both, map[string]any{"chat": businessRef("business/b2", "777")})
	withArgs("chat bound ref", both, map[string]any{"chat": businessRef("-100", "777")})
	withArgs("file kind ref", both, map[string]any{"chat": signRef(testToken, refFile, "business/b1", "777")})
	withArgs("raw chat id", both, map[string]any{"chat": "777"})
	withArgs("reply markup", both, map[string]any{"reply_markup": map[string]any{}})
	withArgs("message effect", both, map[string]any{"message_effect_id": "1"})
	withArgs("passthrough", both, map[string]any{"business_connection_id": "b2", "chat_id": 5})
	cases["missing message id"] = testCase{both, func(tool string) map[string]any {
		a := checklistArgsFor(tool, validChecklist(), nil)
		if tool == "checklistedit" {
			delete(a, "message_id")
		} else {
			a["message_id"] = 4
		}
		return a
	}}
	for name, c := range cases {
		for _, tool := range []string{"checklistsend", "checklistedit"} {
			t.Run(name+" "+tool, func(t *testing.T) {
				env := newBusinessEnv(c.targets...)
				_, err := env.run(tool, c.args(tool))
				if err == nil {
					t.Fatal("accepted")
				}
				if env.lookups != 0 || len(env.calls) != 0 {
					t.Fatalf("secret lookups = %d, requests = %q", env.lookups, env.calls)
				}
				for _, leak := range []string{"FOREIGNID", "b2", testToken} {
					if strings.Contains(err.Error(), leak) {
						t.Fatalf("error leaks %q: %v", leak, err)
					}
				}
			})
		}
	}
}

func TestChecklistToolsRefuseForgedReferencesBeforeRequest(t *testing.T) {
	for name, ref := range map[string]string{
		"flipped tag": flipLast(businessRef("business/b1", "777")),
		"other token": signRef("999:other-token", refBusinessChat, "business/b1", "777"),
		"non-numeric": businessRef("business/b1", "@name"),
	} {
		for _, tool := range []string{"checklistsend", "checklistedit"} {
			env := newBusinessEnv("business/b1")
			if _, err := env.run(tool, checklistArgsFor(tool, validChecklist(), map[string]any{"chat": ref})); err == nil || len(env.calls) != 0 {
				t.Errorf("%s %s: err = %v, calls = %q", tool, name, err, env.calls)
			}
		}
	}
}

func TestChecklistToolsSendOneRequestAndReportUncertainty(t *testing.T) {
	for _, tool := range []string{"checklistsend", "checklistedit"} {
		for name, setup := range map[string]func(*businessEnv){
			"500":     func(e *businessEnv) { e.status = 500 },
			"502":     func(e *businessEnv) { e.status = 502 },
			"timeout": func(e *businessEnv) { e.fail = &timeoutError{} },
			"object":  func(e *businessEnv) { e.body = `{"ok":true,"result":{"x":1}}` },
			"true":    func(e *businessEnv) { e.body = `{"ok":true,"result":true}` },
		} {
			env := newBusinessEnv("business/b1")
			setup(env)
			_, err := env.run(tool, checklistArgsFor(tool, validChecklist(), nil))
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") ||
				strings.Contains(err.Error(), "SECRETDESC") || len(env.calls) != 1 {
				t.Errorf("%s %s: err = %v, calls = %d", tool, name, err, len(env.calls))
			}
		}
	}
	// An answer for another message is not accepted as the edited one.
	env := newBusinessEnv("business/b1")
	env.body = `{"ok":true,"result":{"message_id":7}}`
	if _, err := env.run("checklistedit", checklistArgsFor("checklistedit", validChecklist(), nil)); err == nil {
		t.Error("an answer for another message was accepted")
	}
}

func TestChecklistDescriptors(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		d           capability.Descriptor
		effect      capability.Effect
		idempotency capability.Idempotency
	}{
		{checklistsSend, capability.EffectCreate, capability.IdempotencyNonIdempotent},
		{checklistsEdit, capability.EffectUpdate, capability.IdempotencyUnknown},
	} {
		r := c.d.Risk
		if r.Effect != c.effect || r.Idempotency != c.idempotency || r.Confirmation != capability.ConfirmationRequired ||
			!r.OpenWorld || r.DataSensitivity != "telegram-business-content" || c.d.RequiresToolAllowList ||
			c.d.Group != groupBusiness {
			t.Errorf("%s = %+v", c.d.ID, c.d)
		}
		if _, _, ok := reg.Lookup(c.d.ID); !ok {
			t.Errorf("%s is not registered", c.d.ID)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			if strings.HasPrefix(tool, "telegram.checklists.") {
				t.Errorf("profile %s contains %s", profile.ID, tool)
			}
		}
	}
}
