package aws

import (
	"reflect"
	"strings"
	"testing"
)

func TestValidateTarget(t *testing.T) {
	for _, good := range []string{"account/123456789012", "region/eu-central-1", "region/us-east-1", "region/ap-southeast-2"} {
		if err := validateTarget(good); err != nil {
			t.Errorf("validateTarget(%q) = %v, want nil", good, err)
		}
	}
	for _, bad := range []string{
		"", "account", "account/", "account/12345678901", "account/1234567890123", "account/12345678901a",
		"account/ 123456789012", "region", "region/", "region/eu", "region/EU-CENTRAL-1", "region/eu-central-1/x",
		"region/cn-north-1", "region/cn-northwest-1", "region/us-gov-west-1", "region/aws-cn", "region/aws-us-gov",
		"arn:aws-cn:iam::123456789012:root", "*", "folder/x", "account/123456789012/x",
	} {
		err := validateTarget(bad)
		if err == nil {
			t.Errorf("validateTarget(%q) = nil, want an error", bad)
		} else if len(bad) > 12 && strings.Contains(err.Error(), bad) {
			t.Errorf("validateTarget(%q) quotes the value: %v", bad, err)
		}
	}
	if err := validateTarget("region/cn-north-1"); err == nil || !strings.Contains(err.Error(), "aws partition") {
		t.Errorf("a cn region says %v, want the partition reason", err)
	}
}

func TestValidateSet(t *testing.T) {
	good := [][]string{
		{"account/123456789012", "region/eu-central-1"},
		{"region/us-east-1", "account/123456789012", "region/eu-west-1"},
	}
	for _, set := range good {
		if err := validateSet(set); err != nil {
			t.Errorf("validateSet(%v) = %v, want nil", set, err)
		}
	}
	bad := map[string][]string{
		"no target":         nil,
		"no account":        {"region/eu-central-1"},
		"no region":         {"account/123456789012"},
		"two accounts":      {"account/123456789012", "account/210987654321", "region/eu-central-1"},
		"the same account":  {"account/123456789012", "account/123456789012", "region/eu-central-1"},
		"a repeated region": {"account/123456789012", "region/eu-central-1", "region/eu-central-1"},
		"a cn region":       {"account/123456789012", "region/cn-north-1"},
		"a gov region":      {"account/123456789012", "region/us-gov-east-1"},
		"an unknown target": {"account/123456789012", "region/eu-central-1", "bucket/x"},
		"too many regions":  manyRegions(41),
	}
	for name, set := range bad {
		if err := validateSet(set); err == nil {
			t.Errorf("validateSet(%s) = nil, want an error", name)
		}
	}
	if err := validateSet(manyRegions(40)); err != nil {
		t.Errorf("40 regions: %v", err)
	}
}

func manyRegions(n int) []string {
	set := []string{"account/123456789012"}
	for i := 0; i < n; i++ {
		set = append(set, "region/aa-bb-"+itoa(i+1))
	}
	return set
}

func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}

func TestPickRegion(t *testing.T) {
	one := binding{account: testAccount, regions: []string{"eu-central-1"}}
	two := binding{account: testAccount, regions: []string{"eu-central-1", "us-east-1"}}
	for _, tc := range []struct {
		name      string
		b         binding
		requested string
		want      string
		ok        bool
	}{
		{"the only region by default", one, "", "eu-central-1", true},
		{"the only region named", one, "eu-central-1", "eu-central-1", true},
		{"a region outside the only one", one, "us-east-1", "", false},
		{"several regions need a choice", two, "", "", false},
		{"an allowed region", two, "us-east-1", "us-east-1", true},
		{"a region outside", two, "eu-west-1", "", false},
		{"a cn region", two, "cn-north-1", "", false},
	} {
		got, err := tc.b.pick(tc.requested)
		if tc.ok != (err == nil) || got != tc.want || (err != nil && !isInvalidRequest(err)) {
			t.Errorf("%s: pick(%q) = %q, %v", tc.name, tc.requested, got, err)
		}
	}
}

func TestReaches(t *testing.T) {
	full := []string{"account/123456789012", "region/eu-central-1"}
	if reached, _ := reaches("aws.sts.caller-identity.get", full); !reached {
		t.Error("a full binding does not reach a regional tool")
	}
	// A global service hangs on the account and ignores the allowlist.
	if reached, needs := reaches("aws.iam.users.list", []string{"account/123456789012"}); !reached {
		t.Errorf("a global service needs %v, want the account alone", needs)
	}
	reached, needs := reaches("aws.sts.caller-identity.get", []string{"account/123456789012"})
	if reached || !reflect.DeepEqual(needs, []string{"region/NAME"}) {
		t.Errorf("a regional tool without a region: reached=%v needs=%v", reached, needs)
	}
	reached, needs = reaches("aws.iam.users.list", []string{"region/eu-central-1"})
	if reached || !reflect.DeepEqual(needs, []string{"account/ACCOUNT_ID"}) {
		t.Errorf("a global tool without an account: reached=%v needs=%v", reached, needs)
	}
	// What does not parse is reported by validation, and reaches everything meanwhile.
	if reached, _ := reaches("aws.sts.caller-identity.get", []string{"nonsense"}); !reached {
		t.Error("an unparsable binding hides a tool")
	}
	if reached, _ := reaches("aws.sts.caller-identity.get", nil); !reached {
		t.Error("an empty binding hides a tool")
	}
}

// The needs a provider names must be forms its kinds declare, or the hint is dropped.
func TestReachNeedsAreDeclaredForms(t *testing.T) {
	reg := registered(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	if reached, hint := metadata.Target.Reach("aws.sts.caller-identity.get", []string{"account/123456789012"}); reached ||
		hint != "needs region/NAME" {
		t.Errorf("Reach = %v, %q, want a hint naming region/NAME", reached, hint)
	}
}
