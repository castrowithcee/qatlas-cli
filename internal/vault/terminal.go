package vault

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strings"

	"golang.org/x/term"
)

// ReadPassphrase prompts on the controlling terminal and reads a line back without echoing it, the
// interactive way an encrypted vault's passphrase is ever asked for: never as a command line argument, an
// environment variable, or a file.
//
// It opens the terminal itself rather than reading the process's own standard input, so it still works
// when standard input carries something else, such as the secret 'qatlas credential set' reads from a
// pipe. It reports ErrNoTerminal when the process is not interactive or no terminal can be opened for the
// prompt, which is how a headless session, a CI job, and an agent are told apart from a person.
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

// ReadConfirm asks a yes-or-no question on the controlling terminal and reads a plain, visible line back. It
// is how 'qatlas vault decrypt' and 'qatlas vault migrate' ask for the explicit confirmation a destructive
// step needs, kept apart from ReadPassphrase because the answer is not a secret and is shown as typed. Only
// "y" or "yes", ignoring case and surrounding space, counts as yes; anything else, including an empty line,
// is no. It reports ErrNoTerminal exactly like ReadPassphrase when no terminal can be opened.
func ReadConfirm(prompt string) (bool, error) {
	in, out, closeTTY, err := openTTY()
	if err != nil {
		return false, ErrNoTerminal
	}
	defer closeTTY()

	if _, err := fmt.Fprint(out, prompt); err != nil {
		return false, ErrNoTerminal
	}
	// bufio, not Fscanln: an empty line, plain enter for no, is not itself an error worth turning into
	// ErrNoTerminal the way a real I/O failure is.
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return false, ErrNoTerminal
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

// interactive reports whether a person can be at a terminal for this process: at least one of its standard
// streams is one. A process whose streams are all redirected, such as an agent, a CI job, or an MCP server,
// is not, even when a console or a controlling terminal happens to be reachable; opening that one anyway
// would wait for an answer nobody gives. It is a variable only so a test can decide it.
var interactive = func() bool {
	for _, f := range []*os.File{os.Stdin, os.Stdout, os.Stderr} {
		if term.IsTerminal(int(f.Fd())) {
			return true
		}
	}
	return false
}

// openTTY opens the controlling terminal for reading and writing a prompt, independently of the process's
// own stdin and stdout, and only for an interactive process. On Windows the console device is two files;
// everywhere else /dev/tty is both. When neither is available, the process's own stdin is used as a last
// resort, if it is itself a terminal.
func openTTY() (in, out *os.File, closeAll func(), err error) {
	if !interactive() {
		return nil, nil, nil, fmt.Errorf("no standard stream is a terminal")
	}
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
