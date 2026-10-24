package ninja

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Writer writes Ninja build files with proper indentation and escaping.
type Writer struct {
	w              *bufio.Writer
	definedRules   map[string]bool
	definedTargets map[string]bool
	siso           bool
}

// NewWriter creates a new Ninja writer wrapping the provided io.Writer.
func NewWriter(w io.Writer) *Writer {
	return &Writer{
		w:              bufio.NewWriter(w),
		definedRules:   make(map[string]bool),
		definedTargets: make(map[string]bool),
	}
}

// EnableSiso switches the writer into Siso mode: Siso-specific rule and build
// bindings (restat_content, remote_enabled, sandbox_disabled, cache_disabled,
// phony_output) are emitted. Upstream Ninja rejects these extra bindings, so
// they are only written when this is enabled.
func (n *Writer) EnableSiso() { n.siso = true }

// SisoEnabled reports whether Siso-specific bindings are being emitted.
func (n *Writer) SisoEnabled() bool { return n.siso }

// Flush flushes buffered data to the underlying io.Writer.
func (n *Writer) Flush() error {
	return n.w.Flush()
}

// BlankLine writes a newline.
func (n *Writer) BlankLine() error {
	_, err := n.w.WriteString("\n")
	return err
}

// Comment writes a Ninja comment line.
func (n *Writer) Comment(text string) error {
	_, err := fmt.Fprintf(n.w, "# %s\n", text)
	return err
}

// Variable writes a Ninja top-level variable assignment.
func (n *Writer) Variable(name, value string) error {
	_, err := fmt.Fprintf(n.w, "%s = %s\n", name, value)
	return err
}

// ScopedVariable writes an indented variable under a rule or build edge.
func (n *Writer) ScopedVariable(name, value string) error {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\n", "$\n  ")
	_, err := fmt.Fprintf(n.w, "  %s = %s\n", name, value)
	return err
}

// Rule defines a Ninja build rule.
type Rule struct {
	Name        string
	Command     string
	Description string
	DepFile     string
	Deps        string
	Pool        string
	Restat      bool
	Generator   bool

	// Siso-specific bindings (only emitted in Siso mode; see EnableSiso).
	RestatContent   bool     // restat_content: content-digest cleanliness instead of mtime
	RemoteEnabled   bool     // remote_enabled: allow RBE offloading of this action
	RemoteTimeout   string   // remote_timeout: duration for remote execution
	SandboxDisabled bool     // sandbox_disabled: run without local action sandbox
	CacheDisabled   bool     // cache_disabled: never look up / populate the action cache
	PhonyOutput     bool     // phony_output: Android ninja-fork phony outputs
	ExtraSisoVars   []string // raw additional Siso bindings, each "name = value"
}

func sanitizeRuleName(name string) string {
	var b strings.Builder
	for _, ch := range name {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '.' || ch == '-' {
			b.WriteRune(ch)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

// Rule writes a Ninja rule declaration.
func (n *Writer) Rule(r Rule) error {
	r.Name = sanitizeRuleName(r.Name)
	if n.definedRules == nil {
		n.definedRules = make(map[string]bool)
	}
	if n.definedRules[r.Name] {
		return nil
	}
	n.definedRules[r.Name] = true

	if _, err := fmt.Fprintf(n.w, "rule %s\n", r.Name); err != nil {
		return err
	}
	if err := n.ScopedVariable("command", r.Command); err != nil {
		return err
	}
	if r.Description != "" {
		if err := n.ScopedVariable("description", r.Description); err != nil {
			return err
		}
	}
	if r.DepFile != "" {
		if err := n.ScopedVariable("depfile", r.DepFile); err != nil {
			return err
		}
	}
	if r.Deps != "" {
		if err := n.ScopedVariable("deps", r.Deps); err != nil {
			return err
		}
	}
	if r.Pool != "" {
		if err := n.ScopedVariable("pool", r.Pool); err != nil {
			return err
		}
	}
	if r.Restat {
		if err := n.ScopedVariable("restat", "1"); err != nil {
			return err
		}
	}
	if r.Generator {
		if err := n.ScopedVariable("generator", "1"); err != nil {
			return err
		}
	}
	if n.siso {
		if err := n.writeSisoBindings(r.SisoBindings()); err != nil {
			return err
		}
	}
	return n.BlankLine()
}

// SisoVar is a single Siso-specific ninja variable assignment.
type SisoVar struct {
	Name  string
	Value string
}

// SisoBindings returns the Siso-specific variable assignments configured on
// this rule. Only meaningful when the writer is in Siso mode.
func (r Rule) SisoBindings() []SisoVar {
	var out []SisoVar
	if r.RestatContent {
		out = append(out, SisoVar{"restat_content", "1"})
	}
	if r.RemoteEnabled {
		out = append(out, SisoVar{"remote_enabled", "true"})
	}
	if r.RemoteTimeout != "" {
		out = append(out, SisoVar{"remote_timeout", r.RemoteTimeout})
	}
	if r.SandboxDisabled {
		out = append(out, SisoVar{"sandbox_disabled", "true"})
	}
	if r.CacheDisabled {
		out = append(out, SisoVar{"cache_disabled", "true"})
	}
	if r.PhonyOutput {
		out = append(out, SisoVar{"phony_output", "1"})
	}
	for _, kv := range r.ExtraSisoVars {
		if i := strings.Index(kv, "="); i > 0 {
			out = append(out, SisoVar{strings.TrimSpace(kv[:i]), strings.TrimSpace(kv[i+1:])})
		}
	}
	return out
}

func (n *Writer) writeSisoBindings(bindings []SisoVar) error {
	for _, b := range bindings {
		if err := n.ScopedVariable(b.Name, b.Value); err != nil {
			return err
		}
	}
	return nil
}

// BuildEdge defines a Ninja build statement.
type BuildEdge struct {
	Outputs   []string
	Rule      string
	Inputs    []string
	Implicits []string
	OrderOnly []string
	Variables map[string]string

	// Siso-specific bindings (only emitted in Siso mode; see EnableSiso).
	RestatContent   bool
	RemoteEnabled   bool
	RemoteTimeout   string
	SandboxDisabled bool
	CacheDisabled   bool
	ExtraSisoVars   []string
}

// SisoBindings returns the Siso-specific variable assignments configured on
// this build edge. Only meaningful when the writer is in Siso mode.
func (b BuildEdge) SisoBindings() []SisoVar {
	var out []SisoVar
	if b.RestatContent {
		out = append(out, SisoVar{"restat_content", "1"})
	}
	if b.RemoteEnabled {
		out = append(out, SisoVar{"remote_enabled", "true"})
	}
	if b.RemoteTimeout != "" {
		out = append(out, SisoVar{"remote_timeout", b.RemoteTimeout})
	}
	if b.SandboxDisabled {
		out = append(out, SisoVar{"sandbox_disabled", "true"})
	}
	if b.CacheDisabled {
		out = append(out, SisoVar{"cache_disabled", "true"})
	}
	for _, kv := range b.ExtraSisoVars {
		if i := strings.Index(kv, "="); i > 0 {
			out = append(out, SisoVar{strings.TrimSpace(kv[:i]), strings.TrimSpace(kv[i+1:])})
		}
	}
	return out
}

// Build writes a Ninja build statement.
func (n *Writer) Build(b BuildEdge) error {
	b.Rule = sanitizeRuleName(b.Rule)
	if n.definedTargets == nil {
		n.definedTargets = make(map[string]bool)
	}

	var validOutputs []string
	for _, out := range b.Outputs {
		if !n.definedTargets[out] {
			validOutputs = append(validOutputs, out)
			n.definedTargets[out] = true
		}
	}
	if len(validOutputs) == 0 {
		return nil
	}
	b.Outputs = validOutputs

	var sb strings.Builder
	sb.WriteString("build ")
	if len(b.Outputs) > 0 {
		sb.WriteString(strings.Join(escapePaths(b.Outputs), " "))
	}
	sb.WriteString(": ")
	sb.WriteString(b.Rule)

	if len(b.Inputs) > 0 {
		sb.WriteString(" ")
		sb.WriteString(strings.Join(escapePaths(b.Inputs), " "))
	}
	if len(b.Implicits) > 0 {
		sb.WriteString(" | ")
		sb.WriteString(strings.Join(escapePaths(b.Implicits), " "))
	}
	if len(b.OrderOnly) > 0 {
		sb.WriteString(" || ")
		sb.WriteString(strings.Join(escapePaths(b.OrderOnly), " "))
	}
	sb.WriteString("\n")

	if _, err := n.w.WriteString(sb.String()); err != nil {
		return err
	}

	for k, v := range b.Variables {
		if err := n.ScopedVariable(k, v); err != nil {
			return err
		}
	}
	if n.siso {
		if err := n.writeSisoBindings(b.SisoBindings()); err != nil {
			return err
		}
	}
	return nil
}

// Subninja writes a subninja directive.
func (n *Writer) Subninja(path string) error {
	_, err := fmt.Fprintf(n.w, "subninja %s\n", path)
	return err
}

// Include writes an include directive.
func (n *Writer) Include(path string) error {
	_, err := fmt.Fprintf(n.w, "include %s\n", path)
	return err
}

// Default writes a default target directive.
func (n *Writer) Default(targets ...string) error {
	_, err := fmt.Fprintf(n.w, "default %s\n", strings.Join(escapePaths(targets), " "))
	return err
}

// Escape escapes Ninja special characters ($, :, space).
func Escape(s string) string {
	s = strings.ReplaceAll(s, "$", "$$")
	s = strings.ReplaceAll(s, ":", "$:")
	s = strings.ReplaceAll(s, " ", "$ ")
	return s
}

func escapePaths(paths []string) []string {
	res := make([]string, len(paths))
	for i, p := range paths {
		s := strings.ReplaceAll(p, "$", "$$")
		s = strings.ReplaceAll(s, ":", "$:")
		s = strings.ReplaceAll(s, " ", "$ ")
		res[i] = s
	}
	return res
}
