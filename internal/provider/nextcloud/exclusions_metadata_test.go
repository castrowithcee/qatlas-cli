package nextcloud

import (
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

func TestExclusionsAreDeclaredForTheEnforcedKindsOnly(t *testing.T) {
	meta, _ := registry(t).ProviderMetadata(Provider)
	for _, kind := range meta.Target.Kinds {
		want := slices.Contains([]string{kindFolder, kindNotes, kindCalendar, kindAddressbook, kindDeck, kindTalk}, kind.Name)
		if kind.Excludable != want {
			t.Errorf("kind %s: Excludable = %t, want %t", kind.Name, kind.Excludable, want)
		}
	}
}

func TestExcludeConfigurationIsCheckedByTheCore(t *testing.T) {
	reg := registry(t)
	document := func(targets, excludes string) string {
		return "version: 1\nservices:\n  nc:\n    provider: " + Provider + "\n    base_url: " + mainInstance +
			"\ncredentials:\n  key:\n    provider: " + Provider + "\n    type: env\n    values:\n      " +
			roleUserID + ": " + aliceUserEnv + "\n      " + roleAppPassword + ": " + aliceTokenEnv +
			"\nconnections:\n  c:\n    service: nc\n    credential: key\n    targets: " + targets +
			"\n    exclude: " + excludes + "\n"
	}
	for _, c := range [][2]string{
		{"[folder/Team, notes/Work]", "[folder/Team/Private, notes/Work/Secret]"},
		{"[folder, notes]", "[folder/Private, notes/Secret/Deep]"},
		{"[calendar, addressbook, deck]", "[calendar/work, addressbook/book, deck/42]"},
		{"[talk]", "[talk/abcd1234]"},
		{"[folder/Team, talk]", "[folder/Team/Private, talk/abcd1234]"},
	} {
		if _, err := config.Decode(strings.NewReader(document(c[0], c[1])), reg); err != nil {
			t.Errorf("targets %s exclude %s: %v", c[0], c[1], err)
		}
	}
	for _, c := range [][2]string{
		{"[talk]", "[talk/abc/d]"},
		{"[calendar]", "[calendar]"},
		{"[deck]", "[deck/abc]"},
		{"[folder/Team, account]", "[account]"},
		{"[folder/Team]", "[folder/Team]"},
		{"[folder/Team]", "[folder/Team/*]"},
		{"[folder/Team]", "[folder/Team/A, folder/Team/A]"},
		{"[folder/Team]", "[\"\"]"},
	} {
		_, err := config.Decode(strings.NewReader(document(c[0], c[1])), reg)
		if err == nil {
			t.Errorf("targets %s exclude %s were accepted", c[0], c[1])
			continue
		}
		if strings.Contains(err.Error(), "work") || strings.Contains(err.Error(), "abcd1234") {
			t.Errorf("the error quotes a value: %v", err)
		}
	}
}

func TestExclusionsDoNotChangeWhatTheTargetsBind(t *testing.T) {
	// The same connection with and without exclusions reads the same targets, so it offers the same tools.
	with, err := parseScopeExcluding([]string{"folder/Team", "notes"}, []string{"folder/Team/A", "notes/B"})
	if err != nil {
		t.Fatal(err)
	}
	without, _ := parseScope([]string{"folder/Team", "notes"})
	with.excludes = exclusions{}
	if with.hasFolder != without.hasFolder || with.notes.bound() != without.notes.bound() {
		t.Error("exclusions changed what the targets bind")
	}
}

func TestAggregatedValuesAreWarnedAboutInTheToolDescriptions(t *testing.T) {
	for _, descriptor := range []capability.Descriptor{filesList, filesStat, filesSearch} {
		if !strings.Contains(descriptor.Description, "excluded content") {
			t.Errorf("%s does not warn that aggregated values count excluded content: %s", descriptor.ID, descriptor.Description)
		}
	}
	for _, descriptor := range []capability.Descriptor{trashList, trashRestore, trashDelete} {
		if !strings.Contains(descriptor.Description, "excluded folder") {
			t.Errorf("%s does not name the exclusion: %s", descriptor.ID, descriptor.Description)
		}
	}
}
