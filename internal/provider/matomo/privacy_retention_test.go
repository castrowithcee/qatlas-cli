package matomo

import (
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const (
	deleteLogsArgs    = `{"enableDeleteLogs":true,"deleteLogsOlderThan":180}`
	deleteReportsArgs = `{"enableDeleteReports":true,"deleteReportsOlderThan":24,"keepBasic":true,"keepDay":false,` +
		`"keepWeek":false,"keepMonth":true,"keepYear":true,"keepRange":false,"keepSegments":false}`
	scheduleArgs = `{"deleteLowestInterval":7}`
)

func TestRetentionSettersAreInstanceToolsAndListedOnlyAsDeclared(t *testing.T) {
	reg := registry(t)
	for id, listed := range map[string]bool{"matomo.privacy-settings.set-delete-logs": true,
		"matomo.privacy-settings.set-delete-reports": true, "matomo.privacy-settings.set-report-deletion-schedule": false} {
		d, _, found := reg.Lookup(id)
		if !found || d.Risk.Effect != capability.EffectUpdate || d.RequiresToolAllowList != listed ||
			d.Risk.Confirmation != capability.ConfirmationRequired {
			t.Errorf("%s: found %t, %+v", id, found, d)
		}
		for targets, want := range map[string]bool{"site/1": false, "site": false, "instance": true, "site,instance": true} {
			if ok, _ := reaches(id, strings.Split(targets, ",")); ok != want {
				t.Errorf("%s reaches(%s) = %t", id, targets, ok)
			}
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if strings.HasPrefix(id, "matomo.privacy-settings.set-") {
				t.Errorf("profile %s holds %s", profile.ID, id)
			}
		}
	}
}

func TestRetentionSettersSendOneFullRequest(t *testing.T) {
	for _, c := range []struct {
		id, method, connection, args string
		want                         map[string]string
		result                       string
	}{
		{"matomo.privacy-settings.set-delete-logs", "PrivacyManager.setDeleteLogsSettings", "pe-logs", deleteLogsArgs,
			map[string]string{"enableDeleteLogs": "1", "deleteLogsOlderThan": "180"},
			`{"deleteLogsOlderThan":180,"enableDeleteLogs":true,"saved":true}`},
		{"matomo.privacy-settings.set-delete-reports", "PrivacyManager.setDeleteReportsSettings", "pe-reports", deleteReportsArgs,
			map[string]string{"enableDeleteReports": "1", "deleteReportsOlderThan": "24", "keepBasic": "1", "keepDay": "0",
				"keepWeek": "0", "keepMonth": "1", "keepYear": "1", "keepRange": "0", "keepSegments": "0"},
			`{"deleteReportsOlderThan":24,"enableDeleteReports":true,"keepBasic":true,"keepDay":false,"keepMonth":true,` +
				`"keepRange":false,"keepSegments":false,"keepWeek":false,"keepYear":true,"saved":true}`},
		{"matomo.privacy-settings.set-report-deletion-schedule", "PrivacyManager.setScheduleReportDeletionSettings", "pe-sched",
			scheduleArgs, map[string]string{"deleteLowestInterval": "7"}, `{"deleteLowestInterval":7,"saved":true}`},
	} {
		for name, body := range map[string]string{"success": `{"result":"success","message":"ok"}`, "bool": `{"value":true}`} {
			var calls []call
			env := newEraseEnvironment(t, &calls, eraseAnswers(c.method, func(call) *http.Response { return ok(body) }).handler(t))
			result, err := env.change(c.id, c.connection, c.args)
			if err != nil || result != c.result {
				t.Fatalf("%s %s: result = %s, err = %v", c.id, name, result, err)
			}
			sent := calls[len(calls)-1]
			if countMethod(calls, c.method) != 1 || sent.form.Get("format") != "JSON" {
				t.Errorf("%s: request = %+v", c.id, sent)
			}
			for key, want := range c.want {
				if sent.form.Get(key) != want {
					t.Errorf("%s: %s = %q, want %q", c.id, key, sent.form.Get(key), want)
				}
			}
			for key := range sent.form {
				if _, known := c.want[key]; !known {
					switch key {
					case "method", "module", "format", "serialize", "passwordConfirmation":
					default:
						t.Errorf("%s: unexpected form field %s", c.id, key)
					}
				}
			}
		}
	}
}

func TestRetentionSettersRefuseBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newEraseEnvironment(t, &calls, eraseAnswers("PrivacyManager.x", successAnswer("")).handler(t))
	for id, c := range map[string]struct {
		connection string
		bad        []string
	}{
		"matomo.privacy-settings.set-delete-logs": {"pe-logs", []string{`{}`, `{"enableDeleteLogs":true}`,
			`{"deleteLogsOlderThan":180}`, `{"enableDeleteLogs":true,"deleteLogsOlderThan":0}`,
			`{"enableDeleteLogs":true,"deleteLogsOlderThan":3651}`, `{"enableDeleteLogs":"1","deleteLogsOlderThan":5}`,
			`{"enableDeleteLogs":true,"deleteLogsOlderThan":5,"x":1}`}},
		"matomo.privacy-settings.set-delete-reports": {"pe-reports", []string{`{}`,
			strings.Replace(deleteReportsArgs, `"keepYear":true,`, ``, 1),
			strings.Replace(deleteReportsArgs, `"enableDeleteReports":true,`, ``, 1),
			strings.Replace(deleteReportsArgs, `"deleteReportsOlderThan":24`, `"deleteReportsOlderThan":1`, 1)}},
		"matomo.privacy-settings.set-report-deletion-schedule": {"pe-sched", []string{`{}`,
			`{"deleteLowestInterval":0}`, `{"deleteLowestInterval":"7"}`}},
	} {
		for _, args := range c.bad {
			if _, err := env.change(id, c.connection, args); !isInvalidRequest(err) {
				t.Errorf("%s %s: err = %v", id, args, err)
			}
		}
	}
	for id, c := range map[string]struct{ connection, args string }{
		"matomo.privacy-settings.set-delete-logs":              {"pe-logs-noinst", deleteLogsArgs},
		"matomo.privacy-settings.set-delete-reports":           {"pe-reports-noinst", deleteReportsArgs},
		"matomo.privacy-settings.set-report-deletion-schedule": {"pe-sched-noinst", scheduleArgs},
	} {
		if _, err := env.change(id, c.connection, c.args); err == nil {
			t.Errorf("%s ran without the instance", id)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("%d requests and %d reads before a refusal", len(calls), *env.reads)
	}
}
