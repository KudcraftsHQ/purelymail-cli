package dispatch

import (
	"errors"
	"fmt"
	"text/tabwriter"

	"github.com/KudcraftsHQ/purelymail-cli/internal/api"
	"github.com/KudcraftsHQ/purelymail-cli/internal/config"
	"github.com/spf13/cobra"
)

// ConfigCommand inspects credentials. It never prints a token: the useful
// question is "which credential am I about to use", and that can be answered
// without putting a secret on screen or in a shell history file.
func ConfigCommand(rt *Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "config",
		Short:        "Inspect profiles and credentials",
		GroupID:      "discovery",
		SilenceUsage: true,
	}

	show := &cobra.Command{
		Use:          "show",
		Short:        "Show the resolved settings for this invocation",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c := rt.Config
			view := map[string]any{
				"profile":      c.Profile,
				"config_file":  config.Path(),
				"api_url":      rt.Client.BaseURL,
				"api_token":    describeSecret(c.APIToken),
				"token_source": c.Source,
			}
			if rt.isStructured() {
				return rt.RenderValue(view)
			}
			tw := tabwriter.NewWriter(rt.Out, 0, 0, 2, ' ', 0)
			for _, k := range []string{"profile", "config_file", "api_url", "api_token", "token_source"} {
				fmt.Fprintf(tw, "%s\t%v\n", k, view[k])
			}
			return tw.Flush()
		},
	}

	profiles := &cobra.Command{
		Use:          "profiles",
		Short:        "List the profiles defined in the config file",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			file, err := config.Load()
			if err != nil {
				return err
			}
			names := file.Names()
			if rt.isStructured() {
				return rt.RenderValue(names)
			}
			if len(names) == 0 {
				fmt.Fprintf(rt.Out, "no profiles defined in %s\n", file.Path)
				return nil
			}
			for _, n := range names {
				fmt.Fprintln(rt.Out, n)
			}
			return nil
		},
	}

	verify := &cobra.Command{
		Use:   "verify",
		Short: "Check the token against the API (read-only)",
		Long: "Calls checkAccountCredit, which is read-only, and reports the account credit.\n\n" +
			"Exit 0 when the token works, 1 when no token is configured or the API\n" +
			"could not be reached, 2 when Purelymail refused the token.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !rt.DryRun {
				if err := rt.Config.RequireToken(); err != nil {
					return err
				}
			}
			raw, err := rt.Client.Do(cmd.Context(), "checkAccountCredit", nil)
			if err != nil {
				var apiErr *api.APIError
				if errors.As(err, &apiErr) && api.ExitCode(err) == api.ExitAuth {
					return err
				}
				// Anything else is "could not verify", not "refused": exit 1.
				return verifyError{err}
			}
			if rt.DryRun {
				return rt.Render(raw)
			}
			result := map[string]any{
				"ok":           true,
				"profile":      rt.Config.Profile,
				"token_source": rt.Config.Source,
				"api_url":      rt.Client.BaseURL,
			}
			if v, ok := rawValue(raw).(map[string]any); ok {
				result["credit"] = v["credit"]
			}
			return rt.RenderValue(result)
		},
	}

	cmd.AddCommand(show, profiles, verify)
	return cmd
}

// verifyError hides the API error from ExitCode so `config verify` exits 1 for
// anything other than a refused credential.
type verifyError struct{ err error }

func (e verifyError) Error() string { return "could not verify the token: " + e.err.Error() }

func describeSecret(v string) string {
	if v == "" {
		return "(not set)"
	}
	return fmt.Sprintf("set (%d chars)", len(v))
}
