package tfpath

import (
	"errors"
	"fmt"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"
)

var allTrueFunc = function.New(&function.Spec{
	Params: []function.Parameter{{Name: "list", Type: cty.List(cty.Bool)}},
	Type:   function.StaticReturnType(cty.Bool),
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		for it := args[0].ElementIterator(); it.Next(); {
			_, v := it.Element()
			if !v.IsKnown() {
				return cty.UnknownVal(cty.Bool), nil
			}
			if v.IsNull() || v.False() {
				return cty.False, nil
			}
		}
		return cty.True, nil
	},
})

var anyTrueFunc = function.New(&function.Spec{
	Params: []function.Parameter{{Name: "list", Type: cty.List(cty.Bool)}},
	Type:   function.StaticReturnType(cty.Bool),
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		result := cty.False
		for it := args[0].ElementIterator(); it.Next(); {
			_, v := it.Element()
			if !v.IsKnown() {
				result = cty.UnknownVal(cty.Bool)
				continue
			}
			if !v.IsNull() && v.True() {
				return cty.True, nil
			}
		}
		return result, nil
	},
})

// indexFunc is Terraform's index: the position of value in list.
var indexFunc = function.New(&function.Spec{
	Params: []function.Parameter{
		{Name: "list", Type: cty.DynamicPseudoType},
		{Name: "value", Type: cty.DynamicPseudoType},
	},
	Type: function.StaticReturnType(cty.Number),
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		ty := args[0].Type()
		if !ty.IsListType() && !ty.IsTupleType() {
			return cty.UnknownVal(cty.Number), errors.New("argument must be a list or tuple")
		}

		if args[0].LengthInt() == 0 {
			return cty.UnknownVal(cty.Number), errors.New("cannot search an empty list")
		}

		for it := args[0].ElementIterator(); it.Next(); {
			i, v := it.Element()
			eq, err := stdlib.Equal(v, args[1])
			if err != nil {
				return cty.UnknownVal(cty.Number), err
			}
			if !eq.IsKnown() {
				return cty.UnknownVal(cty.Number), nil
			}
			if eq.True() {
				return i, nil
			}
		}

		return cty.UnknownVal(cty.Number), errors.New("item not found")
	},
})

// matchKeysFunc is Terraform's matchkeys: the elements of values whose
// corresponding element of keys is in searchset.
var matchKeysFunc = function.New(&function.Spec{
	Params: []function.Parameter{
		{Name: "values", Type: cty.List(cty.DynamicPseudoType)},
		{Name: "keys", Type: cty.List(cty.DynamicPseudoType)},
		{Name: "searchset", Type: cty.List(cty.DynamicPseudoType)},
	},
	Type: func(args []cty.Value) (cty.Type, error) {
		if ty, _ := convert.UnifyUnsafe([]cty.Type{args[1].Type(), args[2].Type()}); ty == cty.NilType {
			return cty.NilType, errors.New("keys and searchset must be of the same type")
		}
		return args[0].Type(), nil
	},
	Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
		values := args[0]
		if values.LengthInt() != args[1].LengthInt() {
			return cty.UnknownVal(retType), errors.New("length of keys and values should be equal")
		}

		ty, _ := convert.UnifyUnsafe([]cty.Type{args[1].Type(), args[2].Type()})
		keys, err := convert.Convert(args[1], ty)
		if err != nil {
			return cty.UnknownVal(retType), err
		}
		searchset, err := convert.Convert(args[2], ty)
		if err != nil {
			return cty.UnknownVal(retType), err
		}

		if searchset.LengthInt() == 0 {
			return cty.ListValEmpty(retType.ElementType()), nil
		}

		if !values.IsWhollyKnown() || !keys.IsWhollyKnown() {
			return cty.UnknownVal(retType), nil
		}

		var out []cty.Value
		for it := keys.ElementIterator(); it.Next(); {
			i, key := it.Element()
			for sit := searchset.ElementIterator(); sit.Next(); {
				_, search := sit.Element()
				eq, err := stdlib.Equal(key, search)
				if err != nil {
					return cty.UnknownVal(retType), err
				}
				if !eq.IsKnown() {
					return cty.UnknownVal(retType), nil
				}
				if eq.True() {
					out = append(out, values.Index(i))
					break
				}
			}
		}

		if len(out) == 0 {
			return cty.ListValEmpty(retType.ElementType()), nil
		}

		return cty.ListVal(out), nil
	},
})

// oneFunc is Terraform's one: the only element of a collection, or null
// when it is empty.
var oneFunc = function.New(&function.Spec{
	Params: []function.Parameter{{Name: "list", Type: cty.DynamicPseudoType}},
	Type: func(args []cty.Value) (cty.Type, error) {
		ty := args[0].Type()
		switch {
		case ty.IsListType() || ty.IsSetType():
			return ty.ElementType(), nil
		case ty.IsTupleType():
			switch etys := ty.TupleElementTypes(); len(etys) {
			case 0:
				return cty.DynamicPseudoType, nil
			case 1:
				return etys[0], nil
			}
		}
		return cty.NilType, errors.New("must be a list, set, or tuple value with either zero or one elements")
	},
	Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
		v := args[0]
		if !v.Length().IsKnown() {
			return cty.UnknownVal(retType), nil
		}

		switch v.LengthInt() {
		case 0:
			return cty.NullVal(retType), nil
		case 1:
			it := v.ElementIterator()
			it.Next()
			_, elem := it.Element()
			return elem, nil
		}

		return cty.UnknownVal(retType), errors.New("must be a list, set, or tuple value with either zero or one elements")
	},
})

var sumFunc = function.New(&function.Spec{
	Params: []function.Parameter{{Name: "list", Type: cty.DynamicPseudoType}},
	Type:   function.StaticReturnType(cty.Number),
	Impl: func(args []cty.Value, _ cty.Type) (ret cty.Value, err error) {
		ty := args[0].Type()
		if !ty.IsListType() && !ty.IsSetType() && !ty.IsTupleType() {
			return cty.UnknownVal(cty.Number), fmt.Errorf("argument must be list, set, or tuple. Received %s", ty.FriendlyName())
		}

		if args[0].LengthInt() == 0 {
			return cty.UnknownVal(cty.Number), errors.New("cannot sum an empty list")
		}

		if !args[0].IsWhollyKnown() {
			return cty.UnknownVal(cty.Number), nil
		}

		// big.Float.Add panics on opposing infinities.
		defer func() {
			if r := recover(); r != nil {
				ret, err = cty.UnknownVal(cty.Number), errors.New("can't compute sum of opposing infinities")
			}
		}()

		sum := cty.Zero
		for it := args[0].ElementIterator(); it.Next(); {
			_, v := it.Element()
			if v.IsNull() {
				return cty.UnknownVal(cty.Number), errors.New("argument must be list, set, or tuple of number values")
			}

			n, err := convert.Convert(v, cty.Number)
			if err != nil {
				return cty.UnknownVal(cty.Number), err
			}

			sum = sum.Add(n)
		}

		return sum, nil
	},
})

// transposeFunc is Terraform's transpose: swaps the keys and the values
// of a map of lists of strings.
var transposeFunc = function.New(&function.Spec{
	Params: []function.Parameter{{Name: "values", Type: cty.Map(cty.List(cty.String))}},
	Type:   function.StaticReturnType(cty.Map(cty.List(cty.String))),
	Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
		if !args[0].IsWhollyKnown() {
			return cty.UnknownVal(retType), nil
		}

		tmp := map[string][]cty.Value{}
		for it := args[0].ElementIterator(); it.Next(); {
			k, list := it.Element()
			if list.IsNull() {
				return cty.UnknownVal(retType), errors.New("input must be a map of lists of strings")
			}

			for lit := list.ElementIterator(); lit.Next(); {
				_, v := lit.Element()
				if v.IsNull() {
					return cty.UnknownVal(retType), errors.New("input must be a map of lists of strings")
				}
				tmp[v.AsString()] = append(tmp[v.AsString()], k)
			}
		}

		if len(tmp) == 0 {
			return cty.MapValEmpty(cty.List(cty.String)), nil
		}

		out := make(map[string]cty.Value, len(tmp))
		for k, vals := range tmp {
			out[k] = cty.ListVal(vals)
		}

		return cty.MapVal(out), nil
	},
})
