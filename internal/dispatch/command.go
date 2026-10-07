package dispatch

import (
	"fmt"
	"strings"

	"github.com/KudcraftsHQ/purelymail-cli/internal/tree"
	"github.com/spf13/cobra"
)

// Flags this tool owns on every generated operation. A body field with one of
// these names stays reachable through --body.
var reservedFlags = map[string]bool{"body": true, "file": true, "yes": true}

// customOps supplies the implementation of hand-written commands that the
// generator lists alongside the generated ones.
var customOps = map[string]func(rt *Runtime, res *tree.Resource, op *tree.Op) *cobra.Command{
	"domain/dns": dnsCommand,
}

// ResourceCommand builds the command for one resource and all its operations.
func ResourceCommand(rt *Runtime, res *tree.Resource) *cobra.Command {
	cmd := &cobra.Command{
		Use:   res.Name,
		Short: res.Description,
		Long: fmt.Sprintf("%s.\n\nRun `purelymail describe %s <operation>` for the fields an operation accepts.",
			res.Description, res.Name),
		GroupID:      "resources",
		SilenceUsage: true,
	}
	for i := range res.Ops {
		op := &res.Ops[i]
		if build, ok := customOps[res.Name+"/"+op.Name]; ok {
			cmd.AddCommand(build(rt, res, op))
			continue
		}
		cmd.AddCommand(opCommand(rt, res, op))
	}
	return cmd
}

func opCommand(rt *Runtime, res *tree.Resource, op *tree.Op) *cobra.Command {
	var (
		bodyInline string
		bodyFile   string
		yes        bool
	)

	use := op.Name
	args := cobra.NoArgs
	var positional *tree.Param
	if op.Positional != "" {
		positional, _ = op.Param(op.Positional)
		use += " [" + op.Positional + "]"
		args = cobra.MaximumNArgs(1)
	}

	cmd := &cobra.Command{
		Use:          use,
		Short:        shortFor(op),
		Long:         opLong(res, op),
		Example:      opExample(res, op),
		Args:         args,
		SilenceUsage: true,
	}

	flags := cmd.Flags()
	flags.StringVar(&bodyInline, "body", "", "JSON object used as the request body; explicit flags win")
	flags.StringVarP(&bodyFile, "file", "f", "", "read the request body from a JSON file (- for stdin)")
	if op.Destructive {
		flags.BoolVar(&yes, "yes", false, "confirm this destructive operation")
	}
	bindings := bindParams(flags, op, reservedFlags)

	cmd.RunE = func(cmd *cobra.Command, argv []string) error {
		body, err := loadBody(bodyInline, bodyFile, cmd.InOrStdin())
		if err != nil {
			return err
		}
		explicit, err := collect(flags, bindings)
		if err != nil {
			return err
		}
		for k, v := range explicit {
			body[k] = v
		}
		if len(argv) == 1 && positional != nil {
			if flags.Changed(positional.Flag) {
				return fmt.Errorf("%s given both as an argument and as --%s", positional.Name, positional.Flag)
			}
			v, err := coercePositional(*positional, argv[0])
			if err != nil {
				return err
			}
			body[positional.Name] = v
		}
		if missing := missingRequired(op, body); len(missing) > 0 {
			return fmt.Errorf("%s %s: missing required %s (`purelymail describe %s %s`)",
				res.Name, op.Name, strings.Join(missing, ", "), res.Name, op.Name)
		}
		if op.Destructive && !yes && !rt.DryRun {
			return fmt.Errorf("%s %s is destructive (%s); re-run with --yes to confirm, or --dry-run to preview",
				res.Name, op.Name, op.Description)
		}

		raw, err := rt.Client.Do(cmd.Context(), op.Operation, body)
		if err != nil {
			return err
		}
		return rt.Render(raw)
	}
	return cmd
}

func shortFor(op *tree.Op) string {
	s := strings.TrimSuffix(op.Summary, ".")
	if op.Destructive {
		s += " (destructive, needs --yes)"
	}
	return s
}

func opLong(res *tree.Resource, op *tree.Op) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s (%s)\n\n", op.Method, op.Path, op.Operation)
	desc := op.Description
	if desc == "" {
		desc = op.Summary
	}
	b.WriteString(desc)
	b.WriteString("\n")
	if op.Destructive {
		b.WriteString("\nDestructive: refuses to run without --yes (or --dry-run to preview).\n")
	}
	fmt.Fprintf(&b, "\nEvery body field is a flag below; --body/--file supply a whole JSON body.\n"+
		"`purelymail describe %s %s` shows the fields and the result shape.\n", res.Name, op.Name)
	return b.String()
}

func opExample(res *tree.Resource, op *tree.Op) string {
	var parts []string
	parts = append(parts, "purelymail", res.Name, op.Name)
	for _, p := range op.Params {
		if !p.Required {
			continue
		}
		if p.Name == op.Positional {
			parts = append(parts, "<"+p.Name+">")
			continue
		}
		if p.Kind == tree.KindBool {
			// pflag booleans take their value after "=", never as the next word.
			parts = append(parts, "--"+p.Flag+"=false")
			continue
		}
		parts = append(parts, "--"+p.Flag, placeholder(p))
	}
	if op.Destructive {
		parts = append(parts, "--yes")
	}
	return "  " + strings.Join(parts, " ")
}

func placeholder(p tree.Param) string {
	switch p.Kind {
	case tree.KindInt:
		return "<n>"
	case tree.KindList:
		return "<value>"
	}
	return "<" + p.Name + ">"
}
