// Package tree exposes the generated Purelymail command tree.
//
// The tree is produced by tools/gen_command_tree.py from Purelymail's OpenAPI
// spec and embedded at build time. The whole API is twenty operations, so unlike
// a large API there is nothing to gain from splitting it: one 20 KB file, parsed
// once, on demand.
package tree

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

//go:embed schema.json
var schemaJSON []byte

// Kinds a body field can take. The dispatch layer coerces flag strings into the
// JSON shape the API expects based on these.
const (
	KindString = "string"
	KindInt    = "int"
	KindFloat  = "float"
	KindBool   = "bool"
	KindJSON   = "json"
	KindList   = "list"
)

// API describes where the tree came from and how to reach it.
type API struct {
	Title      string `json:"title"`
	Version    string `json:"version"`
	BaseURL    string `json:"base_url"`
	AuthHeader string `json:"auth_header"`
	DocsURL    string `json:"docs_url"`
	SpecURL    string `json:"spec_url"`
}

// Param is one field of an operation's JSON request body.
type Param struct {
	Name        string `json:"name"`
	Flag        string `json:"flag"`
	Type        string `json:"type"` // the spec's own type, shown in describe
	Kind        string `json:"kind"`
	Item        string `json:"item,omitempty"` // element kind when Kind == KindList
	Required    bool   `json:"required"`
	Default     any    `json:"default,omitempty"`
	Description string `json:"description,omitempty"`
}

// Field is one field of an operation's result, as a dotted path.
type Field struct {
	Path        string `json:"path"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
}

// Op is a single callable operation.
type Op struct {
	Name        string `json:"name"`
	Operation   string `json:"operation"` // the API's own name, e.g. addDomain
	Method      string `json:"method"`
	Path        string `json:"path"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	Destructive bool   `json:"destructive"`
	// Custom marks a hand-written command; its parameters describe the
	// command, not a request body.
	Custom bool `json:"custom,omitempty"`
	// Positional names the one parameter that may be given as an argument
	// instead of a flag, e.g. `domain add example.com`.
	Positional string  `json:"positional,omitempty"`
	Params     []Param `json:"params"`
	Returns    []Field `json:"returns"`
}

// Param returns the named parameter, matching the API name or the flag.
func (o *Op) Param(name string) (*Param, bool) {
	for i := range o.Params {
		if o.Params[i].Name == name || o.Params[i].Flag == name {
			return &o.Params[i], true
		}
	}
	return nil, false
}

// Resource is a group of related operations.
type Resource struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Ops         []Op   `json:"ops"`
}

// Op returns the named operation.
func (r *Resource) Op(name string) (*Op, bool) {
	for i := range r.Ops {
		if r.Ops[i].Name == name {
			return &r.Ops[i], true
		}
	}
	return nil, false
}

// Tree is the whole catalog.
type Tree struct {
	API       API        `json:"api"`
	Resources []Resource `json:"resources"`
}

var (
	once  sync.Once
	value Tree
	err   error
)

// Load parses the embedded tree once.
func Load() (*Tree, error) {
	once.Do(func() {
		if e := json.Unmarshal(schemaJSON, &value); e != nil {
			err = fmt.Errorf("parse embedded schema.json: %w", e)
		}
	})
	if err != nil {
		return nil, err
	}
	return &value, nil
}

// Resource returns the named resource.
func (t *Tree) Resource(name string) (*Resource, bool) {
	for i := range t.Resources {
		if t.Resources[i].Name == name {
			return &t.Resources[i], true
		}
	}
	return nil, false
}

// ByOperation finds an op by the API's own operation name (e.g. "addDomain"),
// skipping hand-written commands that merely call it.
func (t *Tree) ByOperation(operation string) (*Resource, *Op, bool) {
	for i := range t.Resources {
		r := &t.Resources[i]
		for j := range r.Ops {
			if r.Ops[j].Operation == operation && !r.Ops[j].Custom {
				return r, &r.Ops[j], true
			}
		}
	}
	return nil, nil, false
}

// Operations lists every API operation name, sorted.
func (t *Tree) Operations() []string {
	var out []string
	for _, r := range t.Resources {
		for _, o := range r.Ops {
			if !o.Custom {
				out = append(out, o.Operation)
			}
		}
	}
	sort.Strings(out)
	return out
}

// ResourceNames lists resource names in catalog order.
func (t *Tree) ResourceNames() []string {
	out := make([]string, len(t.Resources))
	for i, r := range t.Resources {
		out[i] = r.Name
	}
	return out
}

// Suggest returns names close to a mistyped one, for error messages: prefixes
// and substrings first, then anything within a small edit distance.
func Suggest(name string, candidates []string, limit int) []string {
	lower := strings.ToLower(name)
	var prefix, substring, near []string
	budget := len(lower)/3 + 1
	for _, n := range candidates {
		ln := strings.ToLower(n)
		switch {
		case strings.HasPrefix(ln, lower):
			prefix = append(prefix, n)
		case strings.Contains(ln, lower) || (len(ln) > 2 && strings.Contains(lower, ln)):
			substring = append(substring, n)
		case editDistance(lower, ln) <= budget:
			near = append(near, n)
		}
	}
	out := append(append(prefix, substring...), near...)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}
