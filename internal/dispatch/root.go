package dispatch

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/KudcraftsHQ/purelymail-cli/internal/api"
	"github.com/KudcraftsHQ/purelymail-cli/internal/config"
	"github.com/KudcraftsHQ/purelymail-cli/internal/output"
	"github.com/KudcraftsHQ/purelymail-cli/internal/tree"
	"github.com/spf13/cobra"
)

// Version is set at build time by the release pipeline.
var Version = "dev"

type globals struct {
	profile    string
	token      string
	format     string
	pretty     bool
	columns    []string
	dryRun     bool
	verbose    bool
	maxRetries int
	timeout    time.Duration
}

// NewRoot assembles the command tree for one invocation.
//
// The whole API is twenty operations, so every resource is registered up front
// and plain `--help` shows them all.
func NewRoot(out, errOut io.Writer) *cobra.Command {
	rt := &Runtime{Out: out, Err: errOut, Format: output.JSON}
	g := &globals{}

	root := &cobra.Command{
		Use:   "purelymail",
		Short: "A command line interface to the Purelymail API",
		Long: "purelymail exposes the Purelymail API as commands, generated from\n" +
			"Purelymail's own OpenAPI spec so every operation and body field is covered.\n\n" +
			"Start with `purelymail list` to see the resources and `purelymail describe\n" +
			"<resource> <op>` to see what an operation takes. Output is JSON by default.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       Version,
		Args:          cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			var names []string
			for _, c := range cmd.Commands() {
				names = append(names, c.Name())
			}
			if hints := tree.Suggest(args[0], names, 5); len(hints) > 0 {
				return fmt.Errorf("unknown command %q; did you mean: %s", args[0], strings.Join(hints, ", "))
			}
			return fmt.Errorf("unknown command %q (`purelymail list` shows every resource)", args[0])
		},
	}

	pf := root.PersistentFlags()
	pf.StringVar(&g.profile, "profile", "", "profile from the config file to use")
	pf.StringVar(&g.token, "token", "", "API token (overrides PURELYMAIL_API_TOKEN)")
	pf.StringVarP(&g.format, "output", "o", "json", "output format: "+strings.Join(output.Formats(), ", "))
	pf.BoolVar(&g.pretty, "pretty", false, "indent JSON output")
	pf.StringSliceVar(&g.columns, "columns", nil, "column order for table and csv output")
	pf.BoolVar(&g.dryRun, "dry-run", false, "print the request that would be sent, without sending it")
	pf.BoolVarP(&g.verbose, "verbose", "v", false, "log requests to stderr")
	pf.IntVar(&g.maxRetries, "max-retries", 2, "how many times to retry a throttled or unavailable API")
	pf.DurationVar(&g.timeout, "timeout", time.Minute, "per-request timeout")

	_ = root.RegisterFlagCompletionFunc("output",
		func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
			return output.Formats(), cobra.ShellCompDirectiveNoFileComp
		})

	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		return setup(rt, g, cmd)
	}

	root.AddGroup(
		&cobra.Group{ID: "resources", Title: "API resources:"},
		&cobra.Group{ID: "core", Title: "Escape hatch:"},
		&cobra.Group{ID: "discovery", Title: "Finding your way around:"},
	)

	t, err := tree.Load()
	if err == nil {
		for i := range t.Resources {
			root.AddCommand(ResourceCommand(rt, &t.Resources[i]))
		}
	}
	root.AddCommand(
		RawCommand(rt),
		ListCommand(rt),
		DescribeCommand(rt),
		TreeCommand(rt),
		ConfigCommand(rt),
	)
	return root
}

// setup resolves credentials and builds the client once the global flags have
// been parsed but before any command body runs.
func setup(rt *Runtime, g *globals, cmd *cobra.Command) error {
	cfg, err := config.Resolve(config.Overrides{Profile: g.profile, APIToken: g.token})
	if err != nil {
		return err
	}
	rt.Config = cfg

	format, err := output.Parse(g.format)
	if err != nil {
		return err
	}
	rt.Format = format
	rt.FormatExplicit = cmd.Flags().Changed("output")
	rt.Pretty = g.pretty
	rt.Columns = g.columns
	rt.DryRun = g.dryRun

	var trace io.Writer
	if g.verbose {
		trace = rt.Err
	}
	// PURELYMAIL_API_URL exists for tests and proxies; it is deliberately not a
	// flag, because pointing a token at an arbitrary host should take more
	// than a typo.
	baseURL := api.DefaultBaseURL
	if t, err := tree.Load(); err == nil && t.API.BaseURL != "" {
		baseURL = t.API.BaseURL
	}
	if override := os.Getenv("PURELYMAIL_API_URL"); override != "" {
		baseURL = override
	}

	rt.Client = api.New(api.Options{
		Token:      cfg.APIToken,
		BaseURL:    baseURL,
		MaxRetries: g.maxRetries,
		Timeout:    g.timeout,
		DryRun:     g.dryRun,
		Trace:      trace,
	})

	// Discovery and config commands work offline, so a missing token should
	// not stop them; anything that talks to the API needs one.
	if needsToken(cmd) && !g.dryRun {
		if err := cfg.RequireToken(); err != nil {
			return err
		}
	}
	return nil
}

// needsToken reports whether a command will actually call the API.
func needsToken(cmd *cobra.Command) bool {
	if f := cmd.Flags().Lookup("skip-ownership"); f != nil && f.Changed {
		return false
	}
	for c := cmd; c != nil; c = c.Parent() {
		switch c.Name() {
		case "list", "describe", "tree", "completion", "help", "config":
			// `user list` and friends are API calls, not discovery.
			if c.Parent() != nil && c.Parent().Parent() == nil {
				return false
			}
		}
	}
	return true
}

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	root := NewRoot(os.Stdout, os.Stderr)
	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "purelymail: %s\n", err)
		return api.ExitCode(err)
	}
	return 0
}
