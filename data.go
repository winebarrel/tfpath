package tfpath

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

// dataEntry is a value given for (part of) a data source from outside,
// e.g. "module.*.data.aws_region.current.name" = "ap-northeast-1".
type dataEntry struct {
	modules []step // module path pattern: module.<name>[<key>] and **
	typ     step
	name    step
	key     *step    // instance key; nil matches every instance
	attrs   []string // attribute path inside the data source
	value   cty.Value
}

// moduleSegment is one module call in the path of a module instance.
type moduleSegment struct {
	name string
	key  cty.Value
}

func parseDataEntry(addr string, v cty.Value) (*dataEntry, error) {
	q, err := ParseQuery(addr)
	if err != nil {
		return nil, err
	}

	e := &dataEntry{value: v}
	steps := q.steps

	for len(steps) > 0 {
		if steps[0].kind == stepDeep {
			e.modules = append(e.modules, steps[0])
			steps = steps[1:]
			continue
		}

		if steps[0].kind != stepAttr || steps[0].name != "module" {
			break
		}

		if len(steps) < 2 || (steps[1].kind != stepAttr && steps[1].kind != stepAttrWild) {
			return nil, fmt.Errorf("invalid data source address %q: expected module name", addr)
		}

		e.modules = append(e.modules, steps[0], steps[1])
		steps = steps[2:]

		if key, rest := splitKey(steps); key != nil {
			e.modules = append(e.modules, *key)
			steps = rest
		}
	}

	if len(steps) < 3 || steps[0].kind != stepAttr || steps[0].name != "data" {
		return nil, fmt.Errorf("invalid data source address %q: expected data.<type>.<name>", addr)
	}

	for _, st := range steps[1:3] {
		if st.kind != stepAttr && st.kind != stepAttrWild {
			return nil, fmt.Errorf("invalid data source address %q: expected data.<type>.<name>", addr)
		}
	}

	e.typ, e.name = steps[1], steps[2]
	e.key, steps = splitKey(steps[3:])

	for _, st := range steps {
		switch st.kind {
		case stepAttr:
			e.attrs = append(e.attrs, st.name)
		case stepIndex:
			if k, ok := st.key.(string); ok {
				e.attrs = append(e.attrs, k)
				continue
			}
			fallthrough
		default:
			return nil, fmt.Errorf("invalid data source address %q: attribute path must not contain wildcards or list indexes", addr)
		}
	}

	return e, nil
}

// wildcards counts the parts of the address that match more than one
// thing. Entries with fewer wildcards take precedence.
func (e *dataEntry) wildcards() int {
	n := 0
	for _, st := range append(slices.Clone(e.modules), e.typ, e.name) {
		switch {
		case st.kind == stepAttrWild || st.kind == stepIndexWild || st.kind == stepDeep:
			n++
		case st.kind == stepAttr && st.name == "module":
			// A module call without a key matches all of its instances.
			n++
		case st.kind == stepIndex:
			n--
		}
	}
	if e.key == nil || e.key.kind == stepIndexWild {
		n++
	}
	return n
}

// sortDataEntries orders entries so that more specific ones are applied
// later and therefore win.
func sortDataEntries(entries []*dataEntry, keys []string) {
	idx := make([]int, len(entries))
	for n := range idx {
		idx[n] = n
	}

	slices.SortStableFunc(idx, func(a, b int) int {
		return cmp.Or(
			cmp.Compare(entries[b].wildcards(), entries[a].wildcards()),
			cmp.Compare(len(entries[a].attrs), len(entries[b].attrs)),
			cmp.Compare(keys[a], keys[b]),
		)
	})

	sorted := make([]*dataEntry, len(entries))
	for n, i := range idx {
		sorted[n] = entries[i]
	}
	copy(entries, sorted)
}

func (c *Config) addData(values map[string]cty.Value) error {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	entries := make([]*dataEntry, len(keys))
	for n, k := range keys {
		e, err := parseDataEntry(k, values[k])
		if err != nil {
			return err
		}
		entries[n] = e
	}

	sortDataEntries(entries, keys)
	c.data = append(c.data, entries...)

	return nil
}

func (c *Config) loadDataFile(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	values := make(map[string]cty.Value, len(raw))
	for k, r := range raw {
		ty, err := ctyjson.ImpliedType(r)
		if err != nil {
			return fmt.Errorf("%s: %s: %w", path, k, err)
		}

		v, err := ctyjson.Unmarshal(r, ty)
		if err != nil {
			return fmt.Errorf("%s: %s: %w", path, k, err)
		}

		values[k] = v
	}

	if err := c.addData(values); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	return nil
}

func matchModules(pattern []step, segs []moduleSegment) bool {
	if len(pattern) == 0 {
		return len(segs) == 0
	}

	if pattern[0].kind == stepDeep {
		for n := range len(segs) + 1 {
			if matchModules(pattern[1:], segs[n:]) {
				return true
			}
		}
		return false
	}

	// module.<name>[<key>]
	if len(segs) == 0 || !nameMatches(pattern[1], segs[0].name) {
		return false
	}

	key, rest := splitKey(pattern[2:])
	if !keyMatches(key, segs[0].key) {
		return false
	}

	return matchModules(rest, segs[1:])
}

// dataValue returns data.<typ>.<name> as seen by expressions in this
// instance. It is unknown unless values were given for it.
func (i *instance) dataValue(typ, name string) cty.Value {
	return i.memo("data."+typ+"."+name, func() cty.Value {
		var entries []*dataEntry
		for _, e := range i.cfg.data {
			if nameMatches(e.typ, typ) && nameMatches(e.name, name) && matchModules(e.modules, i.segments) {
				entries = append(entries, e)
			}
		}

		if len(entries) == 0 {
			return cty.DynamicVal
		}

		idx := slices.IndexFunc(i.mod.resources, func(r *resource) bool {
			return r.data && r.typ == typ && r.name == name
		})
		if idx < 0 {
			return cty.DynamicVal
		}

		object := func(key cty.Value) cty.Value {
			v := cty.NilVal
			for _, e := range entries {
				if keyMatches(e.key, key) {
					v = mergePath(v, e.attrs, e.value)
				}
			}
			if v == cty.NilVal {
				return cty.DynamicVal
			}
			return v
		}

		e := i.expand(i.mod.resources[idx].block.Body)

		switch e.kind {
		case expandSingle:
			return object(cty.NilVal)
		case expandCount:
			if len(e.items) == 0 {
				return cty.EmptyTupleVal
			}
			vals := make([]cty.Value, len(e.items))
			for n, item := range e.items {
				vals[n] = object(item.key)
			}
			return cty.TupleVal(vals)
		case expandForEach:
			vals := map[string]cty.Value{}
			for _, item := range e.items {
				vals[item.key.AsString()] = object(item.key)
			}
			return cty.ObjectVal(vals)
		default:
			return cty.DynamicVal
		}
	})
}

func isKnownObject(v cty.Value) bool {
	return v != cty.NilVal && v.Type().IsObjectType() && v.IsKnown() && !v.IsNull()
}

func objectAttrs(v cty.Value) map[string]cty.Value {
	attrs := map[string]cty.Value{}
	if isKnownObject(v) {
		maps.Copy(attrs, v.AsValueMap())
	}
	return attrs
}

// mergePath sets v at path inside base. Objects are merged recursively.
func mergePath(base cty.Value, path []string, v cty.Value) cty.Value {
	if len(path) == 0 {
		if !isKnownObject(base) || !isKnownObject(v) {
			return v
		}

		attrs := objectAttrs(base)
		for k, a := range v.AsValueMap() {
			if cur, ok := attrs[k]; ok {
				a = mergePath(cur, nil, a)
			}
			attrs[k] = a
		}
		return cty.ObjectVal(attrs)
	}

	attrs := objectAttrs(base)
	cur, ok := attrs[path[0]]
	if !ok {
		cur = cty.NilVal
	}
	attrs[path[0]] = mergePath(cur, path[1:], v)

	return cty.ObjectVal(attrs)
}

// fillTraversal adds the attributes that the traversal refers to but v
// does not have as unknown values, so that a reference to an attribute
// that was not given is unknown rather than an error.
func fillTraversal(v cty.Value, t hcl.Traversal) cty.Value {
	if len(t) == 0 || !v.IsKnown() || v.IsNull() {
		return v
	}

	var name string
	switch s := t[0].(type) {
	case hcl.TraverseAttr:
		name = s.Name
	case hcl.TraverseIndex:
		if !s.Key.IsKnown() || s.Key.IsNull() {
			return v
		}
		if v.Type().IsTupleType() && s.Key.Type() == cty.Number {
			return fillTuple(v, s.Key, t[1:])
		}
		if s.Key.Type() != cty.String {
			return v
		}
		name = s.Key.AsString()
	default:
		return v
	}

	if !v.Type().IsObjectType() {
		return v
	}

	attrs := objectAttrs(v)
	if cur, ok := attrs[name]; ok {
		attrs[name] = fillTraversal(cur, t[1:])
	} else {
		attrs[name] = cty.DynamicVal
	}

	return cty.ObjectVal(attrs)
}

func fillTuple(v cty.Value, key cty.Value, rest hcl.Traversal) cty.Value {
	vals := v.AsValueSlice()
	for n := range vals {
		if key.Equals(cty.NumberIntVal(int64(n))).True() {
			vals[n] = fillTraversal(vals[n], rest)
		}
	}
	return cty.TupleVal(vals)
}
