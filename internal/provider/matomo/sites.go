package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxSiteURLs     = 100
	maxUrlLength    = 2048
	maxSettingsText = 2048
	// scanLimit is how many websites a list reads when the targets name single websites: the rows are
	// checked against the targets on this side, so the server cannot be asked for just one page.
	scanLimit = 1000
)

// loose reads a Matomo value that arrives as a string, a number, or a flag, depending on the release.
type loose struct {
	text string
}

func (l *loose) UnmarshalJSON(data []byte) error {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	switch v := value.(type) {
	case string:
		l.text = v
	case float64:
		l.text = strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		l.text = strconv.FormatBool(v)
	case nil:
		l.text = ""
	default:
		return errors.New("not a scalar")
	}
	return nil
}

func (l *loose) str(limit int) string {
	if l == nil {
		return ""
	}
	return boundString(strings.TrimSpace(l.text), limit)
}

func (l *loose) flag() *bool {
	if l == nil {
		return nil
	}
	set := l.text == "1" || strings.EqualFold(l.text, "true")
	return &set
}

func (l *loose) number() *int {
	if l == nil {
		return nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(l.text))
	if err != nil {
		return nil
	}
	return &n
}

// siteJSON is a website as SitesManager reports it. Every member is read loosely and none is trusted.
type siteJSON struct {
	ID                       *loose `json:"idsite"`
	Name                     *loose `json:"name"`
	MainURL                  *loose `json:"main_url"`
	Created                  *loose `json:"ts_created"`
	Ecommerce                *loose `json:"ecommerce"`
	SiteSearch               *loose `json:"sitesearch"`
	SiteSearchKeywordParams  *loose `json:"sitesearch_keyword_parameters"`
	SiteSearchCategoryParams *loose `json:"sitesearch_category_parameters"`
	Timezone                 *loose `json:"timezone"`
	TimezoneName             *loose `json:"timezone_name"`
	Currency                 *loose `json:"currency"`
	CurrencyName             *loose `json:"currency_name"`
	ExcludeUnknownURLs       *loose `json:"exclude_unknown_urls"`
	ExcludedIPs              *loose `json:"excluded_ips"`
	ExcludedParameters       *loose `json:"excluded_parameters"`
	ExcludedUserAgents       *loose `json:"excluded_user_agents"`
	ExcludedReferrers        *loose `json:"excluded_referrers"`
	Group                    *loose `json:"group"`
	Type                     *loose `json:"type"`
	KeepURLFragment          *loose `json:"keep_url_fragment"`
	CreatorLogin             *loose `json:"creator_login"`
}

// Site is a website of the token. All text is untrusted provider data.
type Site struct {
	ID                       int      `json:"id"`
	Name                     string   `json:"name,omitempty"`
	MainURL                  string   `json:"main_url,omitempty"`
	URLs                     []string `json:"urls,omitempty"`
	Created                  string   `json:"ts_created,omitempty"`
	Ecommerce                *bool    `json:"ecommerce,omitempty"`
	SiteSearch               *bool    `json:"sitesearch,omitempty"`
	SiteSearchKeywordParams  string   `json:"sitesearch_keyword_parameters,omitempty"`
	SiteSearchCategoryParams string   `json:"sitesearch_category_parameters,omitempty"`
	Timezone                 string   `json:"timezone,omitempty"`
	TimezoneName             string   `json:"timezone_name,omitempty"`
	Currency                 string   `json:"currency,omitempty"`
	CurrencyName             string   `json:"currency_name,omitempty"`
	ExcludeUnknownURLs       *bool    `json:"exclude_unknown_urls,omitempty"`
	ExcludedIPs              string   `json:"excluded_ips,omitempty"`
	ExcludedParameters       string   `json:"excluded_parameters,omitempty"`
	ExcludedUserAgents       string   `json:"excluded_user_agents,omitempty"`
	ExcludedReferrers        string   `json:"excluded_referrers,omitempty"`
	Group                    string   `json:"group,omitempty"`
	Type                     string   `json:"type,omitempty"`
	KeepURLFragment          *int     `json:"keep_url_fragment,omitempty"`
	CreatorLogin             string   `json:"creator_login,omitempty"`
}

// site converts a row; ok is false for a row without a usable website ID.
func (s siteJSON) site() (Site, bool) {
	id := s.ID.number()
	if id == nil || *id < 1 {
		return Site{}, false
	}
	return Site{
		ID: *id, Name: s.Name.str(maxStringLength), MainURL: s.MainURL.str(maxUrlLength),
		Created: s.Created.str(maxStringLength), Ecommerce: s.Ecommerce.flag(), SiteSearch: s.SiteSearch.flag(),
		SiteSearchKeywordParams:  s.SiteSearchKeywordParams.str(maxSettingsText),
		SiteSearchCategoryParams: s.SiteSearchCategoryParams.str(maxSettingsText),
		Timezone:                 s.Timezone.str(maxStringLength), TimezoneName: s.TimezoneName.str(maxStringLength),
		Currency: s.Currency.str(maxStringLength), CurrencyName: s.CurrencyName.str(maxStringLength),
		ExcludeUnknownURLs: s.ExcludeUnknownURLs.flag(),
		ExcludedIPs:        s.ExcludedIPs.str(maxSettingsText), ExcludedParameters: s.ExcludedParameters.str(maxSettingsText),
		ExcludedUserAgents: s.ExcludedUserAgents.str(maxSettingsText),
		ExcludedReferrers:  s.ExcludedReferrers.str(maxSettingsText),
		Group:              s.Group.str(maxStringLength), Type: s.Type.str(maxStringLength),
		KeepURLFragment: s.KeepURLFragment.number(), CreatorLogin: s.CreatorLogin.str(maxStringLength),
	}, true
}

// SitesResult is the answer of sites.list.
type SitesResult struct {
	Sites      []Site `json:"sites"`
	HasMore    bool   `json:"has_more"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// siteRequirement is what the website tools need from the instance: the core SitesManager of Matomo 5.
func siteRequirement(tool string) requirement { return baseRequirement(tool) }

// fetchSite proves and reads one website by SitesManager.getSiteFromId, which asks for a view right. The
// answer must be the website asked for.
func (c *Client) fetchSite(ctx context.Context, op string, req requirement, id int) (Site, error) {
	var raw json.RawMessage
	params := url.Values{"idSite": {strconv.Itoa(id)}}
	if err := c.call(ctx, op, req, "SitesManager.getSiteFromId", params, &raw); err != nil {
		return Site{}, err
	}
	row, ok := oneSite(raw)
	if !ok {
		return Site{}, provider.InvalidResponse(op, "Matomo returned an invalid website")
	}
	site, ok := row.site()
	if !ok || site.ID != id {
		return Site{}, provider.InvalidResponse(op, "Matomo returned another website than asked for")
	}
	return site, nil
}

// oneSite reads the single website of an answer that is an object or a list of one object.
func oneSite(raw json.RawMessage) (siteJSON, bool) {
	raw = bytes.TrimSpace(raw)
	var row siteJSON
	switch {
	case bytes.HasPrefix(raw, []byte("{")):
		return row, json.Unmarshal(raw, &row) == nil
	case bytes.HasPrefix(raw, []byte("[")):
		var rows []siteJSON
		if json.Unmarshal(raw, &rows) != nil || len(rows) != 1 {
			return row, false
		}
		return rows[0], true
	}
	return row, false
}

func invokeSitesGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get website"
	var args struct {
		IDSite *int `json:"idSite"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := siteRequirement(sitesGet.ID)
	site, err := client.fetchSite(ctx, op, req, id)
	if err != nil {
		return nil, err
	}
	var addresses []string
	params := url.Values{"idSite": {strconv.Itoa(id)}}
	if err := client.call(ctx, op, req, "SitesManager.getSiteUrlsFromId", params, &addresses); err != nil {
		return nil, err
	}
	for _, address := range addresses {
		if len(site.URLs) >= maxSiteURLs {
			break
		}
		site.URLs = append(site.URLs, boundString(strings.TrimSpace(address), maxUrlLength))
	}
	return site, nil
}

func invokeSitesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list websites"
	var args struct {
		Pattern string `json:"pattern"`
		Limit   int    `json:"limit"`
		Cursor  string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if !bound.reachesSites() {
		return nil, invalidArgument("this connection is bound to no website")
	}
	pattern := strings.TrimSpace(args.Pattern)
	if args.Pattern != "" && (pattern == "" || len(pattern) > maxPatternLength) {
		return nil, invalidArgument("pattern must hold 1 to 100 characters")
	}
	sites := sortedSites(bound)
	binding := provider.CursorBinding(sitesList.ID, bound.binding(), pattern)
	paging, err := startPage(binding, args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := siteRequirement(sitesList.ID)

	// Single websites without a pattern: the bound IDs are the list, paged here, and only the websites of the
	// batch are read. Nothing is scanned, so no bound website can fall outside the answer.
	if bound.singles() && pattern == "" {
		batch, next := slice(paging, sites)
		result := &SitesResult{Sites: []Site{}, HasMore: next != "", NextCursor: next}
		for _, id := range batch {
			site, err := client.fetchSite(ctx, op, req, id)
			if err != nil {
				return nil, err
			}
			result.Sites = append(result.Sites, site)
		}
		return result, nil
	}

	method := "SitesManager.getSitesWithAtLeastViewAccess"
	params := url.Values{}
	if pattern != "" {
		method = "SitesManager.getPatternMatchSites"
		params.Set("pattern", pattern)
	}
	// Both methods return the first rows of the list, ordered by ID. With all websites in scope the batch plus
	// one row is enough, widened by the excluded websites that may sit among them; with single websites and a
	// pattern the rows are filtered here, so more are read.
	rows := paging.want() + len(bound.excluded)
	if bound.singles() {
		rows = scanLimit
	}
	params.Set("limit", strconv.Itoa(rows))
	var listed []siteJSON
	if err := client.call(ctx, op, req, method, params, &listed); err != nil {
		return nil, err
	}
	if bound.singles() && len(listed) >= scanLimit {
		// The pattern matches more websites than are read, so a bound website might be missing.
		return nil, invalidArgument("pattern matches too many websites to check against the targets; narrow the pattern")
	}
	var kept []Site
	for _, row := range listed {
		if site, ok := row.site(); ok && bound.allows(site.ID) {
			kept = append(kept, site)
		}
	}
	slices.SortStableFunc(kept, func(a, b Site) int { return a.ID - b.ID })
	batch, next := slice(paging, kept)
	if batch == nil {
		batch = []Site{}
	}
	return &SitesResult{Sites: batch, HasMore: next != "", NextCursor: next}, nil
}

const maxPatternLength = 100
