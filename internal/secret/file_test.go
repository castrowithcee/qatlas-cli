package secret

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func tempFile(t *testing.T) *File {
	t.Helper()
	return NewFile(filepath.Join(t.TempDir(), FileName))
}

// setFixture writes one entry the way the removed Set once did, switch turned on. Nothing in this build
// writes credentials.yaml any more; this stands in for that legacy writer so the tests can still build the
// fixtures the compatibility reader, Delete, and Holds are checked against.
func setFixture(t *testing.T, f *File, credential, role, value string) {
	t.Helper()
	c, err := f.load()
	if err != nil && !errors.Is(err, ErrDisabled) && !errors.Is(err, ErrNoEntry) {
		t.Fatalf("load fixture: %v", err)
	}
	if c.Credentials == nil {
		c.Credentials = map[string]map[string]string{}
	}
	if c.Credentials[credential] == nil {
		c.Credentials[credential] = map[string]string{}
	}
	c.Credentials[credential][role] = value
	c.Version = fileVersion
	c.AllowPlaintext = true
	if err := f.write(c); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

// The compatibility reader delivers only what setFixture, standing in for the removed writer, put on disk
// with the switch set, at mode 0600.
func TestFileSet(t *testing.T) {
	f := tempFile(t)

	if _, err := os.Stat(f.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the file exists before anything was written: %v", err)
	}
	setFixture(t, f, credName, role, canaryPlaintext)

	info, err := os.Stat(f.Path())
	if err != nil {
		t.Fatalf("Stat() = %v", err)
	}
	// Windows synthesises the mode from the read-only attribute, so 0600 cannot show there.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
	data, err := os.ReadFile(f.Path())
	if err != nil {
		t.Fatalf("ReadFile() = %v", err)
	}
	if !strings.Contains(string(data), "allow_plaintext: true") {
		t.Errorf("file = %q, want the switch written", data)
	}
	if !strings.HasPrefix(string(data), "#") {
		t.Errorf("file = %q, want the explaining header first", data)
	}

	got, err := f.Get(credName, role)
	if err != nil || got != canaryPlaintext {
		t.Errorf("Get() = %q, %v, want the stored value", got, err)
	}
}

// Without the switch the file delivers nothing, however complete it looks.
func TestFileWithoutTheSwitch(t *testing.T) {
	f := tempFile(t)
	body := "version: 1\nallow_plaintext: false\ncredentials:\n  " + credName + ":\n    " + role + ": " + canaryPlaintext + "\n"
	if err := os.WriteFile(f.Path(), []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if _, err := f.Get(credName, role); !errors.Is(err, ErrDisabled) {
		t.Errorf("Get() = %v, want ErrDisabled", err)
	}
}

func TestFileGetStates(t *testing.T) {
	t.Run("an absent file is not enabled", func(t *testing.T) {
		if _, err := tempFile(t).Get(credName, role); !errors.Is(err, ErrDisabled) {
			t.Errorf("Get() = %v, want ErrDisabled", err)
		}
	})

	t.Run("a switched-on file without the entry", func(t *testing.T) {
		f := tempFile(t)
		setFixture(t, f, credName, "token-secret", canaryPlaintext)
		if _, err := f.Get(credName, role); !errors.Is(err, ErrNoEntry) {
			t.Errorf("Get() = %v, want ErrNoEntry", err)
		}
	})

	t.Run("a file that is not a fallback at all", func(t *testing.T) {
		f := tempFile(t)
		if err := os.WriteFile(f.Path(), []byte("nonsense: [1, 2\n"), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		_, err := f.Get(credName, role)
		if err == nil || errors.Is(err, ErrNoEntry) || errors.Is(err, ErrDisabled) {
			t.Errorf("Get() = %v, want a read failure", err)
		}
		if strings.Contains(err.Error(), "nonsense") {
			t.Errorf("error = %q, want it to quote no line of the file", err)
		}
	})
}

// Deleting the last entry removes the file, so no switched-on plaintext file stays behind empty.
func TestFileDelete(t *testing.T) {
	f := tempFile(t)
	setFixture(t, f, credName, role, canaryPlaintext)
	setFixture(t, f, credName, "token-secret", canaryPlaintext)

	if err := f.Delete(credName, role); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if _, err := os.Stat(f.Path()); err != nil {
		t.Fatalf("the file is gone while an entry remains: %v", err)
	}
	if err := f.Delete(credName, "token-secret"); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if _, err := os.Stat(f.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the empty file was kept: %v", err)
	}
	if err := f.Delete(credName, role); !errors.Is(err, ErrNoEntry) {
		t.Errorf("Delete() = %v, want ErrNoEntry", err)
	}
}

// switchOff turns the fallback off while leaving every entry in place, the state the file header itself
// suggests to a reader who wants the file to stop delivering.
func switchOff(t *testing.T, f *File) {
	t.Helper()
	data, err := os.ReadFile(f.Path())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	off := strings.Replace(string(data), "allow_plaintext: true", "allow_plaintext: false", 1)
	if err := os.WriteFile(f.Path(), []byte(off), fileMode); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// What Holds reports must be removable. The switch decides whether the file delivers a secret, not whether
// it can be cleaned up: an entry named as present that nothing can remove is a dead end.
func TestInertFileCanStillBeCleanedUp(t *testing.T) {
	f := tempFile(t)
	setFixture(t, f, credName, role, canaryPlaintext)
	setFixture(t, f, "other", role, canaryPlaintext)
	switchOff(t, f)

	// It delivers nothing, and it does hold something: two different questions, two different answers.
	if _, err := f.Get(credName, role); !errors.Is(err, ErrDisabled) {
		t.Errorf("Get() = %v, want the switch to keep it from delivering", err)
	}
	holds, err := f.Holds(credName, role)
	if err != nil || !holds {
		t.Fatalf("Holds() = %v, %v; want the entry on disk to be reported", holds, err)
	}

	if err := f.Delete(credName, role); err != nil {
		t.Fatalf("Delete() = %v, want the entry the file holds to be removable", err)
	}

	if holds, err := f.Holds(credName, role); err != nil || holds {
		t.Errorf("Holds() = %v, %v; want the entry gone", holds, err)
	}
	if holds, err := f.Holds("other", role); err != nil || !holds {
		t.Errorf("Holds() = %v, %v; want the other entry kept", holds, err)
	}
	// Deleting neither switches the fallback on nor off.
	data, err := os.ReadFile(f.Path())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), "allow_plaintext: false") {
		t.Errorf("file = %q, want the switch left as it stood", data)
	}
}

// The mode check governs every read and delete: a file others can read is not read from and not deleted
// from either, and the refusal names the fix.
func TestWidenedModeRefusesEveryOperation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes do not carry on Windows")
	}
	f := tempFile(t)
	setFixture(t, f, credName, role, canaryPlaintext)
	if err := os.Chmod(f.Path(), 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	var tooOpen *PermissionError
	if _, err := f.Get(credName, role); !errors.As(err, &tooOpen) {
		t.Errorf("Get() = %v, want the mode refusal", err)
	}
	if err := f.Delete(credName, role); !errors.As(err, &tooOpen) {
		t.Errorf("Delete() = %v, want the mode refusal", err)
	}
	if _, err := f.Holds(credName, role); !errors.As(err, &tooOpen) {
		t.Errorf("Holds() = %v, want the mode refusal", err)
	}
	if !strings.Contains(tooOpen.Error(), "chmod 600") {
		t.Errorf("error = %q, want it to name the fix", tooOpen)
	}
}

// All is how 'qatlas vault migrate' reads what to carry over: every entry on disk, regardless of the
// switch, and nothing for a file that was never written.
func TestFileAll(t *testing.T) {
	t.Run("absent file", func(t *testing.T) {
		all, err := tempFile(t).All()
		if err != nil || len(all) != 0 {
			t.Fatalf("All() = %v, %v, want none, nil", all, err)
		}
	})

	t.Run("switched off still returns every entry", func(t *testing.T) {
		f := tempFile(t)
		setFixture(t, f, credName, role, canaryPlaintext)
		setFixture(t, f, credName, "token-secret", "second-"+canaryPlaintext)
		setFixture(t, f, "other", role, "other-"+canaryPlaintext)
		switchOff(t, f)

		all, err := f.All()
		if err != nil {
			t.Fatalf("All() error = %v", err)
		}
		want := map[string]map[string]string{
			credName: {role: canaryPlaintext, "token-secret": "second-" + canaryPlaintext},
			"other":  {role: "other-" + canaryPlaintext},
		}
		if len(all) != len(want) || all[credName][role] != want[credName][role] ||
			all[credName]["token-secret"] != want[credName]["token-secret"] ||
			all["other"][role] != want["other"][role] {
			t.Errorf("All() = %v, want %v", all, want)
		}

		// The map handed out is a copy: mutating it must not reach back into the file.
		all[credName][role] = "mutated"
		again, err := f.All()
		if err != nil || again[credName][role] != canaryPlaintext {
			t.Errorf("All() after mutating an earlier result = %v, %v, want the original value unchanged", again, err)
		}
	})
}
