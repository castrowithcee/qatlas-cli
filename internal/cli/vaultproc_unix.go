//go:build linux || darwin

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
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
//
// As a successor, started by a running vault process from the program an update installed (see
// vaultmigrate.StartSuccessor), it listens beside the vault socket, at vaultproc.NextPath, reports ready, and
// waits until that process moved its socket onto the vault socket; it keeps the time that process would have
// locked at.
func runVaultServe(opts *Options, reg *capability.Registry, successor bool) error {
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
	if successor && snap.LocksAt == nil {
		// A successor without the time to lock at would extend the unlock; it does not start at all.
		err = errors.New("the vault handed over does not say when it locks")
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
	listenAt := socket
	if successor {
		listenAt = vaultproc.NextPath(socket)
	}
	listener, err := vaultproc.Listen(listenAt)
	if errors.Is(err, vaultproc.ErrRunning) && !successor {
		answer(vaultmigrate.ReportRunning)
		return err
	}
	if err != nil {
		answer(err.Error())
		return err
	}

	server := vaultproc.NewServer(key, snap.Secrets, snap.Bindings)
	server.IdleTimeout = idle
	if snap.LocksAt != nil {
		server.LocksAt = *snap.LocksAt
	}
	// While it runs, this process is the one writer that signs the invocation log of this vault, with the
	// retention the configuration had when it started.
	server.KeepLog(vaultDir, retentionDays)
	// It also approves open connection changes an agent token covers, in this same vault.
	server.ApproveWithTokens(vaultDir)
	// And it hands the vault over to a successor from a verified release, should an update ask it to.
	server.AllowHandover(vaultDir, releaseKeys(), func(ctx context.Context, program string, next vault.Snapshot) (
		int, func(), error) {
		return vaultmigrate.StartSuccessor(ctx, program, path, next)
	})
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
			cancel()
		case <-ctx.Done():
		}
	}()

	answer(vaultmigrate.ReportReady)
	// A successor serves only once its socket is the vault socket; one that never gets there locks at once.
	if successor && vaultproc.AwaitHandover(ctx, listener, socket, vaultproc.HandoverTimeout) != nil {
		_ = server.Close()
	} else {
		go watchSocket(ctx, socket, func() { _ = server.Close() })
	}
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
