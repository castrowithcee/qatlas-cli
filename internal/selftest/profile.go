package selftest

import "fmt"

// Profile sizes a run.
type Profile struct {
	Name    string
	Storage StorageProfile
}

// StorageProfile sizes the storage scenario. Entry counts are totals of services, credentials, and
// connections together and must be at least 3.
type StorageProfile struct {
	ReadEntries    []int
	Readers        []int
	ReadIterations int // loads per reader

	LockEntries []int
	LockWriters []int
	LockRounds  int

	AppendDays       []int
	AppendIterations int
}

// Standard is the profile of a real measurement.
func Standard() Profile {
	return Profile{Name: "standard", Storage: StorageProfile{
		ReadEntries: []int{1000, 5000, 10000}, Readers: []int{1, 4}, ReadIterations: 50,
		LockEntries: []int{1000, 10000}, LockWriters: []int{5, 20}, LockRounds: 3,
		AppendDays: []int{1, 90, 365, 3650}, AppendIterations: 200,
	}}
}

// Smoke is a small, quick profile that still covers every measurement.
func Smoke() Profile {
	return Profile{Name: "smoke", Storage: StorageProfile{
		ReadEntries: []int{30}, Readers: []int{1, 2}, ReadIterations: 5,
		LockEntries: []int{30}, LockWriters: []int{2, 3}, LockRounds: 1,
		AppendDays: []int{1, 5}, AppendIterations: 5,
	}}
}

// ProfileByName returns the named profile.
func ProfileByName(name string) (Profile, error) {
	switch name {
	case "standard":
		return Standard(), nil
	case "smoke":
		return Smoke(), nil
	}
	return Profile{}, fmt.Errorf("unknown selftest profile %q, want standard or smoke", name)
}

func (p Profile) validate() error {
	s := p.Storage
	for _, list := range [][]int{s.ReadEntries, s.Readers, s.LockEntries, s.LockWriters, s.AppendDays} {
		if len(list) == 0 {
			return fmt.Errorf("selftest profile %q is incomplete", p.Name)
		}
		for _, n := range list {
			if n < 1 {
				return fmt.Errorf("selftest profile %q has a non-positive size", p.Name)
			}
		}
	}
	for _, n := range append(append([]int(nil), s.ReadEntries...), s.LockEntries...) {
		if n < 3 {
			return fmt.Errorf("selftest profile %q has fewer than 3 entries", p.Name)
		}
	}
	if s.ReadIterations < 1 || s.LockRounds < 1 || s.AppendIterations < 1 {
		return fmt.Errorf("selftest profile %q has a non-positive iteration count", p.Name)
	}
	return nil
}
