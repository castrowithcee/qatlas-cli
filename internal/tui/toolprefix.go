package tui

import (
	"github.com/castrowithcee/qatlas-connect/internal/config"
)

// toolEntryLabel is a tools entry as the screens show it: an exact ID stays as it is, a prefix keeps its
// own text and adds how many registered tools it covers today. It is never resolved into the IDs, which
// would hide that it also covers tools a later release registers. The rule for what a prefix covers
// stays in config.ToolPrefixMatches.
func (m *Model) toolEntryLabel(provider, entry string) string {
	if !config.IsToolPrefix(entry) {
		return entry
	}
	metadata, _ := m.cfg.ProviderMetadata(provider)
	return entry + " " + tr(tToolPrefixCovers, config.ToolPrefixMatches(metadata, entry))
}

// toolEntryLabels labels every entry of a tools list; see toolEntryLabel.
func (m *Model) toolEntryLabels(provider string, entries []string) []string {
	out := make([]string, len(entries))
	for i, entry := range entries {
		out[i] = m.toolEntryLabel(provider, entry)
	}
	return out
}

// toolsSelectedCount is the number of tools a tools list selects today, shown in place of the number of
// entries as soon as a prefix is among them: one prefix entry can stand for hundreds of tools, so counting
// entries would say almost nothing. A list of exact IDs keeps counting its entries, which also counts
// an entry the provider no longer registers.
func (m *Model) toolsSelectedCount(provider string, entries []string) int {
	for _, entry := range entries {
		if config.IsToolPrefix(entry) {
			metadata, _ := m.cfg.ProviderMetadata(provider)
			return config.ToolsCovered(metadata, entries)
		}
	}
	return len(entries)
}
