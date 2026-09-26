package vault

import (
	"fmt"
	"os"
	"runtime"

	"golang.org/x/term"
)

// ReadPassphrase prompts on the controlling terminal and reads a line back without echoing it, the
// interactive way an encrypted vault's passphrase is ever asked for: never as a command line argument, an
// environment variable, or a file.
//
// It opens the terminal itself rather than reading the process's own standard input, so it still works
// when standard input carries something else, such as the secret 'qatlas credential set' reads from a
// pipe. It reports ErrNoTerminal when no terminal can be opened for the prompt, which is how a headless
// session, and an agent talking to qatlas over MCP, are told apart from an interactive one.
func ReadPassphrase(prompt string) (string, error) {
	in, out, closeTTY, err := openTTY()
	if err != nil {
		return "", ErrNoTerminal
	}
	defer closeTTY()

	if _, err := fmt.Fprint(out, prompt); err != nil {
		return "", ErrNoTerminal
	}
	passphrase, err := term.ReadPassword(int(in.Fd()))
	fmt.Fprintln(out)
	if err != nil {
		return "", ErrNoTerminal
	}
	return string(passphrase), nil
}

// openTTY opens the controlling terminal for reading and writing a prompt, independently of the process's
// own stdin and stdout. On Windows the console device is two files; everywhere else /dev/tty is both. When
// neither is available, the process's own stdin is used as a last resort, if it is itself a terminal.
func openTTY() (in, out *os.File, closeAll func(), err error) {
	if runtime.GOOS == "windows" {
		in, err = os.OpenFile("CONIN$", os.O_RDWR, 0)
		if err != nil {
			return nil, nil, nil, err
		}
		out, err = os.OpenFile("CONOUT$", os.O_WRONLY, 0)
		if err != nil {
			_ = in.Close()
			return nil, nil, nil, err
		}
		return in, out, func() { _ = in.Close(); _ = out.Close() }, nil
	}
	if tty, ttyErr := os.OpenFile("/dev/tty", os.O_RDWR, 0); ttyErr == nil {
		return tty, tty, func() { _ = tty.Close() }, nil
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		return os.Stdin, os.Stdin, func() {}, nil
	}
	return nil, nil, nil, fmt.Errorf("no terminal is attached")
}
