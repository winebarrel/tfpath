package tfpath

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

type stepKind int

const (
	stepAttr      stepKind = iota // .name
	stepIndex                     // [0] or ["key"]
	stepAttrWild                  // .*
	stepIndexWild                 // [*]
	stepDeep                      // .**
)

type step struct {
	kind stepKind
	name string
	key  any // int or string for stepIndex
}

// Query is a parsed query such as `module.web.aws_s3_bucket.this.bucket`.
//
// A query is a Terraform address followed by a path into the value:
//
//	[module.<name>[<key>].]...<type>.<name>[<key>].<attr>...
//	[module.<name>[<key>].]...data.<type>.<name>[<key>].<attr>...
//	[module.<name>[<key>].]...var.<name>...
//	[module.<name>[<key>].]...local.<name>...
//	[module.<name>[<key>].]...output.<name>...
//
// "*" or "[*]" matches any name, key or element. "**" matches any number
// of module levels when used in the module path, and any depth when used
// inside a value. A resource or module address without a key matches all
// of its instances.
type Query struct {
	src   string
	steps []step
}

// String returns the query as it was written.
func (q *Query) String() string {
	return q.src
}

// ParseQuery parses a query string.
func ParseQuery(s string) (*Query, error) {
	p := &queryParser{src: s}
	if err := p.parse(); err != nil {
		return nil, fmt.Errorf("invalid query %q: %w", s, err)
	}
	return &Query{src: s, steps: p.steps}, nil
}

type queryParser struct {
	src   string
	pos   int
	steps []step
}

func (p *queryParser) parse() error {
	if err := p.name(); err != nil {
		return err
	}

	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case '.':
			p.pos++
			if err := p.name(); err != nil {
				return err
			}
		case '[':
			if err := p.index(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unexpected %q at offset %d", p.src[p.pos], p.pos)
		}
	}

	return nil
}

func isNameChar(c byte) bool {
	return c == '_' || c == '-' || c == '*' ||
		('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9')
}

func (p *queryParser) name() error {
	start := p.pos
	for p.pos < len(p.src) && isNameChar(p.src[p.pos]) {
		p.pos++
	}

	name := p.src[start:p.pos]

	switch {
	case name == "":
		return fmt.Errorf("expected name at offset %d", start)
	case name == "*":
		p.steps = append(p.steps, step{kind: stepAttrWild})
	case name == "**":
		p.steps = append(p.steps, step{kind: stepDeep})
	case strings.Contains(name, "*"):
		return fmt.Errorf("invalid wildcard %q at offset %d", name, start)
	default:
		p.steps = append(p.steps, step{kind: stepAttr, name: name})
	}

	return nil
}

func (p *queryParser) index() error {
	start := p.pos
	p.pos++ // '['

	end := p.pos
	if end < len(p.src) && p.src[end] == '"' {
		end++
		for end < len(p.src) && p.src[end] != '"' {
			if p.src[end] == '\\' {
				end++
			}
			end++
		}
		end++
	}

	rb := strings.IndexByte(p.src[min(end, len(p.src)):], ']')
	if rb < 0 {
		return fmt.Errorf("unclosed '[' at offset %d", start)
	}
	rb += min(end, len(p.src))

	inner := strings.TrimSpace(p.src[p.pos:rb])
	p.pos = rb + 1

	switch {
	case inner == "*":
		p.steps = append(p.steps, step{kind: stepIndexWild})
	case strings.HasPrefix(inner, `"`):
		s, err := strconv.Unquote(inner)
		if err != nil {
			return fmt.Errorf("invalid key %s at offset %d", inner, start)
		}
		p.steps = append(p.steps, step{kind: stepIndex, key: s})
	default:
		n, err := strconv.Atoi(inner)
		if err != nil {
			return fmt.Errorf("invalid index %q at offset %d", inner, start)
		}
		p.steps = append(p.steps, step{kind: stepIndex, key: n})
	}

	return nil
}

// Match is a value found by a query.
type Match struct {
	// Path is the concrete address of the value,
	// e.g. `module.web.aws_s3_bucket.this.tags.Name`.
	Path string

	// Value is the value as decoded by encoding/json (numbers are
	// json.Number). Parts that cannot be evaluated from the configuration
	// alone are strings holding the source expression, e.g.
	// "${aws_iam_role.app.arn}".
	Value any
}

// Query parses query and returns the matching values.
func (c *Config) Query(query string) ([]Match, error) {
	q, err := ParseQuery(query)
	if err != nil {
		return nil, err
	}
	return c.Match(q), nil
}

// Match returns the values matching q.
func (c *Config) Match(q *Query) []Match {
	m := &matcher{}
	m.instance(c.root, q.steps)

	// "**" can reach the same value more than once.
	seen := map[string]bool{}
	var out []Match
	for _, r := range m.out {
		if !seen[r.Path] {
			seen[r.Path] = true
			out = append(out, r)
		}
	}

	return out
}

type matcher struct {
	out []Match
}

func nameMatches(st step, name string) bool {
	return st.kind == stepAttrWild || (st.kind == stepAttr && st.name == name)
}

// splitKey splits off a leading index step.
func splitKey(steps []step) (*step, []step) {
	if len(steps) > 0 && (steps[0].kind == stepIndex || steps[0].kind == stepIndexWild) {
		return &steps[0], steps[1:]
	}
	return nil, steps
}

func keyMatches(st *step, key cty.Value) bool {
	if st == nil || st.kind == stepIndexWild {
		return true
	}

	if key == cty.NilVal || !key.IsKnown() {
		return false
	}

	switch k := st.key.(type) {
	case int:
		return key.Type() == cty.Number && key.Equals(cty.NumberIntVal(int64(k))).True()
	case string:
		return key.Type() == cty.String && key.AsString() == k
	}

	return false
}

func (m *matcher) instance(inst *instance, steps []step) {
	if len(steps) == 0 {
		return
	}

	st := steps[0]

	if st.kind == stepDeep {
		m.instance(inst, steps[1:])
		for _, name := range inst.callNames() {
			if e := inst.childInstances(name); e != nil {
				for _, item := range e.items {
					m.instance(item.inst, steps)
				}
			}
		}
		return
	}

	if st.kind == stepAttr {
		switch st.name {
		case "module":
			m.modules(inst, steps[1:])
			return
		case "data":
			m.resources(inst, true, steps[1:])
			return
		case "var", "local", "output":
			m.values(inst, st.name, steps[1:])
			return
		}
	}

	m.resources(inst, false, steps)
}

func (m *matcher) modules(inst *instance, steps []step) {
	if len(steps) == 0 {
		return
	}

	for _, name := range inst.callNames() {
		if !nameMatches(steps[0], name) {
			continue
		}

		e := inst.childInstances(name)
		if e == nil {
			continue
		}

		key, rest := splitKey(steps[1:])
		for _, item := range e.items {
			if keyMatches(key, item.key) {
				m.instance(item.inst, rest)
			}
		}
	}
}

func (m *matcher) resources(inst *instance, data bool, steps []step) {
	if len(steps) < 2 {
		return
	}

	for _, r := range inst.mod.resources {
		if r.data != data || !nameMatches(steps[0], r.typ) || !nameMatches(steps[1], r.name) {
			continue
		}

		key, rest := splitKey(steps[2:])
		e := inst.expand(r.block.Body)

		for _, item := range e.items {
			if keyMatches(key, item.key) {
				path := joinPath(inst.path, r.addr()) + formatKey(item.key)
				m.value(item.scope.renderBody(r.block.Body, true), rest, path)
			}
		}
	}
}

func (m *matcher) values(inst *instance, kind string, steps []step) {
	if len(steps) == 0 {
		return
	}

	var names []string
	switch kind {
	case "var":
		names = sortedKeys(inst.mod.variables)
	case "local":
		names = sortedKeys(inst.mod.locals)
	case "output":
		names = sortedKeys(inst.mod.outputs)
	}

	for _, name := range names {
		if !nameMatches(steps[0], name) {
			continue
		}

		var v any
		switch kind {
		case "var":
			v = renderVariable(inst, name)
		case "local":
			v = inst.scope().render(inst.mod.locals[name].Expr)
		case "output":
			v = inst.scope().render(inst.mod.outputs[name])
		}

		m.value(v, steps[1:], joinPath(inst.path, kind+"."+name))
	}
}

func renderVariable(inst *instance, name string) any {
	v := inst.variable(name)
	if v.IsWhollyKnown() {
		if a, err := toAny(v); err == nil {
			return a
		}
	}

	if expr := inst.variableExpr(name); expr != nil {
		return inst.callScope.render(expr)
	}

	return "${var." + name + "}"
}

func (m *matcher) value(v any, steps []step, path string) {
	if len(steps) == 0 {
		m.out = append(m.out, Match{Path: path, Value: v})
		return
	}

	st, rest := steps[0], steps[1:]

	switch st.kind {
	case stepAttr:
		if obj, ok := v.(map[string]any); ok {
			if e, ok := obj[st.name]; ok {
				m.value(e, rest, joinAttr(path, st.name))
			}
		}
	case stepIndex:
		switch k := st.key.(type) {
		case int:
			if list, ok := v.([]any); ok && k >= 0 && k < len(list) {
				m.value(list[k], rest, path+"["+strconv.Itoa(k)+"]")
			}
		case string:
			if obj, ok := v.(map[string]any); ok {
				if e, ok := obj[k]; ok {
					m.value(e, rest, joinAttr(path, k))
				}
			}
		}
	case stepAttrWild, stepIndexWild:
		m.children(v, func(e any, p string) { m.value(e, rest, p) }, path)
	case stepDeep:
		m.value(v, rest, path)
		m.children(v, func(e any, p string) { m.value(e, steps, p) }, path)
	}
}

func (m *matcher) children(v any, f func(any, string), path string) {
	switch c := v.(type) {
	case map[string]any:
		for _, k := range sortedKeys(c) {
			f(c[k], joinAttr(path, k))
		}
	case []any:
		for n, e := range c {
			f(e, path+"["+strconv.Itoa(n)+"]")
		}
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func joinPath(prefix, addr string) string {
	if prefix == "" {
		return addr
	}
	return prefix + "." + addr
}

func joinAttr(path, name string) string {
	if !hclsyntax.ValidIdentifier(name) {
		return path + "[" + strconv.Quote(name) + "]"
	}
	return joinPath(path, name)
}

func formatKey(key cty.Value) string {
	if key == cty.NilVal || !key.IsKnown() || key.IsNull() {
		return ""
	}

	switch key.Type() {
	case cty.Number:
		return "[" + key.AsBigFloat().Text('f', -1) + "]"
	case cty.String:
		return "[" + strconv.Quote(key.AsString()) + "]"
	}

	return ""
}
