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

const excludeForm = "a Matomo exclusion must be site/ID with a positive integer, named once and not equal to a target"

const outsideTargets = "idSite is not available to this connection"

// scope is the boundary of one connection. Without targets every website of the token is reachable and no
// instance-wide tool. Otherwise it is the union of the listed targets: all websites (site), single websites
// (site/ID), and the instance itself. An exclusion site/ID takes one website out of what the targets reach: the
// website behaves like one the user holds no view right on.
//
// Rules every module follows:
//  1. Every website argument goes through selectSite, every row that carries an idSite through allows.
//  2. Code outside this file never reads allSites. A sum that is not filtered by allows needs the predicate
//     everySite (all websites, none excluded); a test enforces this.
//  3. A server window is cut on the server rows first and filtered afterwards; the cursor follows the server rows.
//  4. A sum over several websites (totals, a website count, group rows) is dropped unless everySite holds. An
//     instance sum that cannot be attributed to websites stays and says so in the tool description.
//  5. Objects of all websites (idSite 0) are readable; writing, deleting and marking them needs everySite.
type scope struct {
	allSites bool
	sites    []int
	instance bool
	// excluded are the websites taken out of the targets, never targets themselves.
	excluded []int
}

// wholeToken is the scope of a call that is no connection call: every website the token can view.
func wholeToken() scope { return scope{allSites: true} }

// reachesSites reports whether any website can be reached at all.
func (s scope) reachesSites() bool { return s.allSites || len(s.sites) > 0 }

// allows reports whether the website is inside the scope. A general site binding allows every ID but the
// excluded ones; Matomo then proves that the token may view it.
func (s scope) allows(id int) bool {
	return !slices.Contains(s.excluded, id) && (s.allSites || slices.Contains(s.sites, id))
}

// everySite reports whether every website of the token is inside the scope: the general site binding without
// any exclusion.
func (s scope) everySite() bool { return s.allSites && len(s.excluded) == 0 }

// singles reports whether the targets name single websites, so a list of the token's websites is filtered by
// them.
func (s scope) singles() bool { return !s.allSites }

// binding is the part of a cursor binding that ties it to the targets and exclusions of the connection.
func (s scope) binding() []any {
	return []any{s.allSites, sortedCopy(s.sites), sortedCopy(s.excluded)}
}

func sortedCopy(ids []int) []int {
	out := slices.Clone(ids)
	slices.Sort(out)
	return out
}

// parseScope reads the configured targets: site alone or site/ID entries, never both, plus instance, none
// named twice. An exclusion is a site/ID, named once and never equal to a target. It does not rely on the core
// validation, so an invalid exclusion fails closed here too. No error quotes a configured value.
func parseScope(values []string, excludes ...string) (scope, error) {
	if len(values) == 0 && len(excludes) == 0 {
		return scope{allSites: true}, nil
	}
	if len(values) > maxTargets || len(excludes) > maxTargets {
		return scope{}, errors.New("a Matomo connection may be bound to at most 100 targets and exclusions")
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
	if len(values) == 0 {
		bound.allSites = true
	}
	for _, raw := range excludes {
		rest, ok := strings.CutPrefix(raw, sitePrefix)
		id, valid := parseID(rest)
		if !ok || !valid || slices.Contains(bound.excluded, id) || slices.Contains(bound.sites, id) {
			return scope{}, errors.New(excludeForm)
		}
		bound.excluded = append(bound.excluded, id)
	}
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
	bound, err := parseScope(provider.TargetsOf(resolved), provider.ExcludesOf(resolved)...)
	if err != nil {
		return scope{}, provider.Fail("open", err.Error())
	}
	return bound, nil
}

// instanceTools are the instance-wide tools: they need Super User rights, not a website, and are offered only
// to a connection whose targets name instance.
var instanceTools = map[string]bool{siteGroupsList.ID: true, globalSettingsGet.ID: true, sitesAdd.ID: true,
	siteGroupsRename.ID: true, globalSettingsSetExcludedIPs.ID: true, globalSettingsSetExcludedUserAgents.ID: true,
	globalSettingsSetExcludedReferrers.ID: true, globalSettingsSetExcludedQueryParameters.ID: true,
	globalSettingsSetSearchParameters.ID: true, globalSettingsSetKeepURLFragments.ID: true,
	globalSettingsSetDefaultCurrency.ID: true, globalSettingsSetDefaultTimezone.ID: true, usersGet.ID: true,
	usersDelete.ID: true, usersLogout.ID: true, complianceGet.ID: true, complianceSetPolicySettings.ID: true,
	complianceEnforce.ID: true, complianceSetStatus.ID: true, databaseStatisticsGet.ID: true,
	dataPurgeRun.ID: true, trackingFailuresDeleteAll.ID: true, archiveSettingsSet.ID: true, trustedHostsSet.ID: true,
	brandingSet.ID: true, privacySettingsSetAnonymizeIP.ID: true, doNotTrackEnable.ID: true,
	doNotTrackDisable.ID: true, privacySettingsSetDeleteLogs.ID: true, privacySettingsSetDeleteReports.ID: true,
	privacySettingsSetReportDeletionSchedule.ID: true, bruteForceIPsUnblock.ID: true, smsSettingsGet.ID: true,
	smsPhoneNumbersRemove.ID: true, smsCredentialsDelete.ID: true, smsDelegationSet.ID: true, aiProvidersGetSettings.ID: true,
	aiProvidersDisconnect.ID: true, ldapUsersCount.ID: true, ldapUsersList.ID: true}

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
	if (tool == annotationsDeleteAll.ID || tool == sitesDelete.ID || tool == rawDataAnonymize.ID || tool == reportsArchive.ID) && !(bound.instance && bound.reachesSites()) {
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
