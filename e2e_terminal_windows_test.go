//go:build windows

package main

import (
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processAlive reports whether the process pid still runs.
func processAlive(pid int) bool {
	process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(process)
	event, err := windows.WaitForSingleObject(process, 0)
	return err == nil && event == uint32(windows.WAIT_TIMEOUT)
}

// killProcess ends the process pid at once.
func killProcess(pid int) {
	process, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(process)
	_ = windows.TerminateProcess(process, 1)
}

// runAtTerminal runs the binary in a pseudo console of its own, as Windows Terminal and an OpenSSH session
// with a terminal run it, types input once the passphrase prompt appeared, with the Enter key a console
// sends at the end of each line, and returns everything it wrote there and its exit code.
func runAtTerminal(t *testing.T, c *runner, input string, args ...string) (string, int) {
	t.Helper()
	var inRead, inWrite, outRead, outWrite windows.Handle
	if err := windows.CreatePipe(&inRead, &inWrite, nil, 0); err != nil {
		t.Fatalf("creating the console input: %v", err)
	}
	if err := windows.CreatePipe(&outRead, &outWrite, nil, 0); err != nil {
		t.Fatalf("creating the console output: %v", err)
	}
	var console windows.Handle
	err := windows.CreatePseudoConsole(windows.Coord{X: 200, Y: 50}, inRead, outWrite, 0, &console)
	// The pseudo console holds its own copies of its ends.
	_ = windows.CloseHandle(inRead)
	_ = windows.CloseHandle(outWrite)
	if err != nil {
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		t.Skipf("no pseudo console: %v", err)
	}
	consoleOpen := true
	closeConsole := func() {
		if consoleOpen {
			consoleOpen = false
			windows.ClosePseudoConsole(console)
		}
	}
	defer closeConsole()
	output := os.NewFile(uintptr(outRead), "console-output")
	defer output.Close()
	typing := os.NewFile(uintptr(inWrite), "console-input")
	defer typing.Close()

	var mu sync.Mutex
	var out strings.Builder
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := output.Read(buf)
			mu.Lock()
			out.Write(buf[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	text := func() string { mu.Lock(); defer mu.Unlock(); return out.String() }

	process := startInConsole(t, console, c, args)
	defer windows.CloseHandle(process)

	// A pseudo console may render the space after the colon only once something follows it.
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(text(), "passphrase:"); {
		if time.Now().After(deadline) {
			_ = windows.TerminateProcess(process, 1)
			t.Fatalf("no passphrase prompt appeared: %q", text())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := typing.WriteString(strings.ReplaceAll(input, "\n", "\r")); err != nil {
		t.Fatalf("typing at the console: %v", err)
	}
	if event, err := windows.WaitForSingleObject(process, 60*1000); err != nil || event != windows.WAIT_OBJECT_0 {
		_ = windows.TerminateProcess(process, 1)
		t.Fatalf("running %v did not end: %q", args, text())
	}
	var code uint32
	if err := windows.GetExitCodeProcess(process, &code); err != nil {
		t.Fatalf("running %v: %v", args, err)
	}
	// Closing the console ends its output once everything the process wrote was read.
	closeConsole()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	c.seen.WriteString(text())
	return text(), int(code)
}

// startInConsole starts the binary with args and the runner's environment attached to console, and
// returns its process.
func startInConsole(t *testing.T, console windows.Handle, c *runner, args []string) windows.Handle {
	t.Helper()
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		t.Fatalf("creating the process attributes: %v", err)
	}
	defer attrs.Delete()
	// The attribute's value is the console handle itself, not a pointer to it.
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&console)),
		unsafe.Sizeof(console)); err != nil {
		t.Fatalf("attaching the pseudo console: %v", err)
	}
	si := &windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(*si))

	commandLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{c.bin}, args...)))
	if err != nil {
		t.Fatal(err)
	}
	var pi windows.ProcessInformation
	if err := windows.CreateProcess(nil, commandLine, nil, nil, false,
		windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT, environmentBlock(c.env), nil,
		&si.StartupInfo, &pi); err != nil {
		t.Fatalf("starting %v: %v", args, err)
	}
	_ = windows.CloseHandle(pi.Thread)
	return pi.Process
}

// environmentBlock is env as CreateProcess takes it, with SYSTEMROOT added the way os/exec adds it, since
// the system does not work without it.
func environmentBlock(env []string) *uint16 {
	env = append([]string{}, env...)
	found := false
	for _, kv := range env {
		found = found || strings.HasPrefix(strings.ToUpper(kv), "SYSTEMROOT=")
	}
	if !found {
		env = append(env, "SYSTEMROOT="+os.Getenv("SYSTEMROOT"))
	}
	sort.Slice(env, func(i, j int) bool { return strings.ToUpper(env[i]) < strings.ToUpper(env[j]) })
	var block []uint16
	for _, kv := range env {
		utf16, err := windows.UTF16FromString(kv)
		if err != nil {
			continue
		}
		block = append(block, utf16...)
	}
	block = append(block, 0)
	return &block[0]
}
