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
	"os/exec"
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
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// vaultProcessPlatform reports whether this platform runs a vault process.
const vaultProcessPlatform = true

// The descriptors a vault process inherits from 'qatlas vault unlock': the handover it reads the vault's
// key and secrets from, and the report it answers on once it listens or failed to.
const (
	handoverFD = 3
	reportFD   = 4
)

// maxHandover bounds what a vault process reads from its handover, a vault's key and its secrets.
const maxHandover = 16 << 20

// vaultStartTimeout bounds how long 'qatlas vault unlock' waits for the vault process it started.
const vaultStartTimeout = 10 * time.Second

// socketCheckInterval is how often a vault process checks that its socket is still in place.
const socketCheckInterval = 10 * time.Second

// Words the vault process reports its start with on the report descriptor. Anything else is the reason it
// did not start, which never carries a secret.
const (
	reportReady   = "ready"
	reportRunning = "running"
)

// startVaultProcess starts 'qatlas vault serve' for the vault configured at configPath, detached from this
// process and its session, hands it snap through an inherited pipe, and waits until client reaches it.
//
// The program is this very file, so the process passes the check the vault process makes of every client.
// Its standard streams are /dev/null and its working directory is /, so it holds no terminal and no
// directory of the session that started it. Neither the key nor any secret ever appears in its arguments
// or its environment: they travel through the pipe alone, which the process closes once it read them.
func startVaultProcess(ctx context.Context, configPath string, snap vault.Snapshot,
	client *vaultproc.Client) (vaultproc.Status, error) {
	program, err := os.Executable()
	if err != nil {
		return vaultproc.Status{}, fmt.Errorf("cannot find this program to start the vault process: %w", err)
	}
	handoverRead, handoverWrite, err := os.Pipe()
	if err != nil {
		return vaultproc.Status{}, err
	}
	defer handoverWrite.Close()
	reportRead, reportWrite, err := os.Pipe()
	if err != nil {
		_ = handoverRead.Close()
		return vaultproc.Status{}, err
	}
	defer reportRead.Close()

	cmd := exec.Command(program, "vault", "serve", "--config", configPath)
	cmd.Dir = "/"
	cmd.ExtraFiles = []*os.File{handoverRead, reportWrite} // handoverFD and reportFD
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	err = cmd.Start()
	// The process holds its own ends now; closing them here makes its end, or its report, an end of file.
	_ = handoverRead.Close()
	_ = reportWrite.Close()
	if err != nil {
		return vaultproc.Status{}, fmt.Errorf("cannot start the vault process: %w", err)
	}
	// Reaps the process should it end while this one still runs; it is not waited for otherwise.
	go func() { _ = cmd.Wait() }()

	deadline := time.Now().Add(vaultStartTimeout)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	data, err := json.Marshal(snap)
	if err != nil {
		return vaultproc.Status{}, errors.New("cannot encode the vault for the vault process")
	}
	_ = handoverWrite.SetWriteDeadline(deadline)
	_, err = handoverWrite.Write(data)
	clear(data)
	_ = handoverWrite.Close()
	if err != nil {
		return vaultproc.Status{}, errors.New("the vault process did not take the vault")
	}

	_ = reportRead.SetReadDeadline(deadline)
	report, err := bufio.NewReader(io.LimitReader(reportRead, 4096)).ReadString('\n')
	report = strings.TrimSpace(report)
	switch {
	case report == reportReady, report == reportRunning:
	case report != "":
		return vaultproc.Status{}, fmt.Errorf("the vault process did not start: %s", report)
	case errors.Is(err, os.ErrDeadlineExceeded):
		return vaultproc.Status{}, errors.New("the vault process did not report within " + vaultStartTimeout.String())
	default:
		return vaultproc.Status{}, errors.New("the vault process ended before it was ready")
	}

	// Whichever process listens, the one just started or one that won a race with it, it has to answer the
	// check every client makes before it counts as started.
	for {
		status, err := client.Status(ctx)
		if err == nil {
			return status, nil
		}
		if !errors.Is(err, vaultproc.ErrNotRunning) || time.Now().After(deadline) {
			return vaultproc.Status{}, fmt.Errorf("the vault process does not answer: %w", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// runVaultServe is 'qatlas vault serve', the vault process itself. It is started by 'qatlas vault unlock'
// alone, with the handover and the report it inherits; run any other way, it refuses before it reads
// anything.
func runVaultServe(opts *Options, reg *capability.Registry) error {
	if !inheritedPipe(handoverFD) || !inheritedPipe(reportFD) {
		return &UsageError{errors.New("'qatlas vault serve' is started by 'qatlas vault unlock'; run that instead")}
	}
	report := os.NewFile(reportFD, "vault-report")
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
	snap, err := readHandover(os.NewFile(handoverFD, "vault-handover"))
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
	idle, err := vaultIdleTimeout(path, reg)
	if err != nil {
		answer(err.Error())
		return err
	}
	socket, err := vaultproc.SocketPath(vault.New(filepath.Dir(path)).Dir())
	if err != nil {
		answer(err.Error())
		return err
	}
	listener, err := vaultproc.Listen(socket)
	if errors.Is(err, vaultproc.ErrRunning) {
		answer(reportRunning)
		return err
	}
	if err != nil {
		answer(err.Error())
		return err
	}

	server := vaultproc.NewServer(key, snap.Secrets)
	server.IdleTimeout = idle
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

	answer(reportReady)
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
	data, err := io.ReadAll(io.LimitReader(f, maxHandover+1))
	_ = f.Close()
	defer clear(data)
	if err != nil {
		return vault.Snapshot{}, errors.New("cannot read the vault handed over")
	}
	if len(data) > maxHandover {
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
