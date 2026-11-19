package tfpath

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

// scope is where an expression is evaluated: a module instance plus the
// count / each / dynamic-block iterator values in effect.
type scope struct {
	inst  *instance
	count cty.Value
	each  cty.Value
	extra map[string]cty.Value
}

func (s *scope) with(name string, v cty.Value) *scope {
	ns := *s
	ns.extra = maps.Clone(s.extra)
	if ns.extra == nil {
		ns.extra = map[string]cty.Value{}
	}
	ns.extra[name] = v
	return &ns
}

// evalContext builds an EvalContext holding only what expr references.
// References that cannot be known from the configuration alone
// (resources, data sources, ...) are unknown.
func (s *scope) evalContext(expr hcl.Expression) *hcl.EvalContext {
	i := s.inst
	vars := map[string]cty.Value{}
	locals := map[string]bool{}
	allLocals := false
	outputs := map[string]map[string]bool{}
	allOutputs := map[string]bool{}
	data := map[string]map[string]cty.Value{}
	allData := false

	for _, t := range expr.Variables() {
		root := t.RootName()

		if v, ok := s.extra[root]; ok {
			vars[root] = v
			continue
		}

		switch root {
		case "var":
			if _, ok := vars["var"]; !ok {
				vals := map[string]cty.Value{}
				for name := range i.mod.variables {
					vals[name] = i.variable(name)
				}
				vars["var"] = cty.ObjectVal(vals)
			}
		case "local":
			if name, ok := attrAt(t, 1); ok {
				locals[name] = true
			} else {
				allLocals = true
			}
		case "module":
			name, ok := attrAt(t, 1)
			if !ok {
				vars["module"] = cty.DynamicVal
				continue
			}

			if outputs[name] == nil {
				outputs[name] = map[string]bool{}
			}

			// module.X.out or module.X[key].out
			out, ok := attrAt(t, 2)
			if len(t) > 2 {
				if _, isIndex := t[2].(hcl.TraverseIndex); isIndex {
					out, ok = attrAt(t, 3)
				}
			}

			if ok {
				outputs[name][out] = true
			} else {
				allOutputs[name] = true
			}
		case "data":
			typ, ok1 := attrAt(t, 1)
			name, ok2 := attrAt(t, 2)
			if !ok1 || !ok2 {
				allData = true
				continue
			}

			if data[typ] == nil {
				data[typ] = map[string]cty.Value{}
			}

			v, ok := data[typ][name]
			if !ok {
				v = i.dataValue(typ, name)
			}
			data[typ][name] = fillTraversal(v, t[3:])
		case "path":
			vars["path"] = i.pathObject()
		case "terraform":
			vars["terraform"] = cty.ObjectVal(map[string]cty.Value{
				"workspace": cty.StringVal(i.cfg.workspace),
			})
		case "count":
			if s.count != cty.NilVal {
				vars["count"] = cty.ObjectVal(map[string]cty.Value{"index": s.count})
			} else {
				vars["count"] = cty.DynamicVal
			}
		case "each":
			if s.each != cty.NilVal {
				vars["each"] = s.each
			} else {
				vars["each"] = cty.DynamicVal
			}
		default:
			vars[root] = cty.DynamicVal
		}
	}

	if allLocals || len(locals) > 0 {
		vals := map[string]cty.Value{}
		for name := range i.mod.locals {
			if allLocals || locals[name] {
				vals[name] = i.local(name)
			}
		}
		vars["local"] = cty.ObjectVal(vals)
	}

	if _, ok := vars["module"]; !ok && len(outputs) > 0 {
		vals := map[string]cty.Value{}
		for name, outs := range outputs {
			if allOutputs[name] {
				outs = nil
			}
			vals[name] = i.moduleValue(name, outs)
		}
		vars["module"] = cty.ObjectVal(vals)
	}

	if allData {
		vars["data"] = cty.DynamicVal
	} else if len(data) > 0 {
		types := map[string]cty.Value{}
		for typ, names := range data {
			types[typ] = cty.ObjectVal(names)
		}
		vars["data"] = cty.ObjectVal(types)
	}

	return &hcl.EvalContext{Variables: vars, Functions: i.cfg.funcs}
}

func attrAt(t hcl.Traversal, n int) (string, bool) {
	if n >= len(t) {
		return "", false
	}

	switch step := t[n].(type) {
	case hcl.TraverseAttr:
		return step.Name, true
	case hcl.TraverseRoot:
		return step.Name, true
	}

	return "", false
}

// eval evaluates expr, returning unknown on error.
func (s *scope) eval(expr hcl.Expression) cty.Value {
	v, diags := expr.Value(s.evalContext(expr))
	if diags.HasErrors() {
		return cty.DynamicVal
	}
	return v
}

// render converts expr to a JSON-compatible value. Parts that cannot be
// evaluated are replaced by their source text, e.g. "${aws_iam_role.x.arn}".
func (s *scope) render(expr hcl.Expression) any {
	switch e := expr.(type) {
	case *hclsyntax.ObjectConsExpr:
		m := map[string]any{}
		for _, item := range e.Items {
			key := s.source(item.KeyExpr)
			k, diags := item.KeyExpr.Value(s.evalContext(item.KeyExpr))
			if !diags.HasErrors() {
				k, _ = k.Unmark()
				if ks, err := convert.Convert(k, cty.String); err == nil && ks.IsKnown() && !ks.IsNull() {
					key = ks.AsString()
				}
			}
			m[key] = s.render(item.ValueExpr)
		}
		return m
	case *hclsyntax.TupleConsExpr:
		list := make([]any, len(e.Exprs))
		for n, ex := range e.Exprs {
			list[n] = s.render(ex)
		}
		return list
	}

	v, diags := expr.Value(s.evalContext(expr))
	if !diags.HasErrors() && v.IsWhollyKnown() {
		if a, err := toAny(v); err == nil {
			return a
		}
	}

	return s.source(expr)
}

// renderBody converts a block body to a map. Nested blocks become lists
// keyed by block type, and dynamic blocks are expanded when possible.
func (s *scope) renderBody(body *hclsyntax.Body, skipMeta bool) map[string]any {
	m := map[string]any{}

	for name, a := range body.Attributes {
		if skipMeta && (name == "count" || name == "for_each") {
			continue
		}
		m[name] = s.render(a.Expr)
	}

	for _, b := range body.Blocks {
		if b.Type == "dynamic" && len(b.Labels) == 1 {
			s.renderDynamic(b, m)
			continue
		}

		var item any = s.renderBody(b.Body, false)
		for _, v := range slices.Backward(b.Labels) {
			item = map[string]any{v: item}
		}
		appendBlock(m, b.Type, item)
	}

	return m
}

func (s *scope) renderDynamic(b *hclsyntax.Block, m map[string]any) {
	name := b.Labels[0]
	iter := name

	if a, ok := b.Body.Attributes["iterator"]; ok {
		if kw := hcl.ExprAsKeyword(a.Expr); kw != "" {
			iter = kw
		}
	}

	var content *hclsyntax.Body
	for _, cb := range b.Body.Blocks {
		if cb.Type == "content" {
			content = cb.Body
		}
	}

	if content == nil {
		return
	}

	v := cty.DynamicVal
	if a, ok := b.Body.Attributes["for_each"]; ok {
		v, _ = s.eval(a.Expr).UnmarkDeep()
	}

	if !v.IsWhollyKnown() || v.IsNull() || !v.CanIterateElements() {
		is := s.with(iter, eachObject(cty.DynamicVal, cty.DynamicVal))
		appendBlock(m, name, is.renderBody(content, false))
		return
	}

	isSet := v.Type().IsSetType()

	for it := v.ElementIterator(); it.Next(); {
		k, elem := it.Element()
		if isSet {
			k = elem
		}
		is := s.with(iter, eachObject(k, elem))
		appendBlock(m, name, is.renderBody(content, false))
	}
}

func appendBlock(m map[string]any, typ string, item any) {
	list, _ := m[typ].([]any)
	m[typ] = append(list, item)
}

// source returns the source text of expr in template form.
func (s *scope) source(expr hcl.Expression) string {
	rng := expr.Range()
	src, ok := s.inst.cfg.sources[rng.Filename]
	if !ok || rng.End.Byte > len(src) {
		return "${?}"
	}

	text := string(rng.SliceBytes(src))

	if k, ok := expr.(*hclsyntax.ObjectConsKeyExpr); ok && hcl.ExprAsKeyword(k.Wrapped) != "" {
		return text
	}

	if len(text) >= 2 && text[0] == '"' && text[len(text)-1] == '"' {
		return text[1 : len(text)-1]
	}

	if len(text) >= 2 && text[:2] == "<<" {
		return text
	}

	return "${" + text + "}"
}

// toAny converts a wholly known value to plain Go values as decoded by
// encoding/json (numbers are json.Number).
func toAny(v cty.Value) (any, error) {
	v, _ = v.UnmarkDeep()
	if v.IsNull() {
		return nil, nil
	}

	b, err := ctyjson.Marshal(v, v.Type())
	if err != nil {
		return nil, err
	}

	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()

	var a any
	if err := dec.Decode(&a); err != nil {
		return nil, err
	}

	return a, nil
}
