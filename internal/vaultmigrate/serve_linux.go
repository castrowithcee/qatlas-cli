//go:build linux

package vaultmigrate

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// HandoverFD and ReportFD are the descriptors a vault process inherits from whatever started it with
// StartProcess: the handover it reads the vault's key and secrets from, and the report it answers on once
// it listens or failed to. Both 'qatlas vault unlock' and the TUI's own 'ctrl+l' start a vault process this
// same way, so the numbers, and the words on ReportFD below, are exported here instead of kept as a matching
// pair of unexported constants in two packages that would have to be edited together.
const (
	HandoverFD = 3
	ReportFD   = 4
)

// MaxHandover bounds what a vault process reads from its handover, a vault's key and its secrets.
const MaxHandover = 16 << 20

// StartTimeout bounds how long StartProcess waits for the vault process it started.
const StartTimeout = 10 * time.Second

// ReportReady and ReportRunning are the words a vault process reports its start with on ReportFD. Anything
// else is the reason it did not start, which never carries a secret.
const (
	ReportReady   = "ready"
	ReportRunning = "running"
)

// StartProcess starts 'qatlas vault serve' for the vault at configPath, detached from this process and its
// session, hands it snap through an inherited pipe, and waits until client reaches it. It is the shared core
// 'qatlas vault unlock' and the TUI's own 'ctrl+l' unlock both run, so a vault process is started exactly
// the same way whichever asks; only how the passphrase that produced snap was asked differs, and neither
// belongs to this package.
//
// The program started is this same running binary (see os.Executable), so the process passes the check a
// vault process makes of every client; its 'vault serve' subcommand is what actually answers the handover
// and the report, and lives in package cli, the only package that wires up commands. Its standard streams
// are /dev/null and its working directory is /, so it holds no terminal and no directory of the session
// that started it. Neither the key nor any secret ever appears in its arguments or its environment: they
// travel through the pipe alone, which the process closes once it has read them.
func StartProcess(ctx context.Context, configPath string, snap vault.Snapshot,
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
	cmd.ExtraFiles = []*os.File{handoverRead, reportWrite} // HandoverFD and ReportFD
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

	deadline := time.Now().Add(StartTimeout)
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
	case report == ReportReady, report == ReportRunning:
	case report != "":
		return vaultproc.Status{}, fmt.Errorf("the vault process did not start: %s", report)
	case errors.Is(err, os.ErrDeadlineExceeded):
		return vaultproc.Status{}, errors.New("the vault process did not report within " + StartTimeout.String())
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
