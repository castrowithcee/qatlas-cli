package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// defaultCheckTimeout bounds one run of a prepared program; reading a configuration takes milliseconds.
const defaultCheckTimeout = 20 * time.Second

// ReadsConfig reports whether the program of the prepared release reads config, the bytes of a
// configuration file: it runs the verified program with 'config validate' against a copy in a private
// directory that is removed again, so neither the real file nor anything beside it is ever passed to it.
// Exit status 0 means it reads the file, any other exit status means it does not. A run that cannot be
// carried out (the program does not start, runs out of time, or is ended by a signal) is an error, not an
// answer, and the caller changes nothing. The program's output is discarded: it is not trusted and may
// quote the configuration.
func (p *Prepared) ReadsConfig(ctx context.Context, config []byte) (bool, error) {
	timeout := p.timeout
	if timeout <= 0 {
		timeout = defaultCheckTimeout
	}
	dir, err := os.MkdirTemp("", "qatlas-check-*")
	if err != nil {
		return false, fmt.Errorf("create the directory to check the configuration in: %w", err)
	}
	defer os.RemoveAll(dir)
	if err := os.Chmod(dir, 0o700); err != nil {
		return false, fmt.Errorf("restrict the check directory: %w", err)
	}
	name := "qatlas"
	if p.goos == "windows" {
		name += ".exe"
	}
	program := filepath.Join(dir, name)
	if err := os.WriteFile(program, p.payload.executable, 0o700); err != nil {
		return false, fmt.Errorf("store the program to check: %w", err)
	}
	copyPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(copyPath, config, 0o600); err != nil {
		return false, fmt.Errorf("store the configuration copy: %w", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, program, "--config", copyPath, "config", "validate")
	cmd.Dir = dir
	cmd.Env = checkEnvironment(dir)
	// No output pipes: with the default of the null device a program cannot keep Wait waiting through a
	// child that holds a pipe open.
	cmd.WaitDelay = time.Second
	err = cmd.Run()
	if err == nil {
		return true, nil
	}
	if runCtx.Err() != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return false, fmt.Errorf("the program to check did not finish within %s", timeout)
		}
		return false, runCtx.Err()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() > 0 {
		return false, nil
	}
	return false, fmt.Errorf("the program to check could not be run: %w", err)
}

// checkEnvironment is the whole environment of the checked program. It carries no variable of the caller
// besides what the system needs to start a program, and every place a program looks for a configuration,
// a vault, or a runtime directory points into dir: the caller's QATLAS_CONFIG and QATLAS_CLI_HOME, home
// directory, and credentials in the environment must stay out of reach of a program that is not yet trusted
// with them.
func checkEnvironment(dir string) []string {
	env := []string{
		"HOME=" + dir, "USERPROFILE=" + dir, "APPDATA=" + dir, "LOCALAPPDATA=" + dir,
		"TMPDIR=" + dir, "TEMP=" + dir, "TMP=" + dir,
		"XDG_CONFIG_HOME=" + dir, "XDG_DATA_HOME=" + dir, "XDG_STATE_HOME=" + dir, "XDG_CACHE_HOME=" + dir,
		"XDG_RUNTIME_DIR=" + dir, "NO_COLOR=1",
	}
	for _, keep := range []string{"PATH", "SystemRoot", "windir"} {
		if v, ok := os.LookupEnv(keep); ok {
			env = append(env, keep+"="+v)
		}
	}
	return env
}
