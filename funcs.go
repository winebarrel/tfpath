package tfpath

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2/ext/tryfunc"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"
)

// functions returns the subset of Terraform's built-in functions that can
// be evaluated without providers. Calls to other functions are left as
// source text.
func functions(rootDir string) map[string]function.Function {
	resolve := func(path string) string {
		if filepath.IsAbs(path) {
			return path
		}
		return filepath.Join(rootDir, path)
	}

	return map[string]function.Function{
		"abs":             stdlib.AbsoluteFunc,
		"base64decode":    stringFunc(base64Decode),
		"base64encode":    stringFunc(func(s string) (string, error) { return base64.StdEncoding.EncodeToString([]byte(s)), nil }),
		"can":             tryfunc.CanFunc,
		"ceil":            stdlib.CeilFunc,
		"chomp":           stdlib.ChompFunc,
		"chunklist":       stdlib.ChunklistFunc,
		"coalesce":        stdlib.CoalesceFunc,
		"coalescelist":    stdlib.CoalesceListFunc,
		"compact":         stdlib.CompactFunc,
		"concat":          stdlib.ConcatFunc,
		"contains":        stdlib.ContainsFunc,
		"csvdecode":       stdlib.CSVDecodeFunc,
		"distinct":        stdlib.DistinctFunc,
		"element":         stdlib.ElementFunc,
		"endswith":        stringPredicate(strings.HasSuffix),
		"file":            stringFunc(func(p string) (string, error) { b, err := os.ReadFile(resolve(p)); return string(b), err }),
		"fileexists":      fileExistsFunc(resolve),
		"flatten":         stdlib.FlattenFunc,
		"floor":           stdlib.FloorFunc,
		"format":          stdlib.FormatFunc,
		"formatdate":      stdlib.FormatDateFunc,
		"formatlist":      stdlib.FormatListFunc,
		"indent":          stdlib.IndentFunc,
		"join":            stdlib.JoinFunc,
		"jsondecode":      stdlib.JSONDecodeFunc,
		"jsonencode":      stdlib.JSONEncodeFunc,
		"keys":            stdlib.KeysFunc,
		"length":          stdlib.LengthFunc,
		"log":             stdlib.LogFunc,
		"lookup":          stdlib.LookupFunc,
		"lower":           stdlib.LowerFunc,
		"max":             stdlib.MaxFunc,
		"md5":             stringFunc(hashFunc(func(b []byte) []byte { h := md5.Sum(b); return h[:] })),
		"merge":           stdlib.MergeFunc,
		"min":             stdlib.MinFunc,
		"parseint":        stdlib.ParseIntFunc,
		"pow":             stdlib.PowFunc,
		"range":           stdlib.RangeFunc,
		"regex":           stdlib.RegexFunc,
		"regexall":        stdlib.RegexAllFunc,
		"replace":         replaceFunc,
		"reverse":         stdlib.ReverseListFunc,
		"setintersection": stdlib.SetIntersectionFunc,
		"setproduct":      stdlib.SetProductFunc,
		"setsubtract":     stdlib.SetSubtractFunc,
		"setunion":        stdlib.SetUnionFunc,
		"sha1":            stringFunc(hashFunc(func(b []byte) []byte { h := sha1.Sum(b); return h[:] })),
		"sha256":          stringFunc(hashFunc(func(b []byte) []byte { h := sha256.Sum256(b); return h[:] })),
		"signum":          stdlib.SignumFunc,
		"slice":           stdlib.SliceFunc,
		"sort":            stdlib.SortFunc,
		"split":           stdlib.SplitFunc,
		"startswith":      stringPredicate(strings.HasPrefix),
		"strcontains":     stringPredicate(strings.Contains),
		"strrev":          stdlib.ReverseFunc,
		"substr":          stdlib.SubstrFunc,
		"timeadd":         stdlib.TimeAddFunc,
		"title":           stdlib.TitleFunc,
		"tobool":          stdlib.MakeToFunc(cty.Bool),
		"tolist":          stdlib.MakeToFunc(cty.List(cty.DynamicPseudoType)),
		"tomap":           stdlib.MakeToFunc(cty.Map(cty.DynamicPseudoType)),
		"tonumber":        stdlib.MakeToFunc(cty.Number),
		"toset":           stdlib.MakeToFunc(cty.Set(cty.DynamicPseudoType)),
		"tostring":        stdlib.MakeToFunc(cty.String),
		"trim":            stdlib.TrimFunc,
		"trimprefix":      stdlib.TrimPrefixFunc,
		"trimspace":       stdlib.TrimSpaceFunc,
		"trimsuffix":      stdlib.TrimSuffixFunc,
		"try":             tryfunc.TryFunc,
		"upper":           stdlib.UpperFunc,
		"values":          stdlib.ValuesFunc,
		"zipmap":          stdlib.ZipmapFunc,
	}
}

func stringFunc(f func(string) (string, error)) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "str", Type: cty.String}},
		Type:   function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			s, err := f(args[0].AsString())
			if err != nil {
				return cty.UnknownVal(cty.String), err
			}
			return cty.StringVal(s), nil
		},
	})
}

func stringPredicate(f func(string, string) bool) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "str", Type: cty.String}, {Name: "substr", Type: cty.String}},
		Type:   function.StaticReturnType(cty.Bool),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			return cty.BoolVal(f(args[0].AsString(), args[1].AsString())), nil
		},
	})
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

func base64Decode(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	return string(b), err
}

func hashFunc(sum func([]byte) []byte) func(string) (string, error) {
	return func(s string) (string, error) {
		return hex.EncodeToString(sum([]byte(s))), nil
	}
}

// replaceFunc is Terraform's replace: a substring wrapped in slashes is a
// regular expression.
var replaceFunc = function.New(&function.Spec{
	Params: []function.Parameter{
		{Name: "str", Type: cty.String},
		{Name: "substr", Type: cty.String},
		{Name: "replace", Type: cty.String},
	},
	Type: function.StaticReturnType(cty.String),
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		substr := args[1].AsString()
		if len(substr) > 1 && strings.HasPrefix(substr, "/") && strings.HasSuffix(substr, "/") {
			return stdlib.RegexReplace(args[0], cty.StringVal(substr[1:len(substr)-1]), args[2])
		}
		return stdlib.Replace(args[0], args[1], args[2])
	},
})
