package endpoints

import (
	"errors"
	"regexp"
	"testing"
)

func TestResolve(t *testing.T) {
	tests := []struct {
		service Service
		region  string
		want    Endpoint
	}{
		{STS, "eu-central-1", Endpoint{"sts.eu-central-1.amazonaws.com", "eu-central-1", "sts"}},
		{IAM, "", Endpoint{"iam.amazonaws.com", "us-east-1", "iam"}},
		{IAM, "eu-central-1", Endpoint{"iam.amazonaws.com", "us-east-1", "iam"}},
		{S3, "ap-southeast-2", Endpoint{"s3.ap-southeast-2.amazonaws.com", "ap-southeast-2", "s3"}},
		{SES, "us-west-2", Endpoint{"email.us-west-2.amazonaws.com", "us-west-2", "ses"}},
		{Route53, "", Endpoint{"route53.amazonaws.com", "us-east-1", "route53"}},
		{Account, "eu-west-1", Endpoint{"account.us-east-1.amazonaws.com", "us-east-1", "account"}},
	}
	for _, test := range tests {
		got, err := Resolve(test.service, test.region)
		if err != nil || got != test.want {
			t.Errorf("Resolve(%s, %q) = %+v, %v; want %+v", test.service, test.region, got, err, test.want)
		}
	}
	if got, _ := Resolve(STS, "us-east-1"); got.URL() != "https://sts.us-east-1.amazonaws.com" {
		t.Errorf("URL = %q", got.URL())
	}
}

func TestResolveRefusesBadInput(t *testing.T) {
	for _, region := range []string{"", "cn-north-1", "us-gov-west-1", "us-east", "../x", "us-east-1.evil.com/", "US-EAST-1"} {
		if _, err := Resolve(STS, region); err == nil {
			t.Errorf("Resolve(sts, %q) succeeded", region)
		}
	}
	for _, service := range []Service{"", "ec2", "route53-domains", "SES"} {
		if _, err := Resolve(service, "us-east-1"); !errors.Is(err, ErrUnknownService) {
			t.Errorf("Resolve(%q) = %v", service, err)
		}
	}
}

func TestEveryHostIsAnAmazonawsHostWithoutPlaceholders(t *testing.T) {
	host := regexp.MustCompile(`^[a-z0-9-]+(\.[a-z0-9-]+)*\.amazonaws\.com$`)
	for _, service := range Services() {
		got, err := Resolve(service, "eu-central-1")
		if err != nil {
			t.Fatal(err)
		}
		if !host.MatchString(got.Host) {
			t.Errorf("%s host %q", service, got.Host)
		}
		global, _ := IsGlobal(service)
		if global && got.SigningRegion != "us-east-1" {
			t.Errorf("%s: global service signs with %q", service, got.SigningRegion)
		}
	}
	if len(Services()) != 6 {
		t.Errorf("services = %v", Services())
	}
}

func TestValidRegion(t *testing.T) {
	for _, ok := range []string{"us-east-1", "eu-central-1", "ap-southeast-2", "ca-west-1", "me-south-1", "ap-southeast-7"} {
		if err := ValidRegion(ok); err != nil {
			t.Errorf("ValidRegion(%q) = %v", ok, err)
		}
	}
	for _, name := range []string{"cn-north-1", "cn-northwest-1", "us-gov-west-1", "us-gov-east-1"} {
		if err := ValidRegion(name); !errors.Is(err, ErrRegionPartition) {
			t.Errorf("ValidRegion(%q) = %v, want partition error", name, err)
		}
	}
	for _, name := range []string{"", "global", "us-east", "us-east-", "us-east-123", "us_east_1", "us-east-1 ", " us-east-1",
		"us-iso-east-1", "eusc-de-east-1", "eu-central-1\n", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-east-1", "ü-east-1"} {
		if err := ValidRegion(name); !errors.Is(err, ErrRegionInvalid) {
			t.Errorf("ValidRegion(%q) = %v, want invalid", name, err)
		}
	}
}

func TestSegmentTable(t *testing.T) {
	want := map[string]string{
		"sts": "sts", "iam": "iam", "s3": "s3", "sesv2": "ses", "route-53": "route53", "account": "account",
		"route-53-domains": "route53-domains", "sfn": "step-functions", "secrets-manager": "secrets-manager",
		"cost-explorer": "cost-explorer",
	}
	grammar := regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
	models, names := map[string]bool{}, map[string]bool{}
	for _, s := range Segments() {
		if want[s.Model] != s.Segment {
			t.Errorf("%s -> %q", s.Model, s.Segment)
		}
		if models[s.Model] || names[s.Segment] {
			t.Errorf("duplicate entry %+v", s)
		}
		models[s.Model], names[s.Segment] = true, true
		if !grammar.MatchString(s.Segment) {
			t.Errorf("segment %q breaks the grammar", s.Segment)
		}
		got, err := SegmentOf(s.Model)
		if err != nil || got != s {
			t.Errorf("SegmentOf(%s) = %+v, %v", s.Model, got, err)
		}
		back, err := ModelOf(s.Segment)
		if err != nil || back != s {
			t.Errorf("ModelOf(%s) = %+v, %v", s.Segment, back, err)
		}
		// Only reserved segments lack an endpoint.
		if _, err := Resolve(Service(s.Segment), "us-east-1"); (err != nil) != s.Reserved {
			t.Errorf("%s: reserved=%v but Resolve err=%v", s.Segment, s.Reserved, err)
		}
	}
	if len(Segments()) != len(want) || len(Models()) != len(want) {
		t.Errorf("table size %d", len(Segments()))
	}
}

func TestUnmappedServiceIsARegistrationError(t *testing.T) {
	for _, model := range []string{"ec2", "", "sesv1"} {
		if _, err := SegmentOf(model); !errors.Is(err, ErrUnmappedService) {
			t.Errorf("SegmentOf(%q) = %v", model, err)
		}
	}
	if _, err := ModelOf("nope"); !errors.Is(err, ErrUnmappedService) {
		t.Errorf("ModelOf = %v", err)
	}
	s := Segments()
	s[0].Segment = "changed"
	if Segments()[0].Segment == "changed" {
		t.Error("Segments exposes the table")
	}
}
