package cli

import (
	"fmt"
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
			"configuration and this build's registry describe, with no secret value ever read or shown. This\n" +
			"run offers nothing else: no write, no terminal, no remote access. Ending the process, with a\n" +
			"signal or otherwise, closes the listener and discards the session; nothing of this run answers\n" +
			"again.\n\n" +
			"TUI, CLI, and MCP stay independently usable; this command only adds a local browser view beside\n" +
			"them.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			path, err := config.Path(opts.Config)
			if err != nil {
				return err
			}
			cfg, err := config.Load(path, registry)
			if err != nil {
				return classifyUserError(err)
			}

			server, err := web.New(buildOverview(registry, cfg))
			if err != nil {
				return err
			}

			ctx, cancel := signal.NotifyContext(c.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			url := server.URL()
			fmt.Fprintf(c.OutOrStdout(), "qatlas: open %s to couple this browser (valid once, expires soon)\n", url)
			open := opts.Opener
			if open == nil {
				open = openBrowser
			}
			if err := open(url); err != nil {
				fmt.Fprintln(c.ErrOrStderr(), "qatlas: could not open a browser automatically; use the URL above")
			}

			return server.Run(ctx)
		},
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
