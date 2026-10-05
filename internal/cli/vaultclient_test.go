package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultmigrate"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// testReleaseKeyEnv carries the public key of a test's own release, in authorized_keys form, to the qatlas
// processes the test starts from this test binary, which trust it in place of the compiled-in keys. Only the
// test binary reads it; qatlas itself has no such way.
const testReleaseKeyEnv = "QATLAS_CLI_TEST_RELEASE_KEY"

// trustTestReleaseKey makes releaseKeys return the key testReleaseKeyEnv carries, if any.
func trustTestReleaseKey() {
	line := os.Getenv(testReleaseKeyEnv)
	if line == "" {
		return
	}
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		panic("the test release key cannot be read: " + err.Error())
	}
	releaseKeys = func() []ssh.PublicKey { return []ssh.PublicKey{key} }
}

// vaultClientEnv makes the test binary a client of the vault process of the configuration directory in
// vaultClientEnv_DIR instead of running the tests, as a program other than the test binary: a test that runs
// the vault process from a copy of the test binary reaches it only from that same copy. The mode is what it
// does, and it prints the outcome on one line:
//
//   - status: "pid <pid> <locks at>"
//   - get: "value <value>" of credential and role in vaultClientEnv_GET, for the scope in vaultClientEnv_SCOPE
//   - handover: "prepared <answer>" for the release in vaultClientEnv_RELEASE, then, after a line on standard
//     input, "committed <successor pid>"
//
// and "error <message>" for any failure.
const vaultClientEnv = "QATLAS_CLI_TEST_VAULT_CLIENT"

func runVaultClient(mode string) int {
	client, err := vaultmigrate.ProcessClientOf(vault.New(os.Getenv(vaultClientEnv + "_DIR")))
	if err != nil {
		fmt.Println("error", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	switch mode {
	case "status":
		status, err := client.Status(ctx)
		if err != nil {
			fmt.Println("error", err)
			return 1
		}
		fmt.Println("pid", status.PID, status.LocksAt.Format(time.RFC3339Nano))
	case "get":
		var scope vault.Scope
		var pair [2]string
		if json.Unmarshal([]byte(os.Getenv(vaultClientEnv+"_SCOPE")), &scope) != nil ||
			json.Unmarshal([]byte(os.Getenv(vaultClientEnv+"_GET")), &pair) != nil {
			fmt.Println("error the request cannot be read")
			return 1
		}
		value, _, err := client.Get(ctx, pair[0], pair[1], scope)
		if err != nil {
			fmt.Println("error", err)
			return 1
		}
		fmt.Println("value", value)
	case "handover":
		var files vaultproc.ReleaseFiles
		if json.Unmarshal([]byte(os.Getenv(vaultClientEnv+"_RELEASE")), &files) != nil {
			fmt.Println("error the release cannot be read")
			return 1
		}
		behaviour, handover, err := client.PrepareHandover(ctx, files)
		if err != nil {
			fmt.Println("error", err)
			return 1
		}
		fmt.Println("prepared", behaviour)
		if handover == nil {
			return 0
		}
		if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
			_ = handover.Close()
			fmt.Println("error", err)
			return 1
		}
		pid, err := handover.Commit(ctx)
		if err != nil {
			fmt.Println("error", err)
			return 1
		}
		fmt.Println("committed", pid)
	default:
		fmt.Println("error unknown mode", mode)
		return 2
	}
	return 0
}
