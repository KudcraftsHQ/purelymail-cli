package dispatch

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/KudcraftsHQ/purelymail-cli/internal/tree"
	"github.com/spf13/pflag"
)

// binding ties one generated flag to the body field it came from.
type binding struct {
	param tree.Param
	str   *string
	num   *int64
	flt   *float64
	bl    *bool
	list  *[]string
}

// bindParams declares a flag per body field and returns the bindings so values
// can be collected after parsing.
func bindParams(fs *pflag.FlagSet, op *tree.Op, reserved map[string]bool) []*binding {
	bindings := make([]*binding, 0, len(op.Params))
	for _, p := range op.Params {
		if reserved[p.Flag] {
			// Stays reachable through --body.
			continue
		}
		b := &binding{param: p}
		usage := usageFor(p)
		switch p.Kind {
		case tree.KindList:
			b.list = fs.StringArray(p.Flag, nil, usage)
		case tree.KindBool:
			b.bl = fs.Bool(p.Flag, false, usage)
		case tree.KindInt:
			b.num = fs.Int64(p.Flag, 0, usage)
		case tree.KindFloat:
			b.flt = fs.Float64(p.Flag, 0, usage)
		default:
			b.str = fs.String(p.Flag, "", usage)
		}
		bindings = append(bindings, b)
	}
	return bindings
}

func usageFor(p tree.Param) string {
	var parts []string
	if p.Required {
		parts = append(parts, "(required)")
	}
	if p.Description != "" {
		parts = append(parts, p.Description)
	} else {
		parts = append(parts, p.Type)
	}
	if p.Kind == tree.KindList {
		parts = append(parts, "-- repeat the flag, or pass a JSON array")
	}
	if p.Default != nil {
		parts = append(parts, fmt.Sprintf("[API default %v]", p.Default))
	}
	return strings.Join(parts, " ")
}

// collect turns the flags the user actually set into wire values.
//
// Only changed flags are collected: false is a meaningful setting, so an unset
// flag has to be distinguishable from one set to its zero value -- otherwise
// `user modify` would silently switch features off.
func collect(fs *pflag.FlagSet, bindings []*binding) (map[string]any, error) {
	out := map[string]any{}
	for _, b := range bindings {
		if !fs.Changed(b.param.Flag) {
			continue
		}
		value, err := b.value()
		if err != nil {
			return nil, err
		}
		out[b.param.Name] = value
	}
	return out, nil
}

func (b *binding) value() (any, error) {
	p := b.param
	switch {
	case b.list != nil:
		return listValue(p, *b.list)
	case b.bl != nil:
		return *b.bl, nil
	case b.num != nil:
		return *b.num, nil
	case b.flt != nil:
		return *b.flt, nil
	}
	raw := *b.str
	if p.Kind == tree.KindJSON {
		if !json.Valid([]byte(raw)) {
			return nil, fmt.Errorf("--%s expects JSON (%s), got: %s", p.Flag, p.Type, raw)
		}
		return json.RawMessage(raw), nil
	}
	return raw, nil
}

// listValue accepts either a JSON array literal in a single flag, or the flag
// repeated once per element (comma-separated values are split too). Both end
// up as a JSON array on the wire.
func listValue(p tree.Param, values []string) (any, error) {
	if len(values) == 1 {
		trimmed := strings.TrimSpace(values[0])
		if strings.HasPrefix(trimmed, "[") {
			if !json.Valid([]byte(trimmed)) {
				return nil, fmt.Errorf("--%s looks like a JSON array but does not parse: %s", p.Flag, trimmed)
			}
			return json.RawMessage(trimmed), nil
		}
	}
	items := make([]any, 0, len(values))
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			item, err := coerceItem(p, part)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
	}
	return items, nil
}

func coerceItem(p tree.Param, v string) (any, error) {
	switch p.Item {
	case tree.KindInt:
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("--%s expects %s, got %q", p.Flag, p.Type, v)
		}
		return n, nil
	case tree.KindBool:
		bl, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("--%s expects %s, got %q", p.Flag, p.Type, v)
		}
		return bl, nil
	}
	return v, nil
}

// coercePositional converts a positional argument into the parameter's type.
func coercePositional(p tree.Param, v string) (any, error) {
	if p.Kind == tree.KindInt {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s expects an integer, got %q", p.Name, v)
		}
		return n, nil
	}
	return v, nil
}

// loadBody reads the base request body from --body or --file. "-" reads stdin.
func loadBody(inline, file string, stdin io.Reader) (map[string]any, error) {
	if inline != "" && file != "" {
		return nil, fmt.Errorf("--body and --file are mutually exclusive")
	}
	var raw []byte
	switch {
	case inline != "":
		raw = []byte(inline)
	case file == "-":
		b, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("read body from stdin: %w", err)
		}
		raw = b
	case file != "":
		b, err := os.ReadFile(strings.TrimPrefix(file, "@"))
		if err != nil {
			return nil, fmt.Errorf("read body: %w", err)
		}
		raw = b
	default:
		return map[string]any{}, nil
	}
	if strings.TrimSpace(string(raw)) == "" {
		return map[string]any{}, nil
	}
	var doc map[string]any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("request body must be a JSON object: %w", err)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	return doc, nil
}

// missingRequired names the required fields absent from a body, as flags.
func missingRequired(op *tree.Op, body map[string]any) []string {
	var missing []string
	for _, p := range op.Params {
		if !p.Required {
			continue
		}
		if _, ok := body[p.Name]; !ok {
			missing = append(missing, "--"+p.Flag)
		}
	}
	sort.Strings(missing)
	return missing
}
