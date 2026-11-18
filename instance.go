package tfpath

import (
	"path/filepath"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
	"github.com/zclconf/go-cty/cty/gocty"
)

// instance is one instance of a module: the root module, or one expansion
// of a module call (module.web, module.svc["api"], ...). Values are
// evaluated lazily and cached per instance.
type instance struct {
	cfg       *Config
	mod       *module
	path      string
	key       cty.Value
	call      *moduleCall
	callScope *scope
	cache     map[string]cty.Value
	busy      map[string]bool
	children  map[string]*expansion
}

func newInstance(cfg *Config, mod *module, path string, key cty.Value, callScope *scope) *instance {
	return &instance{
		cfg:       cfg,
		mod:       mod,
		path:      path,
		key:       key,
		callScope: callScope,
		cache:     map[string]cty.Value{},
		busy:      map[string]bool{},
		children:  map[string]*expansion{},
	}
}

func (i *instance) scope() *scope {
	return &scope{inst: i}
}

// memo caches f under key. A reference cycle evaluates to unknown instead
// of recursing forever.
func (i *instance) memo(key string, f func() cty.Value) cty.Value {
	if v, ok := i.cache[key]; ok {
		return v
	}

	if i.busy[key] {
		return cty.DynamicVal
	}

	i.busy[key] = true
	v := f()
	delete(i.busy, key)
	i.cache[key] = v

	return v
}

// variableExpr returns the expression that sets variable name in a module
// call, if any.
func (i *instance) variableExpr(name string) hcl.Expression {
	if i.call == nil {
		return nil
	}

	if a, ok := i.call.block.Body.Attributes[name]; ok {
		return a.Expr
	}

	return nil
}

func (i *instance) variable(name string) cty.Value {
	v, ok := i.mod.variables[name]
	if !ok {
		return cty.DynamicVal
	}

	return i.memo("var."+name, func() cty.Value {
		val, ok := i.rawVariable(v)

		if !ok {
			if v.def == nil {
				return cty.DynamicVal
			}

			d, diags := v.def.Value(nil)
			if diags.HasErrors() {
				return cty.DynamicVal
			}
			val = d
		}

		if v.defaults != nil {
			val = v.defaults.Apply(val)
		}

		if conv, err := convert.Convert(val, v.typ); err == nil {
			val = conv
		}

		return val
	})
}

func (i *instance) rawVariable(v *variable) (cty.Value, bool) {
	if i.call != nil {
		if expr := i.variableExpr(v.name); expr != nil {
			return i.callScope.eval(expr), true
		}
		return cty.NilVal, false
	}

	if raw, ok := i.cfg.cliVars[v.name]; ok {
		if v.typ.IsPrimitiveType() || v.typ == cty.DynamicPseudoType {
			return cty.StringVal(raw), true
		}

		expr, diags := hclsyntax.ParseExpression([]byte(raw), "<var "+v.name+">", hcl.InitialPos)
		if diags.HasErrors() {
			return cty.StringVal(raw), true
		}

		val, diags := expr.Value(nil)
		if diags.HasErrors() {
			return cty.StringVal(raw), true
		}

		return val, true
	}

	if val, ok := i.cfg.inputs[v.name]; ok {
		return val, true
	}

	return cty.NilVal, false
}

func (i *instance) local(name string) cty.Value {
	a, ok := i.mod.locals[name]
	if !ok {
		return cty.DynamicVal
	}

	return i.memo("local."+name, func() cty.Value {
		return i.scope().eval(a.Expr)
	})
}

func (i *instance) output(name string) cty.Value {
	expr, ok := i.mod.outputs[name]
	if !ok {
		return cty.DynamicVal
	}

	return i.memo("output."+name, func() cty.Value {
		return i.scope().eval(expr)
	})
}

func (i *instance) pathObject() cty.Value {
	rel, err := filepath.Rel(i.cfg.rootDir, i.mod.dir)
	if err != nil {
		rel = i.mod.dir
	}

	return cty.ObjectVal(map[string]cty.Value{
		"module": cty.StringVal(filepath.ToSlash(rel)),
		"root":   cty.StringVal("."),
		"cwd":    cty.StringVal(i.cfg.rootDir),
	})
}

func (i *instance) callNames() []string {
	names := make([]string, 0, len(i.mod.calls))
	for name := range i.mod.calls {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// childInstances expands module call name into its instances. It returns
// nil when the call does not exist or its source could not be resolved.
func (i *instance) childInstances(name string) *expansion {
	if e, ok := i.children[name]; ok {
		return e
	}

	call, ok := i.mod.calls[name]
	if !ok || call.mod == nil {
		i.children[name] = nil
		return nil
	}

	e := i.expand(call.block.Body)
	i.children[name] = e

	for _, item := range e.items {
		path := joinPath(i.path, "module."+name) + formatKey(item.key)
		child := newInstance(i.cfg, call.mod, path, item.key, item.scope)
		child.call = call
		item.inst = child
	}

	return e
}

// moduleValue builds the value of module.<name> as seen by expressions in
// this instance, containing the requested outputs (all when outs is nil).
func (i *instance) moduleValue(name string, outs map[string]bool) cty.Value {
	e := i.childInstances(name)
	if e == nil {
		return cty.DynamicVal
	}

	object := func(ci *instance) cty.Value {
		vals := map[string]cty.Value{}
		for n := range ci.mod.outputs {
			if outs == nil || outs[n] {
				vals[n] = ci.output(n)
			}
		}
		return cty.ObjectVal(vals)
	}

	switch e.kind {
	case expandSingle:
		return object(e.items[0].inst)
	case expandCount:
		if len(e.items) == 0 {
			return cty.EmptyTupleVal
		}
		vals := make([]cty.Value, len(e.items))
		for n, item := range e.items {
			vals[n] = object(item.inst)
		}
		return cty.TupleVal(vals)
	case expandForEach:
		vals := map[string]cty.Value{}
		for _, item := range e.items {
			vals[item.key.AsString()] = object(item.inst)
		}
		return cty.ObjectVal(vals)
	default:
		return cty.DynamicVal
	}
}

type expandKind int

const (
	expandSingle expandKind = iota
	expandCount
	expandForEach
	expandUnknown
)

type expansion struct {
	kind  expandKind
	items []*expandItem
}

type expandItem struct {
	key   cty.Value
	scope *scope
	inst  *instance
}

// expand evaluates count / for_each of a resource or module block.
func (i *instance) expand(body *hclsyntax.Body) *expansion {
	base := i.scope()

	unknown := func(s *scope) *expansion {
		return &expansion{kind: expandUnknown, items: []*expandItem{{key: cty.NilVal, scope: s}}}
	}

	if a, ok := body.Attributes["count"]; ok {
		s := &scope{inst: i, count: cty.UnknownVal(cty.Number)}
		v, _ := base.eval(a.Expr).Unmark()

		v, err := convert.Convert(v, cty.Number)
		if err != nil || !v.IsKnown() || v.IsNull() {
			return unknown(s)
		}

		var n int
		if err := gocty.FromCtyValue(v, &n); err != nil {
			return unknown(s)
		}

		e := &expansion{kind: expandCount}
		for idx := range n {
			k := cty.NumberIntVal(int64(idx))
			e.items = append(e.items, &expandItem{key: k, scope: &scope{inst: i, count: k}})
		}

		return e
	}

	if a, ok := body.Attributes["for_each"]; ok {
		s := &scope{inst: i, each: eachObject(cty.UnknownVal(cty.String), cty.DynamicVal)}
		v, _ := base.eval(a.Expr).UnmarkDeep()

		if !v.IsWhollyKnown() || v.IsNull() || !v.CanIterateElements() {
			return unknown(s)
		}

		ty := v.Type()
		if !ty.IsSetType() && !ty.IsMapType() && !ty.IsObjectType() {
			return unknown(s)
		}

		e := &expansion{kind: expandForEach}

		for it := v.ElementIterator(); it.Next(); {
			k, elem := it.Element()
			if ty.IsSetType() {
				k = elem
			}

			k, err := convert.Convert(k, cty.String)
			if err != nil || k.IsNull() {
				return unknown(s)
			}

			e.items = append(e.items, &expandItem{key: k, scope: &scope{inst: i, each: eachObject(k, elem)}})
		}

		return e
	}

	return &expansion{kind: expandSingle, items: []*expandItem{{key: cty.NilVal, scope: base}}}
}

func eachObject(key, value cty.Value) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{"key": key, "value": value})
}
