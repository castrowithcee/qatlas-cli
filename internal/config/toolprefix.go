package config

import "strings"

// toolPrefixSuffix is the only pattern a tools list knows: the last segment of a tool ID replaced by one
// '*'. There is no other wildcard, no regular expression and no exclusion.
const toolPrefixSuffix = ".*"

// IsToolPrefix reports whether a tools entry is written as a prefix rather than as an exact tool ID: it
// contains the wildcard character anywhere. Whether it is a valid prefix is decided by ToolPrefixStem.
func IsToolPrefix(entry string) bool {
	return strings.Contains(entry, "*")
}

// ToolPrefixStem returns the whole-segment stem of a valid prefix entry of provider, with its trailing dot
// ("<provider>.<segment>."), and true. A valid prefix ends in ".*", has no other '*', no empty segment, and
// names the provider's own ID (which may contain dots) plus at least one further segment, so "<provider>.*"
// alone is not valid: it would select the whole provider and make the tools list a second way to say
// "every tool of these effects". The stem always ends at a segment border, so "x.ia*" never matches "x.iam.get".
func ToolPrefixStem(provider, entry string) (string, bool) {
	stem, ok := toolPrefixShape(entry)
	if !ok || provider == "" || !strings.HasPrefix(stem, provider+".") || len(stem) == len(provider)+1 {
		return "", false
	}
	return stem, true
}

// toolPrefixShape checks the grammar that does not depend on a provider.
func toolPrefixShape(entry string) (string, bool) {
	if !strings.HasSuffix(entry, toolPrefixSuffix) || strings.Count(entry, "*") != 1 {
		return "", false
	}
	stem := entry[:len(entry)-1]
	for _, segment := range strings.Split(strings.TrimSuffix(stem, "."), ".") {
		if segment == "" || strings.TrimSpace(segment) != segment {
			return "", false
		}
	}
	return stem, true
}

// ToolPrefixCovers reports whether entry is a valid prefix of provider that selects tool. A tool that
// requires its exact ID in the allow-list is never selected by a prefix, because that tool is the boundary
// of privilege escalation and a prefix would release it without a person having named it.
func ToolPrefixCovers(provider, entry string, tool ToolMetadata) bool {
	stem, ok := ToolPrefixStem(provider, entry)
	return ok && !tool.RequiresToolAllowList && strings.HasPrefix(tool.ID, stem)
}

// ToolListCovers is the one question every selection asks of a tools list: does it name tool by its exact
// ID, or does one of its prefixes of provider cover it. Search, tools, describe, route selection and invoke
// reach it through Config.ConnectionRefusal; EffectsOfTools reaches it for the effective effects.
func ToolListCovers(provider string, tools []string, tool ToolMetadata) bool {
	for _, entry := range tools {
		if entry == tool.ID || ToolPrefixCovers(provider, entry, tool) {
			return true
		}
	}
	return false
}

// ToolPrefixMatches counts the registered tools of metadata that entry covers today, 0 for an entry that is
// no valid prefix of the provider.
func ToolPrefixMatches(metadata ProviderMetadata, entry string) int {
	n := 0
	for _, tool := range metadata.Tools {
		if ToolPrefixCovers(metadata.ID, entry, tool) {
			n++
		}
	}
	return n
}

// ToolsCovered counts the registered tools of metadata that a tools list selects today, by exact ID or by
// prefix, each tool once. Unlike len(tools) it does not depend on how many entries stand for them.
func ToolsCovered(metadata ProviderMetadata, tools []string) int {
	n := 0
	for _, tool := range metadata.Tools {
		if ToolListCovers(metadata.ID, tools, tool) {
			n++
		}
	}
	return n
}
