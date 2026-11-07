package gen

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/endpoints"
)

// Table is the curation of one service model: which operations become tools and how, and why every other
// operation is not one. It is data; the generator reads it together with the model and refuses anything
// the two do not bind unambiguously.
type Table struct {
	// Service is the model name, the directory name in api-models-aws.
	Service string
	// Segment is the tool-ID segment from the segment table of the endpoints package.
	Segment string
	// Geltung is Regional or Global and must agree with the endpoint table.
	Geltung string
	// Operations become tools, in this order.
	Operations []Operation
	// Exclude lists operations that never become a tool, each with its reason.
	Exclude []Omission
	// Defer lists operations left for later, each with its reason.
	Defer []Omission
}

// Values of Table.Geltung.
const (
	Regional = "regional"
	Global   = "global"
)

// Omission is an operation that is not a tool, with the reason.
type Omission struct {
	Operation string
	Reason    string
}

// Operation is one model operation that becomes one tool.
type Operation struct {
	// Operation is the operation name in the model, such as GetCallerIdentity.
	Operation string
	// Object and Action are the last two parts of the tool ID aws.<segment>.<object>.<action>.
	Object, Action string
	// Effect is one of read, create, update, delete, execute.
	Effect string
	// Confirm makes the tool require confirmation. Every operation that is not a read must set it.
	Confirm bool
	// ListedOnly makes the tool require an allow-list entry (requires_tool_allow_list).
	ListedOnly bool
	// Idempotency is one of safe, idempotent, non-idempotent. A read is safe; nothing else is.
	Idempotency string
	Title       string
	// Description is the tool description; the generator appends the untrusted-data warning.
	Description string
	Tags        []string
	// DataSensitivity classifies the result for the risk contract.
	DataSensitivity string
	// Arguments describes every input member the tool offers, by model member name. Every offered member
	// needs an entry, since the extracts carry no documentation.
	Arguments map[string]Argument
	// Fields describes every output member the tool returns, by model member name.
	Fields map[string]Field
	// HiddenInputs and HiddenOutputs leave top-level members out for a security reason.
	HiddenInputs, HiddenOutputs []Hidden
	// TargetCheck names the hand-written check that runs on TargetArgs before any I/O.
	TargetCheck string
	// TargetArgs are the input members (top-level strings) TargetCheck reads. Empty for "none".
	TargetArgs []string
	// PolicyCheck names the client-side document check of the service package that runs on PolicyArgs
	// before the call.
	PolicyCheck string
	PolicyArgs  []string
	// AccountCheck marks the operation that is the account check itself (STS GetCallerIdentity): its answer
	// is the result, so the call is made once and is its own proof.
	AccountCheck bool
	// Truncated, when set, adds a boolean result field truncated with this description, true when a string
	// of the result was cut at its limit.
	Truncated string
	Examples  []Example
}

// Argument describes one offered input member.
type Argument struct {
	// Name replaces the derived snake_case name, for readability only.
	Name        string
	Description string
}

// Field describes one returned output member.
type Field struct {
	// Name replaces the derived snake_case name, for readability only.
	Name        string
	Description string
	// MaxLen caps a string; the model's length maximum or the default cap applies when zero.
	MaxLen int
	// Form names a hand-written check of the value, such as account-id.
	Form string
	// Required makes the tool refuse an answer without the member, and the schema require it.
	Required bool
}

// Hidden is a member left out of a tool, with the security reason.
type Hidden struct {
	Member string
	Reason string
}

// Example is one example invocation; Arguments is a JSON object.
type Example struct {
	Description string
	Arguments   string
}

// Names the generator knows. The checks themselves are hand-written in the provider package and in later
// service packages; a name outside these lists is refused.
var (
	Effects      = []string{"read", "create", "update", "delete", "execute"}
	Idempotency  = []string{"safe", "idempotent", "non-idempotent"}
	TargetChecks = []string{"none", "account-argument", "arn-account", "arn-region", "bucket-owner"}
	PolicyChecks = []string{"none", "trust-policy", "bucket-policy", "identity-policy", "pab-tighten"}
	Forms        = []string{"account-id"}
)

var (
	idPart   = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
	argName  = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)
	opName   = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
	tagValue = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

// Validate checks a table on its own, before the model is consulted.
func (t Table) Validate() error {
	seg, err := endpoints.ModelOf(t.Segment)
	if err != nil || seg.Model != t.Service || seg.Reserved {
		return fmt.Errorf("table %s: segment %q is not the segment of model %q in the segment table", t.Service, t.Segment, t.Service)
	}
	global, err := endpoints.IsGlobal(endpoints.Service(t.Segment))
	if err != nil {
		return fmt.Errorf("table %s: segment %q has no endpoint", t.Service, t.Segment)
	}
	if want := map[bool]string{true: Global, false: Regional}[global]; t.Geltung != want {
		return fmt.Errorf("table %s: geltung %q, the endpoint table says %q", t.Service, t.Geltung, want)
	}
	seen := map[string]string{}
	mark := func(op, where string) error {
		if !opName.MatchString(op) {
			return fmt.Errorf("table %s: %q is not an operation name", t.Service, op)
		}
		if prior, dup := seen[op]; dup {
			return fmt.Errorf("table %s: operation %s is listed in %s and in %s", t.Service, op, prior, where)
		}
		seen[op] = where
		return nil
	}
	for _, list := range []struct {
		name string
		ops  []Omission
	}{{"exclude", t.Exclude}, {"defer", t.Defer}} {
		for _, o := range list.ops {
			if err := mark(o.Operation, list.name); err != nil {
				return err
			}
			if strings.TrimSpace(o.Reason) == "" {
				return fmt.Errorf("table %s: %s %s has no reason", t.Service, list.name, o.Operation)
			}
		}
	}
	ids := map[string]bool{}
	for _, op := range t.Operations {
		if err := mark(op.Operation, "operations"); err != nil {
			return err
		}
		if err := op.validate(); err != nil {
			return fmt.Errorf("table %s: operation %s: %w", t.Service, op.Operation, err)
		}
		id := op.Object + "." + op.Action
		if ids[id] {
			return fmt.Errorf("table %s: operation %s: tool ID part %s is used twice", t.Service, op.Operation, id)
		}
		ids[id] = true
	}
	return nil
}

func (op Operation) validate() error {
	switch {
	case !idPart.MatchString(op.Object) || !idPart.MatchString(op.Action):
		return fmt.Errorf("object %q and action %q must be lower case words joined by hyphens", op.Object, op.Action)
	case !slices.Contains(Effects, op.Effect):
		return fmt.Errorf("unknown effect %q", op.Effect)
	case !slices.Contains(Idempotency, op.Idempotency):
		return fmt.Errorf("unknown idempotency %q", op.Idempotency)
	case op.Effect != "read" && !op.Confirm:
		return fmt.Errorf("effect %s needs confirm: every operation that is not a read requires confirmation", op.Effect)
	case (op.Effect == "read") != (op.Idempotency == "safe"):
		return fmt.Errorf("effect %s with idempotency %s: a read is safe and nothing else is", op.Effect, op.Idempotency)
	case !slices.Contains(TargetChecks, op.TargetCheck):
		return fmt.Errorf("unknown target_check %q", op.TargetCheck)
	case !slices.Contains(PolicyChecks, op.PolicyCheck):
		return fmt.Errorf("unknown policy_check %q", op.PolicyCheck)
	case (op.TargetCheck == "none") != (len(op.TargetArgs) == 0):
		return fmt.Errorf("target_check %s needs the arguments it checks, and none only without", op.TargetCheck)
	case (op.PolicyCheck == "none") != (len(op.PolicyArgs) == 0):
		return fmt.Errorf("policy_check %s needs the arguments it checks, and none only without", op.PolicyCheck)
	case strings.TrimSpace(op.Title) == "" || strings.TrimSpace(op.Description) == "" || op.DataSensitivity == "":
		return fmt.Errorf("title, description and data sensitivity are required")
	}
	for _, tag := range op.Tags {
		if !tagValue.MatchString(tag) {
			return fmt.Errorf("tag %q is not a lower case word", tag)
		}
	}
	for _, hidden := range [][]Hidden{op.HiddenInputs, op.HiddenOutputs} {
		for _, h := range hidden {
			if strings.TrimSpace(h.Reason) == "" {
				return fmt.Errorf("hidden member %s has no reason", h.Member)
			}
		}
	}
	for member, a := range op.Arguments {
		if a.Name != "" && !argName.MatchString(a.Name) {
			return fmt.Errorf("argument %s: name %q is not snake_case", member, a.Name)
		}
		if strings.TrimSpace(a.Description) == "" {
			return fmt.Errorf("argument %s has no description", member)
		}
	}
	for member, f := range op.Fields {
		if f.Name != "" && !argName.MatchString(f.Name) {
			return fmt.Errorf("field %s: name %q is not snake_case", member, f.Name)
		}
		if strings.TrimSpace(f.Description) == "" {
			return fmt.Errorf("field %s has no description", member)
		}
		if f.Form != "" && !slices.Contains(Forms, f.Form) {
			return fmt.Errorf("field %s: unknown form %q", member, f.Form)
		}
		if f.MaxLen < 0 {
			return fmt.Errorf("field %s: negative cap", member)
		}
	}
	return nil
}
