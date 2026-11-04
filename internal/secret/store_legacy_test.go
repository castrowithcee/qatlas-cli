package secret

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/zalando/go-keyring"
)

// fakeKeyring replaces the platform store for one test: entries by service and key, never the keyring of
// the machine the test runs on. The logic under test is the same for Secret Service, the Windows Credential
// Manager, and the macOS Keychain, which differ only below these three calls.
type fakeKeyring struct {
	mu      sync.Mutex
	entries map[[2]string]string
	failSet bool
}

func useFakeKeyring(t *testing.T) *fakeKeyring {
	t.Helper()
	fake := &fakeKeyring{entries: map[[2]string]string{}}
	saved := []any{storeReady, keyringGet, keyringSet, keyringDelete}
	storeReady = func(context.Context) error { return nil }
	keyringGet = func(service, key string) (string, error) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		value, ok := fake.entries[[2]string{service, key}]
		if !ok {
			return "", keyring.ErrNotFound
		}
		return value, nil
	}
	keyringSet = func(service, key, value string) error {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		if fake.failSet {
			return errors.New("the collection is locked")
		}
		fake.entries[[2]string{service, key}] = value
		return nil
	}
	keyringDelete = func(service, key string) error {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		if _, ok := fake.entries[[2]string{service, key}]; !ok {
			return keyring.ErrNotFound
		}
		delete(fake.entries, [2]string{service, key})
		return nil
	}
	t.Setenv(StoreSelector, StoreAuto)
	t.Cleanup(func() {
		storeReady = saved[0].(func(context.Context) error)
		keyringGet = saved[1].(func(string, string) (string, error))
		keyringSet = saved[2].(func(string, string, string) error)
		keyringDelete = saved[3].(func(string, string) error)
	})
	return fake
}

func (f *fakeKeyring) get(service, key string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	value, ok := f.entries[[2]string{service, key}]
	return value, ok
}

func TestSystemStoreReadsAndTakesOverALegacyEntry(t *testing.T) {
	fake := useFakeKeyring(t)
	fake.entries[[2]string{LegacyStoreService, "crm/token"}] = "legacy-value"

	value, err := systemStore{}.Get(context.Background(), "crm/token")

	if err != nil || value != "legacy-value" {
		t.Fatalf("Get() = %q, %v, want the legacy value", value, err)
	}
	if got, ok := fake.get(StoreService, "crm/token"); !ok || got != "legacy-value" {
		t.Errorf("entry under %s = %q, %v, want it taken over", StoreService, got, ok)
	}
	if _, ok := fake.get(LegacyStoreService, "crm/token"); !ok {
		t.Error("the legacy entry was removed; it stays for a rollback")
	}
}

func TestSystemStorePrefersTheCurrentEntry(t *testing.T) {
	fake := useFakeKeyring(t)
	fake.entries[[2]string{StoreService, "crm/token"}] = "rotated"
	fake.entries[[2]string{LegacyStoreService, "crm/token"}] = "old"

	value, err := systemStore{}.Get(context.Background(), "crm/token")

	if err != nil || value != "rotated" {
		t.Fatalf("Get() = %q, %v, want the current value", value, err)
	}
}

func TestSystemStoreReadsALegacyEntryEvenWhenTheCopyFails(t *testing.T) {
	fake := useFakeKeyring(t)
	fake.entries[[2]string{LegacyStoreService, "crm/token"}] = "legacy-value"
	fake.failSet = true

	value, err := systemStore{}.Get(context.Background(), "crm/token")

	if err != nil || value != "legacy-value" {
		t.Fatalf("Get() = %q, %v, want the legacy value", value, err)
	}
}

func TestSystemStoreMissingUnderBothNames(t *testing.T) {
	useFakeKeyring(t)

	if _, err := (systemStore{}).Get(context.Background(), "crm/token"); !errors.Is(err, ErrNoEntry) {
		t.Errorf("Get() error = %v, want ErrNoEntry", err)
	}
}

func TestSystemStoreWritesOnlyTheCurrentName(t *testing.T) {
	fake := useFakeKeyring(t)
	fake.entries[[2]string{LegacyStoreService, "crm/token"}] = "old"

	if err := (systemStore{}).Set("crm/token", "new"); err != nil {
		t.Fatal(err)
	}

	if got, _ := fake.get(StoreService, "crm/token"); got != "new" {
		t.Errorf("current entry = %q, want new", got)
	}
	if got, _ := fake.get(LegacyStoreService, "crm/token"); got != "old" {
		t.Errorf("legacy entry = %q, want it untouched", got)
	}
}

// A deleted secret must stay deleted: the legacy entry would otherwise answer the next read.
func TestSystemStoreDeleteRemovesBothNames(t *testing.T) {
	fake := useFakeKeyring(t)
	fake.entries[[2]string{StoreService, "crm/token"}] = "new"
	fake.entries[[2]string{LegacyStoreService, "crm/token"}] = "old"

	if err := (systemStore{}).Delete("crm/token"); err != nil {
		t.Fatal(err)
	}

	if _, err := (systemStore{}).Get(context.Background(), "crm/token"); !errors.Is(err, ErrNoEntry) {
		t.Errorf("Get() after Delete error = %v, want ErrNoEntry", err)
	}
	if err := (systemStore{}).Delete("crm/token"); !errors.Is(err, ErrNoEntry) {
		t.Errorf("second Delete() error = %v, want ErrNoEntry", err)
	}

	fake.entries[[2]string{LegacyStoreService, "only/legacy"}] = "old"
	if err := (systemStore{}).Delete("only/legacy"); err != nil {
		t.Errorf("Delete() of a legacy-only entry error = %v", err)
	}
	if _, ok := fake.get(LegacyStoreService, "only/legacy"); ok {
		t.Error("the legacy-only entry survived Delete")
	}
}

func TestAdoptLegacyCopiesAndReadsBack(t *testing.T) {
	fake := useFakeKeyring(t)
	fake.entries[[2]string{LegacyStoreService, "crm/token"}] = "a"
	fake.entries[[2]string{LegacyStoreService, "crm/secret"}] = "b"
	fake.entries[[2]string{StoreService, "wiki/token"}] = "current"
	fake.entries[[2]string{LegacyStoreService, "wiki/token"}] = "stale"

	copied, err := AdoptLegacy(context.Background(), []string{"crm/secret", "crm/token", "none/token", "wiki/token"})

	if err != nil || copied != 2 {
		t.Fatalf("AdoptLegacy() = %d, %v, want 2 copied", copied, err)
	}
	for key, want := range map[string]string{"crm/token": "a", "crm/secret": "b", "wiki/token": "current"} {
		if got, _ := fake.get(StoreService, key); got != want {
			t.Errorf("%s under %s = %q, want %q", key, StoreService, got, want)
		}
	}
	if _, ok := fake.get(StoreService, "none/token"); ok {
		t.Error("a key without a legacy entry was created")
	}
	if got, _ := fake.get(LegacyStoreService, "crm/token"); got != "a" {
		t.Error("the legacy entries must stay for a rollback")
	}

	copied, err = AdoptLegacy(context.Background(), []string{"crm/secret", "crm/token"})
	if err != nil || copied != 0 {
		t.Errorf("a repeated AdoptLegacy() = %d, %v, want nothing to copy", copied, err)
	}
}

func TestAdoptLegacyReportsAFailedCopy(t *testing.T) {
	fake := useFakeKeyring(t)
	fake.entries[[2]string{LegacyStoreService, "crm/token"}] = "a"
	fake.failSet = true

	copied, err := AdoptLegacy(context.Background(), []string{"crm/token"})

	if copied != 0 || !errors.Is(err, ErrUnavailable) {
		t.Errorf("AdoptLegacy() = %d, %v, want ErrUnavailable", copied, err)
	}
}

func TestAdoptLegacyLeavesASwitchedOffStoreAlone(t *testing.T) {
	fake := useFakeKeyring(t)
	fake.entries[[2]string{LegacyStoreService, "crm/token"}] = "a"
	t.Setenv(StoreSelector, StoreNone)

	if copied, err := AdoptLegacy(context.Background(), []string{"crm/token"}); err != nil || copied != 0 {
		t.Errorf("AdoptLegacy() = %d, %v, want nothing", copied, err)
	}
	if _, ok := fake.get(StoreService, "crm/token"); ok {
		t.Error("a switched-off store was written")
	}
}
