package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sort"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/web"
)

func newWebCommand(opts *Options, registry *capability.Registry) *cobra.Command {
	return &cobra.Command{
		Use:   "web",
		Short: "Show a local, one-time browser overview of providers, services, credentials, and connections",
		Long: "Web starts a local HTTP server on 127.0.0.1, an OS-assigned port, and prints the URL to couple\n" +
			"a browser with this run. It opens that browser where it can; where it cannot, the URL is the\n" +
			"only output and nothing else is tried.\n\n" +
			"The URL carries a random, one-time access token, valid for a few minutes and good for exactly\n" +
			"one browser. Opening it couples that browser to this process: the token is consumed, a session\n" +
			"cookie takes its place, and the browser is redirected to the same address without the token, so\n" +
			"it never sits in history, a bookmark, or a referrer. A reused or expired token is refused, and so\n" +
			"is every request from a browser that never redeemed one.\n\n" +
			"The coupled browser sees one page: the providers, services, credentials, and connections this\n" +
			"configuration and this build's registry describe, with no secret value ever read or shown, plus\n" +
			"this run's admin status. A future write needs its own, separate admin approval: with an\n" +
			"encrypted vault, either the coupled browser's own masked passphrase form, or, in this terminal\n" +
			"while it stays interactive, pressing enter and then typing the passphrase covertly, the same\n" +
			"hidden way every other management command reads one. A right passphrase approves the coupled\n" +
			"session for vault.admin_timeout (default 10m idle, renewed by activity); admin_timeout: 0\n" +
			"approves exactly the next change and nothing beyond it. A vault with no passphrase at all needs\n" +
			"no approval beyond coupling, and the page says so.\n\n" +
			"With admin approval active, the coupled browser can also add a credential (keyring, vault, or\n" +
			"env) and replace one secret role of an existing keyring or vault credential; a stored secret is\n" +
			"never shown back. Every credential page reloads the configuration fresh, so a change made in this\n" +
			"run is visible right away, and a save carries proof of the exact file it was shown, so a change\n" +
			"from elsewhere in the meantime is refused as a conflict instead of being overwritten. This run\n" +
			"offers no connection write yet and no remote access. Ending the process, with a signal or\n" +
			"otherwise, closes the listener and discards the session and any admin approval; nothing of this\n" +
			"run answers again.\n\n" +
			"TUI, CLI, and MCP stay independently usable; this command only adds a local browser view beside\n" +
			"them.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			path, err := config.Path(opts.Config)
			if err != nil {
				return err
			}
			store := config.NewStore(path, registry)
			cfg, err := store.Load()
			if err != nil {
				return classifyUserError(err)
			}

			secrets, err := webSecrets(opts)
			if err != nil {
				return err
			}

			server, err := web.New(buildOverview(registry, cfg), secrets.Vault(), cfg.VaultAdminTimeout(),
				store, secrets, opts.Redactor)
			if err != nil {
				return err
			}

			ctx, cancel := signal.NotifyContext(c.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			url := server.URL()
			fmt.Fprintf(c.OutOrStdout(), "qatlas: open %s to couple this browser (valid once, expires soon)\n", url)
			if checkInteractive() {
				if required, err := server.AdminPassphraseRequired(); err == nil && required {
					fmt.Fprintln(c.OutOrStdout(),
						"qatlas: press enter here to approve the coupled browser with the vault passphrase")
				}
			}
			open := opts.Opener
			if open == nil {
				open = openBrowser
			}
			if err := open(url); err != nil {
				fmt.Fprintln(c.ErrOrStderr(), "qatlas: could not open a browser automatically; use the URL above")
			}

			go runTerminalAdmin(ctx, c.InOrStdin(), c.OutOrStdout(), server)

			return server.Run(ctx)
		},
	}
}

// webSecrets returns the resolver 'qatlas web' reads its vault through. Like tuiSecrets (see
// internal/cli/tui.go) it never lets the resolver's own cascade ask this terminal for a passphrase on its
// own: this run's admin session, not an implicit prompt buried in credential resolution, is the one place
// a vault passphrase is ever asked for here, on the coupled browser's own masked form or explicitly on this
// terminal (see runTerminalAdmin).
func webSecrets(opts *Options) (*secret.Resolver, error) {
	secrets, err := opts.resolver()
	if err != nil {
		return nil, err
	}
	if v := secrets.Vault(); v != nil {
		secrets.WithVault(v, nil)
	}
	return secrets, nil
}

// runTerminalAdmin lets a person at this process's own terminal grant the coupled browser's admin
// approval, alongside its masked form: each time stdin delivers a line (pressing enter is enough; its
// content is discarded), it reads the vault passphrase covertly, the same hidden way every other management
// command does, through readVaultPassphrase, and grants it to the currently coupled session through
// server.VerifyAndGrantAdmin, the very check the browser's own form uses. It never runs at all without an
// interactive terminal, so a piped or headless run never blocks waiting for a line nobody sends, and it
// never accepts the passphrase as a command line argument, an environment variable, or a file. It returns
// once stdin reaches its end; ctx cancellation alone does not interrupt an in-flight read, since the
// process is exiting either way once the server itself stops.
func runTerminalAdmin(ctx context.Context, stdin io.Reader, stdout io.Writer, server *web.Server) {
	if !checkInteractive() {
		return
	}
	scanner := bufio.NewScanner(stdin)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return
		}
		if !server.SessionCoupled() {
			fmt.Fprintln(stdout, "qatlas: no browser is coupled yet; open the URL above first")
			continue
		}
		if required, err := server.AdminPassphraseRequired(); err == nil && !required {
			fmt.Fprintln(stdout, "qatlas: this vault has no passphrase; the coupled browser needs no approval")
			continue
		}
		passphrase, err := readVaultPassphrase("vault passphrase: ")
		if err != nil {
			fmt.Fprintln(stdout, "qatlas: could not read the passphrase on this terminal")
			continue
		}
		if err := server.VerifyAndGrantAdmin(passphrase); err != nil {
			if errors.Is(err, vault.ErrWrongPassphrase) {
				fmt.Fprintln(stdout, "qatlas: wrong passphrase")
			} else {
				fmt.Fprintln(stdout, "qatlas: admin approval refused")
			}
			continue
		}
		fmt.Fprintln(stdout, "qatlas: admin approval granted to the coupled browser")
	}
}

// buildOverview reads the secretfree overview a coupled browser is shown, from the configuration and the
// registry's provider metadata alone. It never resolves a secret and never reads a credential's stored
// values.
func buildOverview(registry *capability.Registry, cfg *config.Config) web.Overview {
	core := application.New(registry, cfg, nil, nil)
	providers := core.Providers().Providers
	connections := core.Connections("", nil).Connections

	overview := web.Overview{
		Providers:   make([]web.ProviderRow, 0, len(providers)),
		Services:    make([]web.ServiceRow, 0, len(cfg.Services)),
		Credentials: make([]web.CredentialRow, 0, len(cfg.Credentials)),
		Connections: make([]web.ConnectionRow, 0, len(connections)),
	}
	for _, p := range providers {
		overview.Providers = append(overview.Providers, web.ProviderRow{
			Provider: p.Provider, Description: p.Description, Note: p.Note,
			Tools: p.Tools, Connections: p.Connections, Configured: p.Configured,
		})
	}
	for _, name := range sortedKeys(cfg.Services) {
		service := cfg.Services[name]
		overview.Services = append(overview.Services, web.ServiceRow{
			Name: name, Provider: service.Provider, BaseURL: displayBaseURL(cfg.ServiceBaseURL(service)),
		})
	}
	for _, name := range sortedKeys(cfg.Credentials) {
		credential := cfg.Credentials[name]
		overview.Credentials = append(overview.Credentials, web.CredentialRow{
			Name: name, Provider: credential.Provider, Type: credential.Type,
		})
	}
	for _, cn := range connections {
		overview.Connections = append(overview.Connections, web.ConnectionRow{
			Name: cn.Name, Provider: cn.Provider, Description: cn.Description,
			Permissions: cn.Permissions, Tools: cn.Tools,
		})
	}
	return overview
}

// displayBaseURL returns base stripped of any access value a service's base_url might carry: userinfo,
// query, and fragment. A value that does not parse as a URL is not shown at all, since it cannot be
// stripped with confidence.
func displayBaseURL(base string) string {
	parsed, err := url.Parse(base)
	if err != nil {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

// sortedKeys returns the keys of m in stable, ascending order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// openBrowser starts the platform's default way of opening a URL, without waiting for it and without
// letting its output reach this process's own streams. It is a best-effort convenience: a nil return
// means the platform opener was started, not that a browser actually showed the page, and any failure
// simply leaves the printed URL as the way to continue.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// Reap the opener in the background once it exits, so it never lingers as a zombie process.
	go func() { _ = cmd.Wait() }()
	return nil
}
