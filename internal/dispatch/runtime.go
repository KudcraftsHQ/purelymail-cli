package dispatch

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/KudcraftsHQ/purelymail-cli/internal/api"
	"github.com/KudcraftsHQ/purelymail-cli/internal/config"
	"github.com/KudcraftsHQ/purelymail-cli/internal/output"
)

// Runtime is the state every command shares: resolved credentials, an API
// client, and how results should be printed. It is filled in once the global
// flags have been parsed and before any command body runs.
type Runtime struct {
	Config config.Config
	Client *api.Client

	Format output.Format
	// FormatExplicit records whether --output was given, which is how discovery
	// commands know to switch from readable text to structured output.
	FormatExplicit bool
	Pretty         bool
	Columns        []string
	DryRun         bool

	Out io.Writer
	Err io.Writer
}

// Render writes a raw JSON result in the configured format.
func (r *Runtime) Render(raw json.RawMessage) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		_, writeErr := fmt.Fprintln(r.Out, string(raw))
		return writeErr
	}
	return r.RenderValue(v)
}

// RenderValue writes an already-decoded value in the configured format.
func (r *Runtime) RenderValue(v any) error {
	return output.Write(r.Out, r.Format, v, output.Options{
		Pretty:  r.Pretty,
		Columns: r.Columns,
	})
}

// Warnf writes a note to stderr, which keeps stdout clean for piping.
func (r *Runtime) Warnf(format string, args ...any) {
	if r.Err == nil {
		return
	}
	fmt.Fprintf(r.Err, "purelymail: "+strings.TrimRight(format, "\n")+"\n", args...)
}

// isStructured reports whether discovery output should be machine-readable.
//
// API results default to JSON because that is what gets piped and parsed.
// Discovery output defaults to aligned text because that is what gets read --
// but asking for a format explicitly overrides that either way.
func (r *Runtime) isStructured() bool {
	return r.FormatExplicit && r.Format != output.Table
}
