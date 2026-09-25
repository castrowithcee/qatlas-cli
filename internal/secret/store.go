package secret

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/zalando/go-keyring"
)

// StoreService is the service name every qatlas entry lives under: one namespace in Secret Service on
// Linux, in the macOS Keychain, and in the Windows Credential Manager, so the entries are recognisable in
// the platform's own key manager and removable without qatlas.
const StoreService = "qatlas-cli"

// StoreSelector names the environment variable that chooses the credential store. "auto", the default,
// uses the store of the platform. "none" replaces it with a store that holds nothing, which is what a CI
// run, a container, and every test wants: no D-Bus call, no unlock prompt, no dependency on the machine.
const StoreSelector = "QATLAS_CREDENTIAL_STORE"

// The values StoreSelector accepts. They are exported because a caller that switched the store off has to
// be able to say so: a command that skipped the store must not look like one that cleared it.
const (
	StoreAuto = "auto"
	StoreNone = "none"
)

// SystemStore returns the credential store this process should use.
//
// An unrecognised selector is an error rather than a silent "auto": a user who writes off or disabled
// means to switch the store off, and quietly doing the opposite is the one outcome they did not ask for.
// The message names the accepted values without quoting what was written.
//
// github.com/zalando/go-keyring is the whole dependency here. It is small, has no cgo, and covers exactly
// the three platforms this project targets. Anything richer would add a backend zoo that nothing in the
// cascade needs.
func SystemStore() (Store, error) {
	switch os.Getenv(StoreSelector) {
	case "", StoreAuto:
		return systemStore{}, nil
	case StoreNone:
		return unavailableStore{err: ErrDisabled}, nil
	default:
		return nil, fmt.Errorf("%s must be %s or %s", StoreSelector, StoreAuto, StoreNone)
	}
}

// StoreName returns what the platform calls the store SystemStore reaches, so a message can send a user to
// the place they know from their own system. The operating system is an argument, as runtime.GOOS spells
// it, so every name can be checked on any machine without reaching a store. The systems go-keyring serves
// through Secret Service are named as such; anything else is simply the system keyring.
func StoreName(goos string) string {
	switch goos {
	case "darwin":
		return "macOS Keychain"
	case "windows":
		return "Windows Credential Manager"
	case "linux", "freebsd", "openbsd", "netbsd", "dragonfly":
		return "Secret Service"
	}
	return "system keyring"
}

// StoreLabel names the system keyring together with what the platform calls it, for example "the system
// keyring (macOS Keychain)".
func StoreLabel(goos string) string {
	if name := StoreName(goos); name != "system keyring" {
		return "the system keyring (" + name + ")"
	}
	return "the system keyring"
}

// StoreAdvice says in one sentence what a store state means on the given platform and what to do about it.
// It is built from the state alone, so it never repeats what the platform reported and never names a
// value. States that need no advice return an empty string.
func StoreAdvice(state StoreState, goos string) string {
	label := StoreLabel(goos)
	switch state {
	case StoreLocked:
		fix := "unlock it"
		switch StoreName(goos) {
		case "macOS Keychain":
			fix = "unlock the login keychain, for example in Keychain Access"
		case "Windows Credential Manager":
			fix = "sign in to Windows as this user again"
		case "Secret Service":
			fix = "unlock its login collection in a desktop session of this user, for example in GNOME " +
				"Keyring or KWallet; a session without a desktop, such as SSH, cannot answer the unlock prompt"
		}
		return label + " is locked: " + fix
	case StoreUnavailable:
		fix := "make sure this session can reach it"
		switch StoreName(goos) {
		case "macOS Keychain":
			fix = "run qatlas in your logged-in macOS user session"
		case "Windows Credential Manager":
			fix = "run qatlas as the signed-in Windows user"
		case "Secret Service":
			fix = "start a provider such as GNOME Keyring or KWallet in this login session; SSH sessions " +
				"and containers usually have none"
		}
		return label + " cannot be reached: " + fix
	case StoreTimedOut:
		return fmt.Sprintf("%s did not answer within %s: answer the unlock prompt on the desktop, or unlock it "+
			"there first, then try again", label, storeTimeout)
	case StoreOff:
		return fmt.Sprintf("%s is switched off because %s=%s: unset it, or set it to %s, to use the keyring",
			label, StoreSelector, StoreNone, StoreAuto)
	}
	return ""
}

// storeTimeout bounds every call into the platform store; a request that ends sooner ends it sooner.
//
// The library offers no context, and the call goes to a service in another process: a half-started
// keyring daemon or an unlock prompt nobody answers can leave it waiting forever. Ten seconds is long
// enough for a person to answer a legitimate unlock dialog and leaves most of an invocation's time limit to
// the provider. It is a variable only so a test can shorten it.
var storeTimeout = 10 * time.Second

// noPromptKey marks a context whose request nobody can answer an unlock prompt for.
type noPromptKey struct{}

func withoutPrompt(ctx context.Context) context.Context {
	return context.WithValue(ctx, noPromptKey{}, true)
}

// promptable reports whether an unlock prompt can reach someone: the request is not unattended, and the
// session has a display to show it on.
func promptable(ctx context.Context) bool {
	if unattended, _ := ctx.Value(noPromptKey{}).(bool); unattended {
		return false
	}
	return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
}

type systemStore struct{}

func (systemStore) Get(ctx context.Context, key string) (string, error) {
	value, err := within(ctx, func(ctx context.Context) (string, error) {
		if err := ready(ctx); err != nil {
			return "", err
		}
		return keyring.Get(StoreService, key)
	})
	switch {
	case err == nil:
		return value, nil
	case errors.Is(err, keyring.ErrNotFound):
		return "", ErrNoEntry
	case errors.Is(err, ErrUnavailable):
		return "", err
	default:
		return "", classify(err)
	}
}

func (systemStore) Set(key, value string) error {
	_, err := within(context.Background(), func(ctx context.Context) (struct{}, error) {
		if err := ready(ctx); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, keyring.Set(StoreService, key, value)
	})
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrUnavailable):
		return err
	default:
		return classify(err)
	}
}

func (systemStore) Delete(key string) error {
	_, err := within(context.Background(), func(ctx context.Context) (struct{}, error) {
		if err := ready(ctx); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, keyring.Delete(StoreService, key)
	})
	switch {
	case err == nil:
		return nil
	case errors.Is(err, keyring.ErrNotFound):
		return ErrNoEntry
	case errors.Is(err, ErrUnavailable):
		return err
	default:
		return classify(err)
	}
}

// within runs one store operation for at most storeTimeout, and never past the end of ctx. A limit that
// passes is not an abort: it is the same class as a store that cannot be reached, so the cascade moves on
// and the stage says what happened. The operation gets the bounded context, so whatever of it honours one
// ends with it.
//
// The channel is buffered, so the operation can still finish and hand back its result after the deadline
// without blocking on a receiver that has gone away.
func within[T any](ctx context.Context, op func(context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()

	type outcome struct {
		value T
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		value, err := op(ctx)
		done <- outcome{value: value, err: err}
	}()

	select {
	case got := <-done:
		return got.value, got.err
	case <-ctx.Done():
		var zero T
		return zero, fmt.Errorf("%w: %w", ErrUnavailable, ErrTimedOut)
	}
}

// classify turns anything the platform reports into the one class the cascade acts on. The platform's own
// text is not passed on: it is phrased for the service rather than for a user, and the class together with
// StoreAdvice already says what happened and what to do, on every platform alike.
func classify(err error) error {
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "locked collection") ||
		strings.Contains(message, "failed to unlock correct collection") {
		return fmt.Errorf("%w: %w", ErrUnavailable, ErrLocked)
	}
	return ErrUnavailable
}

// unavailableStore stands in wherever no store can be reached. It keeps a missing service from becoming a
// dead end: the cascade simply moves on to the next stage.
type unavailableStore struct{ err error }

func (s unavailableStore) Get(context.Context, string) (string, error) { return "", s.err }
func (s unavailableStore) Set(string, string) error                    { return s.err }
func (s unavailableStore) Delete(string) error                         { return s.err }

// MemoryStore is a credential store that lives in this process only.
//
// It is exported on purpose: the resolver must be exercised without touching the store of the machine the
// tests run on, and the packages that need it are not this one.
type MemoryStore struct {
	mu          sync.Mutex
	entries     map[string]string
	unavailable error
}

// NewMemoryStore returns an empty in-process store.
func NewMemoryStore() *MemoryStore { return &MemoryStore{entries: map[string]string{}} }

// Fail makes every operation report err, which is how a machine without a running secret service behaves.
// Passing nil makes the store work again.
func (s *MemoryStore) Fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unavailable = err
}

func (s *MemoryStore) Get(_ context.Context, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unavailable != nil {
		return "", s.unavailable
	}
	value, ok := s.entries[key]
	if !ok {
		return "", ErrNoEntry
	}
	return value, nil
}

func (s *MemoryStore) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unavailable != nil {
		return s.unavailable
	}
	s.entries[key] = value
	return nil
}

func (s *MemoryStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unavailable != nil {
		return s.unavailable
	}
	if _, ok := s.entries[key]; !ok {
		return ErrNoEntry
	}
	delete(s.entries, key)
	return nil
}
