package tfpath

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

// fileText is Terraform's file: the contents must be valid UTF-8.
func fileText(readFile func(string) ([]byte, error)) func(string) (string, error) {
	return func(path string) (string, error) {
		b, err := readFile(path)
		if err != nil {
			return "", err
		}

		if !utf8.Valid(b) {
			return "", fmt.Errorf("contents of %s are not valid UTF-8; use the filebase64 function instead", path)
		}

		return string(b), nil
	}
}

// fileFunc reads a file and converts its contents with conv.
func fileFunc(readFile func(string) ([]byte, error), conv func([]byte) string) func(string) (string, error) {
	return func(path string) (string, error) {
		b, err := readFile(path)
		if err != nil {
			return "", err
		}
		return conv(b), nil
	}
}

func fileExistsFunc(resolve func(string) string) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "path", Type: cty.String}},
		Type:   function.StaticReturnType(cty.Bool),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			fi, err := os.Stat(resolve(args[0].AsString()))
			return cty.BoolVal(err == nil && fi.Mode().IsRegular()), nil
		},
	})
}

// fileSetFunc is Terraform's fileset: the regular files under path
// matching pattern, relative to path and separated by slashes.
func fileSetFunc(resolve func(string) string) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{
			{Name: "path", Type: cty.String},
			{Name: "pattern", Type: cty.String},
		},
		Type: function.StaticReturnType(cty.Set(cty.String)),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			dir := resolve(args[0].AsString())
			pattern := filepath.Clean(filepath.Join(dir, args[1].AsString()))

			matches, err := doublestar.FilepathGlob(pattern, doublestar.WithFilesOnly())
			if err != nil {
				return cty.UnknownVal(cty.Set(cty.String)), fmt.Errorf("failed to glob pattern %q: %w", pattern, err)
			}

			vals := make([]cty.Value, 0, len(matches))
			for _, m := range matches {
				rel, err := filepath.Rel(dir, m)
				if err != nil {
					return cty.UnknownVal(cty.Set(cty.String)), err
				}
				vals = append(vals, cty.StringVal(filepath.ToSlash(rel)))
			}

			if len(vals) == 0 {
				return cty.SetValEmpty(cty.String), nil
			}

			return cty.SetVal(vals), nil
		},
	})
}

func base64Decode(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}

	if !utf8.Valid(b) {
		return "", fmt.Errorf("the result of decoding %q is not valid UTF-8", s)
	}

	return string(b), nil
}

func base64Gzip(s string) (string, error) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)

	if _, err := w.Write([]byte(s)); err != nil {
		return "", err
	}

	if err := w.Close(); err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

func parseTemplate(src []byte, filename string) (hcl.Expression, error) {
	expr, diags := hclsyntax.ParseTemplate(src, filename, hcl.InitialPos)
	if diags.HasErrors() {
		return nil, diags
	}
	return expr, nil
}

// templateFunc is Terraform's templatefile / templatestring: load turns
// the first argument into a template, which is rendered with the second
// argument as its variables.
func templateFunc(param string, funcs map[string]function.Function, load func(string) (hcl.Expression, error)) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{
			{Name: param, Type: cty.String},
			{Name: "vars", Type: cty.DynamicPseudoType},
		},
		Type: function.StaticReturnType(cty.DynamicPseudoType),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			expr, err := load(args[0].AsString())
			if err != nil {
				return cty.DynamicVal, err
			}
			return renderTemplate(expr, args[1], funcs)
		},
	})
}

func renderTemplate(expr hcl.Expression, vars cty.Value, funcs map[string]function.Function) (cty.Value, error) {
	ty := vars.Type()
	if !ty.IsObjectType() && !ty.IsMapType() {
		return cty.DynamicVal, function.NewArgErrorf(1, "invalid vars value: must be a map")
	}

	ctx := &hcl.EvalContext{Variables: map[string]cty.Value{}, Functions: funcs}

	for it := vars.ElementIterator(); it.Next(); {
		k, v := it.Element()
		name := k.AsString()
		if !hclsyntax.ValidIdentifier(name) {
			return cty.DynamicVal, function.NewArgErrorf(1, "invalid template variable name %q", name)
		}
		ctx.Variables[name] = v
	}

	for _, t := range expr.Variables() {
		if _, ok := ctx.Variables[t.RootName()]; !ok {
			return cty.DynamicVal, function.NewArgErrorf(1, "vars map does not contain key %q", t.RootName())
		}
	}

	v, diags := expr.Value(ctx)
	if diags.HasErrors() {
		return cty.DynamicVal, diags
	}

	return v, nil
}

var recursiveTemplateFunc = function.New(&function.Spec{
	VarParam: &function.Parameter{Name: "args", Type: cty.DynamicPseudoType, AllowUnknown: true, AllowNull: true, AllowDynamicType: true},
	Type:     function.StaticReturnType(cty.DynamicPseudoType),
	Impl: func([]cty.Value, cty.Type) (cty.Value, error) {
		return cty.DynamicVal, errors.New("cannot recursively call templatefile or templatestring from inside a template")
	},
})
