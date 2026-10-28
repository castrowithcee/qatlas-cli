package seatable

import "context"

// The table boundary of a connection is decided here and nowhere else. Every tool asks these helpers
// instead of reading the scope fields, so a rule about which tables a connection reaches changes in one place.

// wholeBase reports whether the connection reaches every table of the base without any restriction: the
// wildcard without exclusions. Code that skips a boundary check for the whole base asks this, never the
// wildcard flag itself.
func (s scope) wholeBase() bool {
	return s.wildcard && len(s.excluded) == 0
}

// excludes reports whether the connection carries any exclusion, so that judging a table takes the metadata.
func (s scope) excludes() bool {
	return len(s.excluded) > 0
}

// matchesView reports whether a target names the view by its identifier or its name.
func matchesView(wanted target, id, name string) bool {
	return (wanted.viewParam == "view_id" && wanted.view == id) ||
		(wanted.viewParam == "view_name" && wanted.view == name)
}

// excludedText reports whether an exclusion rules out the selected target by its spelling alone. A table
// exclusion rules out the table with every view; a view exclusion only the view. An exclusion spelled in the
// other form is only found in the metadata.
func (s scope) excludedText(selected target) bool {
	for _, excluded := range s.excluded {
		if excluded.tableParam != selected.tableParam || excluded.table != selected.table {
			continue
		}
		if excluded.view == "" {
			return true
		}
		if selected.view != "" && excluded.viewParam == selected.viewParam && excluded.view == selected.view {
			return true
		}
	}
	return false
}

// tableExcluded reports whether an exclusion takes the whole table out, whichever form it is spelled in.
func (s scope) tableExcluded(table tableJSON) bool {
	for _, excluded := range s.excluded {
		if excluded.view == "" && matchesTable(excluded, table) {
			return true
		}
	}
	return false
}

// viewExcluded reports whether an exclusion takes the view out of the table, or the table itself.
func (s scope) viewExcluded(table tableJSON, id, name string) bool {
	if s.tableExcluded(table) {
		return true
	}
	for _, excluded := range s.excluded {
		if excluded.view != "" && matchesTable(excluded, table) && matchesView(excluded, id, name) {
			return true
		}
	}
	return false
}

// targetExcluded reports whether an exclusion takes a target out again, in whichever form either is spelled:
// the target's table, or its view, is excluded. Such a connection reaches nothing of that target.
func (s scope) targetExcluded(document metadataJSON, wanted target) bool {
	table := findTable(document, wanted)
	if table == nil {
		return false
	}
	if s.tableExcluded(*table) {
		return true
	}
	if wanted.view == "" {
		return false
	}
	for _, view := range table.Views {
		if matchesView(wanted, view.ID, view.Name) && s.viewExcluded(*table, view.ID, view.Name) {
			return true
		}
	}
	return false
}

// claimsTableName reports whether an exclusion names a table by this name, so that no table may be created
// or renamed to it.
func (s scope) claimsTableName(name string) bool {
	for _, excluded := range s.excluded {
		if excluded.tableParam == "table_name" && excluded.table == name {
			return true
		}
	}
	return false
}

// claimsViewName reports whether an exclusion names a view of the table by this name.
func (s scope) claimsViewName(table tableJSON, name string) bool {
	for _, excluded := range s.excluded {
		if excluded.view != "" && excluded.viewParam == "view_name" && excluded.view == name &&
			matchesTable(excluded, table) {
			return true
		}
	}
	return false
}

// claimsViewText is claimsViewName decided by spelling alone, for a table selected in the same form.
func (s scope) claimsViewText(selected target, name string) bool {
	for _, excluded := range s.excluded {
		if excluded.view != "" && excluded.viewParam == "view_name" && excluded.view == name &&
			excluded.tableParam == selected.tableParam && excluded.table == selected.table {
			return true
		}
	}
	return false
}

// unresolved reports whether an exclusion spelled by name finds nothing in the metadata. Such a call must
// fail, because the table or view may have been renamed and would no longer be excluded. An exclusion by
// identifier that matches nothing is valid and has no effect.
func (s scope) unresolved(document metadataJSON) bool {
	for _, excluded := range s.excluded {
		byName := excluded.tableParam == "table_name"
		viewByName := excluded.view != "" && excluded.viewParam == "view_name"
		if !byName && !viewByName {
			continue
		}
		table := findTable(document, excluded)
		if table == nil {
			return true
		}
		if !viewByName {
			continue
		}
		found := false
		for _, view := range table.Views {
			if view.Name == excluded.view {
				found = true
				break
			}
		}
		if !found {
			return true
		}
	}
	return false
}

// admits reports whether a selected target is inside the scope by its text alone, before any metadata is read.
// A wildcard with exclusions still admits what no exclusion rules out by its spelling; the metadata decides
// the rest.
func (s scope) admits(selected target) bool {
	if s.excludedText(selected) {
		return false
	}
	if s.wildcard {
		return true
	}
	for _, allowed := range s.targets {
		if selected == allowed {
			return true
		}
	}
	return false
}

// reaches reports whether the connection may read the table.
func (s scope) reaches(table tableJSON) bool {
	if s.tableExcluded(table) {
		return false
	}
	if s.wildcard {
		return true
	}
	for _, allowed := range s.targets {
		if matchesTable(allowed, table) {
			return true
		}
	}
	return false
}

// reference returns the invocation reference of a table the connection reaches. A restricted connection
// hands out the form its own target uses.
func (s scope) reference(table tableJSON) (string, bool) {
	if s.tableExcluded(table) {
		return "", false
	}
	if s.wildcard {
		return idPrefix + table.ID, true
	}
	for _, wanted := range s.targets {
		if matchesTable(wanted, table) {
			return formatTarget(wanted), true
		}
	}
	return "", false
}

// namedTarget reports whether the connection holds the table by its name, as a target or in a view
// exclusion, so that renaming it would silently change what the connection reaches.
func (s scope) namedTarget(table tableJSON) bool {
	for _, configured := range append(append([]target(nil), s.targets...), s.excluded...) {
		if configured.tableParam == "table_name" && matchesTable(configured, table) {
			return true
		}
	}
	return false
}

// namedViewTarget reports whether a view of the table is configured as a target of the connection.
func (s scope) namedViewTarget(table tableJSON, id, name string) bool {
	for _, configured := range s.targets {
		if configured.view == "" || !matchesTable(configured, table) {
			continue
		}
		if matchesView(configured, id, name) {
			return true
		}
	}
	return false
}

// findTable returns the table of the metadata a selected target names, or nil when the base holds none.
func findTable(document metadataJSON, selected target) *tableJSON {
	for i := range document.Metadata.Tables {
		if matchesTable(selected, document.Metadata.Tables[i]) {
			return &document.Metadata.Tables[i]
		}
	}
	return nil
}

// tableAllowed reports whether the table with this identifier is inside the connection's allow-list.
func (c *Client) tableAllowed(document metadataJSON, id string) bool {
	for _, table := range document.Metadata.Tables {
		if table.ID == id && c.scope.reaches(table) {
			return true
		}
	}
	return false
}

// joinsReachable reports whether every link and link-formula column of the table joins a table the
// connection reaches. A join that cannot be determined counts as not reachable.
func (c *Client) joinsReachable(document metadataJSON, table tableJSON) bool {
	for _, column := range table.Columns {
		if column.Type != "link" && column.Type != "link-formula" {
			continue
		}
		tree, _ := decodeBounded(column.RawData)
		data, _ := tree.(map[string]any)
		if !c.joinedTableReadable(document, table, column, data) {
			return false
		}
	}
	return true
}

// linkedViewMembersVisible reports whether the view members that can carry values of linked tables may be
// shown for the table: always for the whole base, and for a wildcard with exclusions only where no link
// leads to a table the connection does not reach. An allow-list never shows them.
func (c *Client) linkedViewMembersVisible(document metadataJSON, table tableJSON) bool {
	if c.scope.wholeBase() {
		return true
	}
	return c.scope.wildcard && c.joinsReachable(document, table)
}

// selectTable settles the table of a call. The spelling decides first, before any request; with exclusions
// the metadata then decides, after the token exchange: an excluded table or view, and a table the base does
// not hold, is refused like one outside the allow-list, without naming it.
func (c *Client) selectTable(ctx context.Context, op, raw string) (target, error) {
	selected, err := c.scope.selectTarget(raw)
	if err != nil {
		return target{}, providerError(op, err.Error())
	}
	if !c.scope.excludes() {
		return selected, nil
	}
	document, err := c.metadata(ctx, op)
	if err != nil {
		return target{}, err
	}
	found := findTable(document, selected)
	if found == nil {
		return target{}, providerError(op, "the selected SeaTable table no longer exists")
	}
	if !c.scope.reaches(*found) {
		return target{}, providerError(op, errOutsideAllowList)
	}
	if selected.view != "" {
		for _, view := range found.Views {
			if matchesView(selected, view.ID, view.Name) && c.scope.viewExcluded(*found, view.ID, view.Name) {
				return target{}, providerError(op, errOutsideAllowList)
			}
		}
	}
	return selected, nil
}

const errOutsideAllowList = "the selected SeaTable table is outside this connection's allow-list"
