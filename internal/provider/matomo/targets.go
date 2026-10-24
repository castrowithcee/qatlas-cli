package matomo

import (
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	formSites    = "site"
	formSite     = "site/ID"
	formInstance = "instance"
	sitePrefix   = "site/"
	// maxTargets bounds the targets one connection may be bound to.
	maxTargets = 100
	// maxIDDigits bounds a website ID well above any realistic Matomo idSite.
	maxIDDigits = 9
)

const targetForm = "a Matomo target must be site, site/ID with a positive integer, or instance"

const outsideTargets = "idSite is outside the targets of this connection"

// scope is the boundary of one connection. Without targets every website of the token is reachable and no
// instance-wide tool. Otherwise it is the union of the listed targets: all websites (site), single websites
// (site/ID), and the instance itself.
type scope struct {
	allSites bool
	sites    []int
	instance bool
}

// reachesSites reports whether any website can be reached at all.
func (s scope) reachesSites() bool { return s.allSites || len(s.sites) > 0 }

// allows reports whether the website is inside the scope. A general site binding allows every ID; Matomo
// then proves that the token may view it.
func (s scope) allows(id int) bool { return s.allSites || slices.Contains(s.sites, id) }

// parseScope reads the configured targets: site alone or site/ID entries, never both, plus instance, none
// named twice. No error quotes a configured value.
func parseScope(values []string) (scope, error) {
	if len(values) == 0 {
		return scope{allSites: true}, nil
	}
	if len(values) > maxTargets {
		return scope{}, errors.New("a Matomo connection may be bound to at most 100 targets")
	}
	var bound scope
	var general, instance bool
	for _, raw := range values {
		raw = strings.TrimSpace(raw)
		switch {
		case raw == formSites:
			if general {
				return scope{}, errors.New("the Matomo targets name site more than once")
			}
			general = true
		case raw == formInstance:
			if instance {
				return scope{}, errors.New("the Matomo targets name instance more than once")
			}
			instance = true
		default:
			rest, ok := strings.CutPrefix(raw, sitePrefix)
			id, valid := parseID(rest)
			if !ok || !valid {
				return scope{}, errors.New(targetForm)
			}
			if slices.Contains(bound.sites, id) {
				return scope{}, errors.New("the Matomo targets name a website more than once")
			}
			bound.sites = append(bound.sites, id)
		}
	}
	if general && len(bound.sites) > 0 {
		return scope{}, errors.New("the Matomo targets cannot combine site with site/ID")
	}
	bound.allSites, bound.instance = general, instance
	return bound, nil
}

// parseID accepts plain, non-padded decimal digits only.
func parseID(raw string) (int, bool) {
	if raw == "" || len(raw) > maxIDDigits || raw[0] == '0' {
		return 0, false
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	id, err := strconv.Atoi(raw)
	return id, err == nil
}

func validateTarget(raw string) error {
	_, err := parseScope([]string{raw})
	return err
}

func validateSet(values []string) error {
	_, err := parseScope(values)
	return err
}

// boundScope reads the connection's scope before any secret is resolved.
func boundScope(resolved *config.Resolved) (scope, error) {
	bound, err := parseScope(provider.TargetsOf(resolved))
	if err != nil {
		return scope{}, provider.Fail("open", err.Error())
	}
	return bound, nil
}

// instanceTools are the instance-wide tools: they need Super User rights, not a website, and are offered only
// to a connection whose targets name instance.
var instanceTools = map[string]bool{siteGroupsList.ID: true}

// reaches is the Reaches answer of the Matomo target: the version tool serves every binding, an instance-wide
// tool needs the instance target, and the website tools need a binding that reaches websites. A binding to the
// instance alone reaches no website.
func reaches(tool string, targets []string) (bool, []string) {
	bound, err := parseScope(targets)
	if instanceTools[tool] {
		return err == nil && bound.instance, []string{formInstance}
	}
	if err != nil {
		return true, nil
	}
	if tool == annotationsDeleteAll.ID && !(bound.instance && bound.reachesSites()) {
		return false, []string{formInstance}
	}
	if bound.reachesSites() || tool == versionGet.ID {
		return true, nil
	}
	return false, []string{formSites, formSite}
}

// selectSite settles the website of a call before any secret is resolved and before any request: the
// argument when given, or else the only website the connection binds. A website outside the targets is
// refused without naming it or a target.
func selectSite(resolved *config.Resolved, given *int) (int, error) {
	bound, err := boundScope(resolved)
	if err != nil {
		return 0, err
	}
	if !bound.reachesSites() {
		return 0, invalidArgument("this connection is bound to no website")
	}
	if given == nil {
		if len(bound.sites) == 1 {
			return bound.sites[0], nil
		}
		return 0, invalidArgument("idSite is required unless the connection binds exactly one website")
	}
	if *given < 1 || len(strconv.Itoa(*given)) > maxIDDigits {
		return 0, invalidArgument("idSite must be a positive integer")
	}
	if !bound.allows(*given) {
		return 0, invalidArgument(outsideTargets)
	}
	return *given, nil
}

func invalidArgument(message string) error { return &provider.InvalidRequestError{Message: message} }
