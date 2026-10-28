package provider

import (
	"fmt"
	"slices"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// ExcludeFormProblems compares the exclusion forms a provider declares with its exclusion parser, for the
// provider's own test. accepted maps every declared exclusion form to an example value spelled in it; each must
// fit its form, pass the core check, and be read by parse. rejected lists forms of the provider that are not
// declared as exclusions; the core check must refuse each and parse must refuse it too, so the declaration
// never widens the parser. parse reads one exclusion next to targets the caller chose, so that the value alone
// decides.
func ExcludeFormProblems(target config.TargetMetadata, accepted map[string]string, rejected []string, parse func(exclude string) error) []string {
	var problems []string
	if !target.Excludes() {
		return []string{"the provider declares no exclusions"}
	}
	declared := map[string]bool{}
	for _, kind := range target.Kinds {
		for _, form := range kind.ExcludeForms {
			declared[form] = true
			if !slices.Contains(kind.Forms, form) {
				problems = append(problems, fmt.Sprintf("kind %s: exclusion form %q is no form of the kind", kind.Name, form))
			}
			if _, ok := accepted[form]; !ok {
				problems = append(problems, fmt.Sprintf("kind %s: exclusion form %q has no example", kind.Name, form))
			}
		}
	}
	for form, value := range accepted {
		if !declared[form] {
			problems = append(problems, fmt.Sprintf("example for undeclared exclusion form %q", form))
			continue
		}
		if !config.FitsForm(strings.Split(value, "/"), strings.Split(form, "/")) {
			problems = append(problems, fmt.Sprintf("example %q does not fit form %q", value, form))
		}
		if findings := target.CheckExcludes([]string{value}, nil); len(findings) != 0 {
			problems = append(problems, fmt.Sprintf("form %q: the core refuses the example %q: %+v", form, value, findings))
		}
		if err := parse(value); err != nil {
			problems = append(problems, fmt.Sprintf("form %q: the parser refuses the example %q: %v", form, value, err))
		}
	}
	for _, value := range rejected {
		if findings := target.CheckExcludes([]string{value}, nil); len(findings) != 1 {
			problems = append(problems, fmt.Sprintf("the core accepts the undeclared form %q", value))
		}
		if err := parse(value); err == nil {
			problems = append(problems, fmt.Sprintf("the parser accepts the undeclared form %q", value))
		}
	}
	return problems
}
