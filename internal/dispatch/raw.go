package dispatch

import (
	"fmt"
	"strings"

	"github.com/KudcraftsHQ/purelymail-cli/internal/tree"
	"github.com/spf13/cobra"
)

// RawCommand is the escape hatch. The generated tree is only as current as the
// spec it was built from, so there has to be a way to call an operation the
// tree has never heard of, or to send a field it does not know.
func RawCommand(rt *Runtime) *cobra.Command {
	var (
		bodyInline string
		bodyFile   string
		yes        bool
	)

	cmd := &cobra.Command{
		Use:   "raw <operation>",
		Short: "Call any Purelymail API operation directly",
		Long: "POSTs a JSON body to https://purelymail.com/api/v0/<operation> with the\n" +
			"configured token and prints the unwrapped result.\n\n" +
			"The operation is the API's own camelCase name (addDomain, listUser, ...).\n" +
			"Operations whose name starts with \"delete\" need --yes, like their generated twins.",
		Example: "  purelymail raw listDomains --body '{\"includeShared\":true}'\n" +
			"  purelymail raw createUser -f user.json\n" +
			"  purelymail raw deleteDomain --body '{\"name\":\"example.com\"}' --yes",
		Args:         cobra.ExactArgs(1),
		GroupID:      "core",
		SilenceUsage: true,
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			t, err := tree.Load()
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return t.Operations(), cobra.ShellCompDirectiveNoFileComp
		},
	}

	f := cmd.Flags()
	f.StringVar(&bodyInline, "body", "", "JSON object to send as the request body (default {})")
	f.StringVarP(&bodyFile, "file", "f", "", "read the request body from a JSON file (- for stdin)")
	f.BoolVar(&yes, "yes", false, "confirm a destructive (delete*) operation")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		operation := strings.Trim(args[0], "/")
		if operation == "" || strings.ContainsAny(operation, "/?# ") {
			return fmt.Errorf("operation must be a bare name like listDomains, got %q", args[0])
		}
		body, err := loadBody(bodyInline, bodyFile, cmd.InOrStdin())
		if err != nil {
			return err
		}
		if t, err := tree.Load(); err == nil {
			if _, _, known := t.ByOperation(operation); !known {
				rt.Warnf("%s is not in the generated tree; sending it anyway", operation)
			}
		}
		if strings.HasPrefix(strings.ToLower(operation), "delete") && !yes && !rt.DryRun {
			return fmt.Errorf("%s is destructive; re-run with --yes to confirm, or --dry-run to preview", operation)
		}
		raw, err := rt.Client.Do(cmd.Context(), operation, body)
		if err != nil {
			return err
		}
		return rt.Render(raw)
	}
	return cmd
}
