package dispatch

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/KudcraftsHQ/purelymail-cli/internal/tree"
	"github.com/spf13/cobra"
)

// Discovery is a first-class part of the tool: list to see what exists,
// describe to see what an operation takes and returns, tree to dump the lot as
// JSON. An agent can find its way around the whole API from these three
// without reading any documentation.

// ListCommand enumerates resources, or the operations on one resource.
func ListCommand(rt *Runtime) *cobra.Command {
	var showOps bool

	cmd := &cobra.Command{
		Use:   "list [resource]",
		Short: "List resources, or the operations on one resource",
		Long: "With no argument, lists every resource. With a resource name, lists its\n" +
			"operations and the API operation each one calls.",
		Args:              cobra.MaximumNArgs(1),
		GroupID:           "discovery",
		SilenceUsage:      true,
		ValidArgsFunction: completeResources,
	}
	cmd.Flags().BoolVar(&showOps, "ops", false, "include every operation when listing all resources")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		t, err := tree.Load()
		if err != nil {
			return err
		}
		if len(args) == 1 {
			res, err := resourceOrSuggest(t, args[0])
			if err != nil {
				return err
			}
			return listOps(rt, res)
		}
		return listResources(rt, t, showOps)
	}
	return cmd
}

func listResources(rt *Runtime, t *tree.Tree, withOps bool) error {
	if rt.isStructured() {
		rows := make([]map[string]any, 0, len(t.Resources))
		for _, r := range t.Resources {
			names := make([]string, 0, len(r.Ops))
			for _, o := range r.Ops {
				names = append(names, o.Name)
			}
			row := map[string]any{"name": r.Name, "description": r.Description, "operations": names}
			if withOps {
				row["ops"] = opRows(&r)
			}
			rows = append(rows, row)
		}
		return rt.RenderValue(rows)
	}

	tw := tabwriter.NewWriter(rt.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RESOURCE\tOPERATIONS\tDESCRIPTION")
	for _, r := range t.Resources {
		names := make([]string, 0, len(r.Ops))
		for _, o := range r.Ops {
			names = append(names, o.Name)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Name, strings.Join(names, " "), r.Description)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if withOps {
		for i := range t.Resources {
			fmt.Fprintln(rt.Out)
			if err := listOps(rt, &t.Resources[i]); err != nil {
				return err
			}
		}
	}
	fmt.Fprintf(rt.Err, "\n`purelymail list <resource>` shows its operations; `purelymail describe <resource> <op>` what one takes.\n")
	return nil
}

func opRows(res *tree.Resource) []map[string]any {
	rows := make([]map[string]any, 0, len(res.Ops))
	for _, o := range res.Ops {
		rows = append(rows, map[string]any{
			"name":        o.Name,
			"command":     "purelymail " + res.Name + " " + o.Name,
			"operation":   o.Operation,
			"summary":     o.Summary,
			"destructive": o.Destructive,
			"custom":      o.Custom,
			"params":      len(o.Params),
		})
	}
	return rows
}

func listOps(rt *Runtime, res *tree.Resource) error {
	if rt.isStructured() {
		return rt.RenderValue(opRows(res))
	}
	tw := tabwriter.NewWriter(rt.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "COMMAND\tAPI OPERATION\tSUMMARY")
	for _, o := range res.Ops {
		summary := o.Summary
		if o.Destructive {
			summary += " [destructive: --yes]"
		}
		apiOp := o.Operation
		if o.Custom {
			apiOp += " (+local)"
		}
		fmt.Fprintf(tw, "%s %s\t%s\t%s\n", res.Name, o.Name, apiOp, summary)
	}
	return tw.Flush()
}

// DescribeCommand prints the full signature of an operation.
func DescribeCommand(rt *Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "describe <resource> [operation]",
		Short: "Show the fields an operation accepts and what it returns",
		Long: "Prints an operation's endpoint, every body field with its flag, type,\n" +
			"whether it is required, its API default and description, and the shape\n" +
			"of the result. Accepts the API's own name too: `describe addDomain`.",
		Args:         cobra.RangeArgs(1, 2),
		GroupID:      "discovery",
		SilenceUsage: true,
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			t, err := tree.Load()
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			switch len(args) {
			case 0:
				return t.ResourceNames(), cobra.ShellCompDirectiveNoFileComp
			case 1:
				res, ok := t.Resource(args[0])
				if !ok {
					return nil, cobra.ShellCompDirectiveNoFileComp
				}
				var out []string
				for _, o := range res.Ops {
					out = append(out, o.Name)
				}
				return out, cobra.ShellCompDirectiveNoFileComp
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
	}

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		t, err := tree.Load()
		if err != nil {
			return err
		}
		var (
			res *tree.Resource
			op  *tree.Op
		)
		if len(args) == 1 {
			// An API operation name resolves straight to its command.
			if r, o, ok := t.ByOperation(args[0]); ok {
				res, op = r, o
			} else {
				r, err := resourceOrSuggest(t, args[0])
				if err != nil {
					return err
				}
				return listOps(rt, r)
			}
		} else {
			res, err = resourceOrSuggest(t, args[0])
			if err != nil {
				return err
			}
			var ok bool
			op, ok = res.Op(args[1])
			if !ok {
				return unknownOp(res, args[1])
			}
		}
		if rt.isStructured() {
			return rt.RenderValue(map[string]any{
				"resource": res.Name,
				"command":  "purelymail " + res.Name + " " + op.Name,
				"url":      t.API.BaseURL + op.Path,
				"op":       op,
			})
		}
		return describeText(rt, t, res, op)
	}
	return cmd
}

func describeText(rt *Runtime, t *tree.Tree, res *tree.Resource, op *tree.Op) error {
	w := rt.Out
	fmt.Fprintf(w, "%s %s%s  (%s)\n", op.Method, t.API.BaseURL, op.Path, op.Operation)
	desc := op.Description
	if desc == "" || desc == op.Summary {
		desc = op.Summary
	} else {
		desc = strings.TrimSuffix(op.Summary, ".") + ". " + desc
	}
	fmt.Fprintf(w, "%s\n\n", desc)
	fmt.Fprintf(w, "  %s\n", strings.TrimSpace(opExample(res, op)))
	if op.Destructive {
		fmt.Fprintln(w, "\n  Destructive: refuses to run without --yes (--dry-run previews it).")
	}
	if op.Positional != "" {
		if p, ok := op.Param(op.Positional); ok {
			fmt.Fprintf(w, "\n  %s may be given as the first argument instead of --%s.\n", p.Name, p.Flag)
		}
	}
	fmt.Fprintln(w)

	if len(op.Params) == 0 {
		fmt.Fprintln(w, "No body fields (sends {}).")
	} else {
		label := "BODY FIELD"
		if op.Custom {
			label = "ARGUMENT"
		}
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintf(tw, "FLAG\t%s\tTYPE\tREQUIRED\tDEFAULT\tDESCRIPTION\n", label)
		for _, p := range op.Params {
			req := "no"
			if p.Required {
				req = "yes"
			}
			def := ""
			if p.Default != nil {
				def = fmt.Sprint(p.Default)
				if def == "" {
					def = `""`
				}
			}
			fmt.Fprintf(tw, "--%s\t%s\t%s\t%s\t%s\t%s\n", p.Flag, p.Name, p.Type, req, def, p.Description)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}

	if len(op.Returns) > 0 {
		fmt.Fprintln(w, "\nRETURNS")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, f := range op.Returns {
			fmt.Fprintf(tw, "  %s\t%s\t%s\n", f.Path, f.Type, f.Description)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	} else if !op.Custom {
		fmt.Fprintln(w, "\nRETURNS  {} on success")
	}
	if !op.Custom {
		fmt.Fprintln(w, "\nAlso: --body '<json>' or -f file.json for the whole body (flags win), --dry-run to preview.")
	}
	return nil
}

// TreeCommand dumps the whole catalog, which is the form a program or a model
// wants when it needs to reason about the API surface as a whole.
func TreeCommand(rt *Runtime) *cobra.Command {
	return &cobra.Command{
		Use:               "tree [resource...]",
		Short:             "Dump the command tree as JSON (every op, field and result path)",
		GroupID:           "discovery",
		SilenceUsage:      true,
		ValidArgsFunction: completeResources,
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := tree.Load()
			if err != nil {
				return err
			}
			if len(args) == 0 {
				return rt.RenderValue(t)
			}
			out := tree.Tree{API: t.API}
			for _, name := range args {
				res, err := resourceOrSuggest(t, name)
				if err != nil {
					return err
				}
				out.Resources = append(out.Resources, *res)
			}
			return rt.RenderValue(out)
		},
	}
}

func completeResources(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	t, err := tree.Load()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return t.ResourceNames(), cobra.ShellCompDirectiveNoFileComp
}

func resourceOrSuggest(t *tree.Tree, name string) (*tree.Resource, error) {
	if res, ok := t.Resource(name); ok {
		return res, nil
	}
	if hints := tree.Suggest(name, t.ResourceNames(), 5); len(hints) > 0 {
		return nil, fmt.Errorf("unknown resource %q; did you mean: %s", name, strings.Join(hints, ", "))
	}
	return nil, fmt.Errorf("unknown resource %q (have: %s)", name, strings.Join(t.ResourceNames(), ", "))
}

func unknownOp(res *tree.Resource, name string) error {
	names := make([]string, 0, len(res.Ops))
	for _, o := range res.Ops {
		names = append(names, o.Name)
	}
	return fmt.Errorf("%s has no operation %q (have: %s)", res.Name, name, strings.Join(names, ", "))
}
