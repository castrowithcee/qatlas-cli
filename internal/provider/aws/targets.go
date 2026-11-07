package aws

import (
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/endpoints"
)

// The two kinds of target a connection binds. A target is always kind/value: there is no bare kind and no
// wildcard, so a connection never reaches "every account" or "every region" by omission.
const (
	kindAccount = "account"
	kindRegion  = "region"

	accountPrefix = kindAccount + "/"
	regionPrefix  = kindRegion + "/"

	formAccount = accountPrefix + "ACCOUNT_ID"
	formRegion  = regionPrefix + "NAME"

	// maxRegions bounds an allowlist well above the number of regions the aws partition has.
	maxRegions = 40
)

// accountID is an AWS account ID: exactly twelve digits.
var accountID = regexp.MustCompile(`^[0-9]{12}$`)

// binding is what one connection is bound to: its single account and its region allowlist, in configured
// order. Every request is built from it, never from an argument.
type binding struct {
	account string
	regions []string
}

// parseTarget reads one target into its kind and value. No error quotes the configured value, and a
// region of another partition is refused with a reason of its own.
func parseTarget(raw string) (kind, value string, err error) {
	switch {
	case strings.HasPrefix(raw, accountPrefix):
		value = strings.TrimPrefix(raw, accountPrefix)
		if !accountID.MatchString(value) {
			return "", "", errors.New("an AWS account target must be " + formAccount + " with a 12-digit account ID")
		}
		return kindAccount, value, nil
	case strings.HasPrefix(raw, regionPrefix):
		value = strings.TrimPrefix(raw, regionPrefix)
		if err := endpoints.ValidRegion(value); err != nil {
			return "", "", errors.New("an AWS region target must be " + formRegion + ": " + err.Error())
		}
		return kindRegion, value, nil
	}
	return "", "", errors.New("an AWS target must be " + formAccount + " or " + formRegion)
}

func validateTarget(raw string) error {
	_, _, err := parseTarget(raw)
	return err
}

// validateSet requires exactly one account and between one and maxRegions distinct regions.
func validateSet(values []string) error {
	_, err := parseSet(values)
	return err
}

func parseSet(values []string) (binding, error) {
	var b binding
	accounts := 0
	for _, raw := range values {
		kind, value, err := parseTarget(raw)
		if err != nil {
			return binding{}, err
		}
		if kind == kindAccount {
			accounts++
			b.account = value
			continue
		}
		if slices.Contains(b.regions, value) {
			return binding{}, errors.New("an AWS region may be listed only once")
		}
		b.regions = append(b.regions, value)
	}
	switch {
	case accounts != 1:
		return binding{}, errors.New("an AWS connection binds exactly one " + formAccount + " target")
	case len(b.regions) == 0:
		return binding{}, errors.New("an AWS connection needs at least one " + formRegion + " target")
	case len(b.regions) > maxRegions:
		return binding{}, errors.New("an AWS connection may list at most 40 regions")
	}
	return b, nil
}

// bindingOf reads the binding of a selected connection before any secret is resolved.
func bindingOf(resolved *config.Resolved) (binding, error) {
	return parseSet(provider.TargetsOf(resolved))
}

// pick returns the region a regional tool acts on. The argument is optional with exactly one region;
// otherwise it must name a region of the allowlist. A region outside it is refused here, before any secret
// is resolved or any request is built.
func (b binding) pick(requested string) (string, error) {
	if requested == "" {
		if len(b.regions) == 1 {
			return b.regions[0], nil
		}
		return "", &provider.InvalidRequestError{
			Message: "region is required because this connection allows several regions"}
	}
	if !slices.Contains(b.regions, requested) {
		return "", &provider.InvalidRequestError{
			Message: "region is not one of the regions this connection allows"}
	}
	return requested, nil
}

// reaches says whether a binding reaches a tool. Every tool needs the account, because the account check
// runs before each call; a regional tool needs a region as well. Global services hang on the account and
// ignore the allowlist. A binding that does not parse reaches everything: configuration validation reports
// it, and every call refuses it again.
func reaches(tool string, targets []string) (bool, []string) {
	if len(targets) == 0 {
		return true, nil
	}
	var hasAccount, hasRegion bool
	for _, raw := range targets {
		kind, _, err := parseTarget(raw)
		if err != nil {
			return true, nil
		}
		hasAccount = hasAccount || kind == kindAccount
		hasRegion = hasRegion || kind == kindRegion
	}
	var needs []string
	if !hasAccount {
		needs = append(needs, formAccount)
	}
	if global, ok := globalTool(tool); ok && !global && !hasRegion {
		needs = append(needs, formRegion)
	}
	return len(needs) == 0, needs
}

// globalTool reports whether the service segment of a tool ID names a global service. ok is false for an ID
// without a service in the endpoint table.
func globalTool(tool string) (global, ok bool) {
	parts := strings.Split(tool, ".")
	if len(parts) != 4 {
		return false, false
	}
	global, err := endpoints.IsGlobal(endpoints.Service(parts[1]))
	return global, err == nil
}

// regionSchema is the input schema of the region argument of a regional tool.
const regionSchema = `{"type":"string","maxLength":32,"pattern":"^[a-z]{2}-[a-z]+-[0-9]{1,2}$"}`
