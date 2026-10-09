package lexware

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// issueCase pairs an issuing tool with its draft tool and arguments valid for both.
type issueCase struct {
	name, issue, draft, path string
	args                     func(extra string) json.RawMessage
	follow                   bool // the issue tool takes preceding_voucher_id
}

func issueCases() []issueCase {
	quotation := func(extra string) json.RawMessage {
		return draftArgs(articleLine, `,"expiration_date":"2026-10-12T00:00:00+02:00"`+extra)
	}
	return []issueCase{
		{"invoice", "lexware.invoices.issue", "lexware.invoices.create", "/v1/invoices",
			func(extra string) json.RawMessage { return draftArgs(articleLine, extra) }, true},
		{"quotation", "lexware.quotations.issue", "lexware.quotations.create", "/v1/quotations", quotation, false},
		{"order confirmation", "lexware.orderconfirmations.issue", "lexware.orderconfirmations.create", "/v1/order-confirmations",
			func(extra string) json.RawMessage { return draftArgs(articleLine, extra) }, false},
		{"credit note", "lexware.creditnotes.issue", "lexware.creditnotes.create", "/v1/credit-notes", creditArgs, true},
		{"delivery note", "lexware.deliverynotes.issue", "lexware.deliverynotes.create", "/v1/delivery-notes", deliveryArgs, false},
	}
}

func issueCore(t *testing.T) *application.Core {
	t.Helper()
	stubLimiter(t, primaryKey)
	red := &redact.Redactor{}
	return application.New(registry(t), issueConfig(), resolver(red), red)
}

func invokeOn(core *application.Core, connection, operation string, args json.RawMessage, confirm bool) error {
	_, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: connection, Arguments: args, Confirmed: confirm})
	return err
}

// An issue tool sends the same payload as its draft tool plus the fixed finalize query; the draft tool never does.
func TestIssueSendsTheDraftPayloadWithTheFixedFinalizeQuery(t *testing.T) {
	for _, tt := range issueCases() {
		for _, withPreceding := range []bool{false, true} {
			if withPreceding && !tt.follow {
				continue
			}
			t.Run(tt.name, func(t *testing.T) {
				core := issueCore(t)
				extra := ""
				wantSuffix := ""
				if withPreceding {
					extra, wantSuffix = `,"preceding_voucher_id":"`+precedeID+`"`, "precedingSalesVoucherId="+precedeID
				}
				var queries, payloads []string
				serve(t, func(request *http.Request) (*http.Response, error) {
					body := new(strings.Builder)
					var payload map[string]any
					if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
						t.Error(err)
					}
					encoded, _ := json.Marshal(payload)
					body.Write(encoded)
					if request.Method != http.MethodPost || request.URL.Path != tt.path {
						t.Errorf("request = %s %s", request.Method, request.URL.Path)
					}
					queries = append(queries, request.URL.RawQuery)
					payloads = append(payloads, body.String())
					return jsonResponse(http.StatusCreated, draftReply), nil
				})
				if err := invokeOn(core, "lexware-creator", tt.draft, tt.args(extra), true); err != nil {
					t.Fatalf("draft = %v", err)
				}
				if err := invokeOn(core, "lexware-issuer", tt.issue, tt.args(extra), true); err != nil {
					t.Fatalf("issue = %v", err)
				}
				wantIssue := strings.TrimSuffix("finalize=true&"+wantSuffix, "&")
				if len(queries) != 2 || queries[0] != wantSuffix || queries[1] != wantIssue {
					t.Errorf("queries = %q, want %q and %q", queries, wantSuffix, wantIssue)
				}
				if payloads[0] != payloads[1] || strings.Contains(payloads[1], "finalize") {
					t.Errorf("payloads differ or carry finalize: %s / %s", payloads[0], payloads[1])
				}
			})
		}
	}
}

func TestIssueToolsNeedAnExplicitToolsEntry(t *testing.T) {
	core := issueCore(t)
	refuse(t)
	for _, tt := range issueCases() {
		for _, connection := range []string{"lexware-creator", "lexware-primary"} {
			if err := invokeOn(core, connection, tt.issue, tt.args(""), true); err == nil {
				t.Errorf("%s offered %s without a tools entry", connection, tt.issue)
			}
		}
	}
}

func TestIssueToolsAreRefusedBeforeIO(t *testing.T) {
	refuse(t)
	core := issueCore(t)
	for _, tt := range issueCases() {
		type refusal struct {
			name, extra string
			base        json.RawMessage
			confirm     bool
		}
		tests := []refusal{
			{"without confirm", "", nil, false},
			{"finalize argument", `,"finalize":true`, nil, true},
			{"contact id no UUID", "", json.RawMessage(strings.Replace(string(tt.args("")), invoiceID, "../"+bodyCanary, 1)), true},
		}
		if tt.follow {
			tests = append(tests, refusal{"predecessor no UUID", `,"preceding_voucher_id":"../` + bodyCanary + `"`, nil, true})
		} else {
			tests = append(tests, refusal{"predecessor", `,"preceding_voucher_id":"` + precedeID + `"`, nil, true})
		}
		for _, c := range tests {
			t.Run(tt.name+" "+c.name, func(t *testing.T) {
				args := c.base
				if args == nil {
					args = tt.args(c.extra)
				}
				err := invokeOn(core, "lexware-issuer", tt.issue, args, c.confirm)
				if err == nil {
					t.Fatal("the core accepted the request")
				}
				if strings.Contains(err.Error(), bodyCanary) {
					t.Errorf("error repeats the rejected value: %v", err)
				}
			})
		}
	}
}

func TestIssueReportsUncertaintyWithoutRetry(t *testing.T) {
	for _, tt := range issueCases() {
		t.Run(tt.name, func(t *testing.T) {
			core := issueCore(t)
			requests := 0
			serve(t, func(*http.Request) (*http.Response, error) {
				requests++
				return jsonResponse(http.StatusInternalServerError, `{"message":"`+bodyCanary+`"}`), nil
			})
			err := invokeOn(core, "lexware-issuer", tt.issue, tt.args(""), true)
			noun := strings.ToLower(tt.name)
			if err == nil || !strings.Contains(err.Error(), "the "+noun+" may have been issued") ||
				strings.Contains(err.Error(), bodyCanary) || requests != 1 {
				t.Errorf("error = %v, requests = %d", err, requests)
			}
		})
	}
}

func TestIssueFollowUpRejectionHasFixedMessage(t *testing.T) {
	for _, tt := range issueCases() {
		if !tt.follow {
			continue
		}
		t.Run(tt.name, func(t *testing.T) {
			core := issueCore(t)
			serve(t, func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusNotAcceptable, `{"message":"`+bodyCanary+`"}`), nil
			})
			err := invokeOn(core, "lexware-issuer", tt.issue, tt.args(`,"preceding_voucher_id":"`+precedeID+`"`), true)
			if err == nil || !strings.Contains(err.Error(), "cannot be followed up by") || strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("error = %v, want the fixed follow-up message", err)
			}
		})
	}
}
