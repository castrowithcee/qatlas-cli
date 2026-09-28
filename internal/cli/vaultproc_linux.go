//go:build linux

package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"filippo.io/age"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultmigrate"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// vaultProcessPlatform reports whether this platform runs a vault process.
const vaultProcessPlatform = true

// socketCheckInterval is how often a vault process checks that its socket is still in place.
const socketCheckInterval = 10 * time.Second

// runVaultServe is 'qatlas vault serve', the vault process itself. It is started by 'qatlas vault unlock',
// or by the TUI's own 'ctrl+l' (see vaultmigrate.StartProcess, which both run to start it), with the
// handover and the report it inherits; run any other way, it refuses before it reads anything.
func runVaultServe(opts *Options, reg *capability.Registry) error {
	if !inheritedPipe(vaultmigrate.HandoverFD) || !inheritedPipe(vaultmigrate.ReportFD) {
		return &UsageError{errors.New("'qatlas vault serve' is started by 'qatlas vault unlock'; run that instead")}
	}
	report := os.NewFile(vaultmigrate.ReportFD, "vault-report")
	reported := false
	answer := func(word string) {
		if !reported {
			reported = true
			fmt.Fprintln(report, word)
			_ = report.Close()
		}
	}
	defer answer("the vault process stopped before it was ready")

	// Nothing secret is in this process yet; from here on nobody else reads its memory.
	if err := vaultproc.Harden(); err != nil {
		answer(err.Error())
		return err
	}
	snap, err := readHandover(os.NewFile(vaultmigrate.HandoverFD, "vault-handover"))
	if err != nil {
		answer(err.Error())
		return err
	}
	key, err := age.ParseX25519Identity(snap.Identity)
	snap.Identity = ""
	if err != nil {
		err = errors.New("the vault key handed over cannot be read")
		answer(err.Error())
		return err
	}

	path, err := absConfigPath(opts)
	if err != nil {
		answer(err.Error())
		return err
	}
	idle, retentionDays, err := vaultProcessSettings(path, reg)
	if err != nil {
		answer(err.Error())
		return err
	}
	vaultDir := vault.New(filepath.Dir(path)).Dir()
	socket, err := vaultproc.SocketPath(vaultDir)
	if err != nil {
		answer(err.Error())
		return err
	}
	listener, err := vaultproc.Listen(socket)
	if errors.Is(err, vaultproc.ErrRunning) {
		answer(vaultmigrate.ReportRunning)
		return err
	}
	if err != nil {
		answer(err.Error())
		return err
	}

	server := vaultproc.NewServer(key, snap.Secrets, snap.Bindings)
	server.IdleTimeout = idle
	// While it runs, this process is the one writer that signs the invocation log of this vault, with the
	// retention the configuration had when it started.
	server.KeepLog(vaultDir, retentionDays)
	// It also approves open connection changes an agent token covers, in this same vault.
	server.ApproveWithTokens(vaultDir)
	snap.Secrets = nil

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
	defer signal.Stop(signals)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-signals:
			_ = server.Close()
		case <-ctx.Done():
		}
	}()
	go watchSocket(ctx, socket, func() { _ = server.Close() })

	answer(vaultmigrate.ReportReady)
	return server.Serve(listener)
}

// inheritedPipe reports whether fd is open and a pipe, which is what 'qatlas vault unlock' hands a vault
// process. A descriptor that is anything else is left alone.
func inheritedPipe(fd int) bool {
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil {
		return false
	}
	return stat.Mode&syscall.S_IFMT == syscall.S_IFIFO
}

// readHandover reads the vault's key and secrets from the pipe 'qatlas vault unlock' writes them to, and
// closes it. The bytes read are overwritten once decoded; an error never quotes them.
func readHandover(f *os.File) (vault.Snapshot, error) {
	data, err := io.ReadAll(io.LimitReader(f, vaultmigrate.MaxHandover+1))
	_ = f.Close()
	defer clear(data)
	if err != nil {
		return vault.Snapshot{}, errors.New("cannot read the vault handed over")
	}
	if len(data) > vaultmigrate.MaxHandover {
		return vault.Snapshot{}, errors.New("the vault handed over is too large")
	}
	var snap vault.Snapshot
	if err := json.Unmarshal(data, &snap); err != nil || snap.Identity == "" {
		return vault.Snapshot{}, errors.New("the vault handed over cannot be read")
	}
	return snap, nil
}

// watchSocket calls gone once the socket at path is removed or replaced, which is what happens to a socket
// in the runtime directory when the system removes that directory at the end of the last session. A vault
// process nobody can reach any more locks itself rather than holding the secrets until its idle timeout.
func watchSocket(ctx context.Context, path string, gone func()) {
	own, err := os.Lstat(path)
	if err != nil {
		gone()
		return
	}
	ticker := time.NewTicker(socketCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if now, err := os.Lstat(path); err != nil || !os.SameFile(own, now) {
				gone()
				return
			}
		}
	}
}

// systemdRoot is the root the systemd files sessionWarnings reads are found below. It is a variable only so
// a test can point it at a directory of its own.
var systemdRoot = "/"

// sessionWarnings says what would end the vault process, or cut it off from its socket, when the session
// that started it ends. It reads the files systemd-logind reads and asks nothing; a system without systemd
// has no such warning.
func sessionWarnings(socket string) []string {
	if _, err := os.Stat(filepath.Join(systemdRoot, "run", "systemd", "system")); err != nil {
		return nil
	}
	name := ""
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	var warnings []string
	if killsUserProcesses(name) {
		warnings = append(warnings, "systemd-logind ends every process of a session when the session closes "+
			"(KillUserProcesses=yes), the vault process included, so the vault locks when you log out; it keeps "+
			"running only once KillUserProcesses=no is set, or your user is listed in KillExcludeUsers=, in "+
			"/etc/systemd/logind.conf")
	}
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	inRuntime := filepath.IsAbs(runtime) && strings.HasPrefix(socket, filepath.Clean(runtime)+string(filepath.Separator))
	if name != "" && inRuntime {
		if _, err := os.Stat(filepath.Join(systemdRoot, "var", "lib", "systemd", "linger", name)); err != nil {
			warnings = append(warnings, fmt.Sprintf("linger is off for %s, so the system removes %s when your "+
				"last session ends, and with it the vault socket, which locks the vault; run 'loginctl "+
				"enable-linger' to keep the vault unlocked after you log out", name, runtime))
		}
	}
	return warnings
}

// killsUserProcesses reports whether systemd-logind ends the processes of name's sessions when they close:
// KillUserProcesses=yes, unless KillExcludeUsers= names the user or KillOnlyUsers= names others only. The
// files are read in the order logind reads them, and a later setting wins.
func killsUserProcesses(name string) bool {
	settings := map[string]string{}
	for _, path := range logindFiles() {
		readLogindFile(path, settings)
	}
	kill, _ := parseSystemdBool(settings["KillUserProcesses"])
	if !kill {
		return false
	}
	if name == "" {
		return true
	}
	if contains(strings.Fields(settings["KillExcludeUsers"]), name) {
		return false
	}
	if only := strings.Fields(settings["KillOnlyUsers"]); len(only) > 0 && !contains(only, name) {
		return false
	}
	return true
}

// logindFiles lists logind.conf and its drop-ins in the order systemd reads them: the main file, then the
// drop-ins sorted by name, where a drop-in in a later directory replaces one of the same name.
func logindFiles() []string {
	var files []string
	for _, main := range []string{"usr/lib/systemd/logind.conf", "etc/systemd/logind.conf"} {
		files = append(files, filepath.Join(systemdRoot, main))
	}
	dropIns := map[string]string{}
	for _, dir := range []string{"usr/lib/systemd/logind.conf.d", "usr/local/lib/systemd/logind.conf.d",
		"run/systemd/logind.conf.d", "etc/systemd/logind.conf.d"} {
		entries, err := os.ReadDir(filepath.Join(systemdRoot, dir))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".conf") {
				dropIns[entry.Name()] = filepath.Join(systemdRoot, dir, entry.Name())
			}
		}
	}
	names := make([]string, 0, len(dropIns))
	for name := range dropIns {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		files = append(files, dropIns[name])
	}
	return files
}

// readLogindFile adds the [Login] settings of one file to settings. A file that cannot be read adds none.
func readLogindFile(path string, settings map[string]string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	section := ""
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case line == "", strings.HasPrefix(line, "#"), strings.HasPrefix(line, ";"):
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
			section = line
		case section == "[Login]":
			if key, value, ok := strings.Cut(line, "="); ok {
				settings[strings.TrimSpace(key)] = strings.TrimSpace(value)
			}
		}
	}
}

func parseSystemdBool(value string) (bool, bool) {
	switch strings.ToLower(value) {
	case "1", "yes", "y", "true", "t", "on":
		return true, true
	case "0", "no", "n", "false", "f", "off":
		return false, true
	}
	return false, false
}
