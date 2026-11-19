package tfpath

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

// Marks Terraform puts on values of variables declared with
// sensitive = true / ephemeral = true.
const (
	markSensitive = "sensitive"
	markEphemeral = "ephemeral"
)

var sensitiveFunc = function.New(&function.Spec{
	Params: []function.Parameter{{Name: "value", Type: cty.DynamicPseudoType, AllowUnknown: true, AllowNull: true, AllowMarked: true, AllowDynamicType: true}},
	Type:   func(args []cty.Value) (cty.Type, error) { return args[0].Type(), nil },
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		return args[0].Mark(markSensitive), nil
	},
})

var nonSensitiveFunc = function.New(&function.Spec{
	Params: []function.Parameter{{Name: "value", Type: cty.DynamicPseudoType, AllowUnknown: true, AllowNull: true, AllowMarked: true, AllowDynamicType: true}},
	Type:   func(args []cty.Value) (cty.Type, error) { return args[0].Type(), nil },
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		v, m := args[0].Unmark()
		delete(m, markSensitive)
		return v.WithMarks(m), nil
	},
})

var isSensitiveFunc = function.New(&function.Spec{
	Params: []function.Parameter{{Name: "value", Type: cty.DynamicPseudoType, AllowUnknown: true, AllowNull: true, AllowMarked: true, AllowDynamicType: true}},
	Type:   function.StaticReturnType(cty.Bool),
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		switch v := args[0]; {
		case v.HasMark(markSensitive):
			return cty.True, nil
		case !v.IsKnown():
			return cty.UnknownVal(cty.Bool), nil
		}
		return cty.False, nil
	},
})

// ephemeralAsNullFunc replaces every ephemeral value nested in its
// argument with null.
var ephemeralAsNullFunc = function.New(&function.Spec{
	Params: []function.Parameter{{Name: "value", Type: cty.DynamicPseudoType, AllowUnknown: true, AllowNull: true, AllowMarked: true, AllowDynamicType: true}},
	Type:   func(args []cty.Value) (cty.Type, error) { return args[0].Type(), nil },
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		return cty.Transform(args[0], func(_ cty.Path, v cty.Value) (cty.Value, error) {
			if !v.HasMark(markEphemeral) {
				return v, nil
			}
			v, m := v.Unmark()
			delete(m, markEphemeral)
			return cty.NullVal(v.Type()).WithMarks(m), nil
		})
	},
})

// The functions of the built-in "terraform" provider.

var encodeTfvarsFunc = function.New(&function.Spec{
	Params: []function.Parameter{{Name: "value", Type: cty.DynamicPseudoType, AllowNull: true, AllowUnknown: true, AllowDynamicType: true}},
	Type:   function.StaticReturnType(cty.String),
	Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
		v := args[0]
		if v.IsNull() {
			return cty.UnknownVal(retType), function.NewArgErrorf(0, "cannot encode a null value in tfvars syntax")
		}

		if !v.IsWhollyKnown() {
			return cty.UnknownVal(retType), nil
		}

		if ty := v.Type(); !ty.IsObjectType() && !ty.IsMapType() {
			return cty.UnknownVal(retType), function.NewArgErrorf(0, "value must be an object or a map to be encoded")
		}

		f := hclwrite.NewEmptyFile()
		body := f.Body()

		for it := v.ElementIterator(); it.Next(); {
			k, elem := it.Element()
			name := k.AsString()
			if !hclsyntax.ValidIdentifier(name) {
				return cty.UnknownVal(retType), function.NewArgErrorf(0, "invalid variable name %q: must be a valid identifier", name)
			}
			body.SetAttributeValue(name, elem)
		}

		return cty.StringVal(string(f.Bytes())), nil
	},
})

var decodeTfvarsFunc = function.New(&function.Spec{
	Params: []function.Parameter{{Name: "src", Type: cty.String}},
	Type:   function.StaticReturnType(cty.DynamicPseudoType),
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		f, diags := hclsyntax.ParseConfig([]byte(args[0].AsString()), "<decode_tfvars argument>", hcl.InitialPos)
		if diags.HasErrors() {
			return cty.DynamicVal, fmt.Errorf("invalid tfvars syntax: %s", diags.Error())
		}

		attrs, diags := f.Body.JustAttributes()
		if diags.HasErrors() {
			return cty.DynamicVal, fmt.Errorf("invalid tfvars content: %s", diags.Error())
		}

		vals := make(map[string]cty.Value, len(attrs))
		for name, attr := range attrs {
			// Like .tfvars files, no functions or references are allowed.
			v, diags := attr.Expr.Value(nil)
			if diags.HasErrors() {
				return cty.DynamicVal, fmt.Errorf("invalid expression for variable %q: %s", name, diags.Error())
			}
			vals[name] = v
		}

		return cty.ObjectVal(vals), nil
	},
})

var encodeExprFunc = function.New(&function.Spec{
	Params: []function.Parameter{{Name: "value", Type: cty.DynamicPseudoType, AllowNull: true, AllowUnknown: true, AllowDynamicType: true}},
	Type:   function.StaticReturnType(cty.String),
	Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
		if !args[0].IsWhollyKnown() {
			return cty.UnknownVal(retType), nil
		}
		return cty.StringVal(string(hclwrite.TokensForValue(args[0]).Bytes())), nil
	},
})
