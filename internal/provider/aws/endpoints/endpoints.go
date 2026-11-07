package endpoints

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Service names a service by its tool-ID segment (see Segments).
type Service string

// The services of the endpoint table.
const (
	STS      Service = "sts"
	IAM      Service = "iam"
	S3       Service = "s3"
	SES      Service = "ses"
	Route53  Service = "route53"
	Account  Service = "account"
	hostRepl         = "{region}"
)

// ErrUnknownService reports a service without an endpoint entry.
var ErrUnknownService = errors.New("no endpoint is known for this AWS service")

// Endpoint is where and how to send a signed request.
type Endpoint struct {
	// Host is the fixed hostname of the service.
	Host string
	// SigningRegion is the region of the credential scope.
	SigningRegion string
	// SigningName is the service name of the credential scope.
	SigningName string
}

// URL returns the https base URL of the endpoint. Only https is ever used.
func (e Endpoint) URL() string { return "https://" + e.Host }

type entry struct {
	// host is the hostname; {region} is replaced for regional services.
	host string
	// global services ignore the caller's region and sign with signingRegion.
	global        bool
	signingRegion string
	signingName   string
}

// table is the whole endpoint data. Checked against the AWS service endpoint documentation (General
// Reference) and, for Account, the endpoint rule tests of the service model (api-models-aws).
var table = map[Service]entry{
	STS:     {host: "sts." + hostRepl + ".amazonaws.com", signingName: "sts"},
	IAM:     {host: "iam.amazonaws.com", global: true, signingRegion: "us-east-1", signingName: "iam"},
	S3:      {host: "s3." + hostRepl + ".amazonaws.com", signingName: "s3"},
	SES:     {host: "email." + hostRepl + ".amazonaws.com", signingName: "ses"},
	Route53: {host: "route53.amazonaws.com", global: true, signingRegion: "us-east-1", signingName: "route53"},
	Account: {host: "account.us-east-1.amazonaws.com", global: true, signingRegion: "us-east-1", signingName: "account"},
}

// Services lists the services with an endpoint entry, sorted.
func Services() []Service {
	list := make([]Service, 0, len(table))
	for s := range table {
		list = append(list, s)
	}
	sort.Slice(list, func(i, j int) bool { return list[i] < list[j] })
	return list
}

// IsGlobal reports whether service is global: one endpoint tied to the account, whatever the region.
func IsGlobal(service Service) (bool, error) {
	e, ok := table[service]
	if !ok {
		return false, ErrUnknownService
	}
	return e.global, nil
}

// Resolve returns the endpoint of service. A regional service needs a valid aws-partition region; a global
// service ignores region, which may be empty.
func Resolve(service Service, region string) (Endpoint, error) {
	e, ok := table[service]
	if !ok {
		return Endpoint{}, ErrUnknownService
	}
	if e.global {
		return Endpoint{Host: e.host, SigningRegion: e.signingRegion, SigningName: e.signingName}, nil
	}
	if err := ValidRegion(region); err != nil {
		return Endpoint{}, fmt.Errorf("%s endpoint: %w", service, err)
	}
	return Endpoint{
		Host:          strings.Replace(e.host, hostRepl, region, 1),
		SigningRegion: region,
		SigningName:   e.signingName,
	}, nil
}
