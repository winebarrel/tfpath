package tfpath

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/ext/tryfunc"
	ctyyaml "github.com/zclconf/go-cty-yaml"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"
)

// functions returns Terraform's built-in functions. Functions whose result
// changes on every run (bcrypt, plantimestamp, timestamp and uuid) are not
// included, so calls to them are left as source text, as are calls to
// provider-defined functions.
func functions(rootDir string) map[string]function.Function {
	resolve := func(path string) string {
		path = expandHome(path)
		if filepath.IsAbs(path) {
			return path
		}
		return filepath.Join(rootDir, path)
	}

	readFile := func(path string) ([]byte, error) {
		return os.ReadFile(resolve(path))
	}

	funcs := map[string]function.Function{
		"abs":              stdlib.AbsoluteFunc,
		"abspath":          stringFunc(func(p string) (string, error) { return filepath.ToSlash(resolve(p)), nil }),
		"alltrue":          allTrueFunc,
		"anytrue":          anyTrueFunc,
		"base64decode":     stringFunc(base64Decode),
		"base64encode":     stringFunc(func(s string) (string, error) { return base64.StdEncoding.EncodeToString([]byte(s)), nil }),
		"base64gzip":       stringFunc(base64Gzip),
		"base64sha256":     stringFunc(base64HashFunc(sha256Sum)),
		"base64sha512":     stringFunc(base64HashFunc(sha512Sum)),
		"basename":         stringFunc(func(p string) (string, error) { return filepath.Base(p), nil }),
		"can":              tryfunc.CanFunc,
		"ceil":             stdlib.CeilFunc,
		"chomp":            stdlib.ChompFunc,
		"chunklist":        stdlib.ChunklistFunc,
		"cidrhost":         cidrHostFunc,
		"cidrnetmask":      cidrNetmaskFunc,
		"cidrsubnet":       cidrSubnetFunc,
		"cidrsubnets":      cidrSubnetsFunc,
		"coalesce":         coalesceFunc,
		"coalescelist":     stdlib.CoalesceListFunc,
		"compact":          stdlib.CompactFunc,
		"concat":           stdlib.ConcatFunc,
		"contains":         stdlib.ContainsFunc,
		"csvdecode":        stdlib.CSVDecodeFunc,
		"dirname":          stringFunc(func(p string) (string, error) { return filepath.Dir(p), nil }),
		"distinct":         stdlib.DistinctFunc,
		"element":          stdlib.ElementFunc,
		"endswith":         stringPredicate(strings.HasSuffix),
		"ephemeralasnull":  ephemeralAsNullFunc,
		"file":             stringFunc(fileText(readFile)),
		"filebase64":       stringFunc(fileFunc(readFile, base64.StdEncoding.EncodeToString)),
		"filebase64sha256": stringFunc(fileFunc(readFile, base64Hash(sha256Sum))),
		"filebase64sha512": stringFunc(fileFunc(readFile, base64Hash(sha512Sum))),
		"fileexists":       fileExistsFunc(resolve),
		"filemd5":          stringFunc(fileFunc(readFile, hexHash(md5Sum))),
		"fileset":          fileSetFunc(resolve),
		"filesha1":         stringFunc(fileFunc(readFile, hexHash(sha1Sum))),
		"filesha256":       stringFunc(fileFunc(readFile, hexHash(sha256Sum))),
		"filesha512":       stringFunc(fileFunc(readFile, hexHash(sha512Sum))),
		"flatten":          stdlib.FlattenFunc,
		"floor":            stdlib.FloorFunc,
		"format":           stdlib.FormatFunc,
		"formatdate":       stdlib.FormatDateFunc,
		"formatlist":       stdlib.FormatListFunc,
		"indent":           stdlib.IndentFunc,
		"index":            indexFunc,
		"issensitive":      isSensitiveFunc,
		"join":             stdlib.JoinFunc,
		"jsondecode":       stdlib.JSONDecodeFunc,
		"jsonencode":       stdlib.JSONEncodeFunc,
		"keys":             stdlib.KeysFunc,
		"length":           lengthFunc,
		"log":              stdlib.LogFunc,
		"lookup":           stdlib.LookupFunc,
		"lower":            stdlib.LowerFunc,
		"matchkeys":        matchKeysFunc,
		"max":              stdlib.MaxFunc,
		"md5":              stringFunc(hashFunc(md5Sum)),
		"merge":            stdlib.MergeFunc,
		"min":              stdlib.MinFunc,
		"nonsensitive":     nonSensitiveFunc,
		"one":              oneFunc,
		"parseint":         stdlib.ParseIntFunc,
		"pathexpand":       stringFunc(func(p string) (string, error) { return expandHome(p), nil }),
		"pow":              stdlib.PowFunc,
		"range":            stdlib.RangeFunc,
		"regex":            stdlib.RegexFunc,
		"regexall":         stdlib.RegexAllFunc,
		"replace":          replaceFunc,
		"reverse":          stdlib.ReverseListFunc,
		"rsadecrypt":       rsaDecryptFunc,
		"sensitive":        sensitiveFunc,
		"setintersection":  stdlib.SetIntersectionFunc,
		"setproduct":       stdlib.SetProductFunc,
		"setsubtract":      stdlib.SetSubtractFunc,
		"setunion":         stdlib.SetUnionFunc,
		"sha1":             stringFunc(hashFunc(sha1Sum)),
		"sha256":           stringFunc(hashFunc(sha256Sum)),
		"sha512":           stringFunc(hashFunc(sha512Sum)),
		"signum":           stdlib.SignumFunc,
		"slice":            stdlib.SliceFunc,
		"sort":             stdlib.SortFunc,
		"split":            stdlib.SplitFunc,
		"startswith":       stringPredicate(strings.HasPrefix),
		"strcontains":      stringPredicate(strings.Contains),
		"strrev":           stdlib.ReverseFunc,
		"substr":           stdlib.SubstrFunc,
		"sum":              sumFunc,
		"textdecodebase64": textDecodeBase64Func,
		"textencodebase64": textEncodeBase64Func,
		"timeadd":          stdlib.TimeAddFunc,
		"timecmp":          timeCmpFunc,
		"title":            stdlib.TitleFunc,
		"tobool":           stdlib.MakeToFunc(cty.Bool),
		"tolist":           stdlib.MakeToFunc(cty.List(cty.DynamicPseudoType)),
		"tomap":            stdlib.MakeToFunc(cty.Map(cty.DynamicPseudoType)),
		"tonumber":         stdlib.MakeToFunc(cty.Number),
		"toset":            stdlib.MakeToFunc(cty.Set(cty.DynamicPseudoType)),
		"tostring":         stdlib.MakeToFunc(cty.String),
		"transpose":        transposeFunc,
		"trim":             stdlib.TrimFunc,
		"trimprefix":       stdlib.TrimPrefixFunc,
		"trimspace":        stdlib.TrimSpaceFunc,
		"trimsuffix":       stdlib.TrimSuffixFunc,
		"try":              tryfunc.TryFunc,
		"upper":            stdlib.UpperFunc,
		"urlencode":        stringFunc(func(s string) (string, error) { return url.QueryEscape(s), nil }),
		"uuidv5":           uuidV5Func,
		"values":           stdlib.ValuesFunc,
		"yamldecode":       ctyyaml.YAMLDecodeFunc,
		"yamlencode":       ctyyaml.YAMLEncodeFunc,
		"zipmap":           stdlib.ZipmapFunc,

		"provider::terraform::decode_tfvars": decodeTfvarsFunc,
		"provider::terraform::encode_expr":   encodeExprFunc,
		"provider::terraform::encode_tfvars": encodeTfvarsFunc,
	}

	// Templates can call every function except the template functions
	// themselves.
	inTemplate := maps.Clone(funcs)
	inTemplate["templatefile"] = recursiveTemplateFunc
	inTemplate["templatestring"] = recursiveTemplateFunc

	funcs["templatefile"] = templateFunc("path", inTemplate, func(path string) (hcl.Expression, error) {
		b, err := readFile(path)
		if err != nil {
			return nil, err
		}
		return parseTemplate(b, path)
	})
	funcs["templatestring"] = templateFunc("template", inTemplate, func(src string) (hcl.Expression, error) {
		return parseTemplate([]byte(src), "<templatestring argument>")
	})

	return funcs
}

// expandHome replaces a leading "~" with the home directory.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, "~"+string(filepath.Separator)) {
		return path
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}

	return filepath.Join(home, path[1:])
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

func md5Sum(b []byte) []byte    { h := md5.Sum(b); return h[:] }
func sha1Sum(b []byte) []byte   { h := sha1.Sum(b); return h[:] }
func sha256Sum(b []byte) []byte { h := sha256.Sum256(b); return h[:] }
func sha512Sum(b []byte) []byte { h := sha512.Sum512(b); return h[:] }

func hexHash(sum func([]byte) []byte) func([]byte) string {
	return func(b []byte) string { return hex.EncodeToString(sum(b)) }
}

func base64Hash(sum func([]byte) []byte) func([]byte) string {
	return func(b []byte) string { return base64.StdEncoding.EncodeToString(sum(b)) }
}

func hashFunc(sum func([]byte) []byte) func(string) (string, error) {
	return func(s string) (string, error) { return hexHash(sum)([]byte(s)), nil }
}

func base64HashFunc(sum func([]byte) []byte) func(string) (string, error) {
	return func(s string) (string, error) { return base64Hash(sum)([]byte(s)), nil }
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

// coalesceFunc is Terraform's coalesce: unlike stdlib.CoalesceFunc, it
// also skips empty strings.
var coalesceFunc = function.New(&function.Spec{
	VarParam: &function.Parameter{
		Name:             "vals",
		Type:             cty.DynamicPseudoType,
		AllowUnknown:     true,
		AllowDynamicType: true,
		AllowNull:        true,
	},
	Type: func(args []cty.Value) (cty.Type, error) {
		types := make([]cty.Type, len(args))
		for i, v := range args {
			types[i] = v.Type()
		}

		ty, _ := convert.UnifyUnsafe(types)
		if ty == cty.NilType {
			return cty.NilType, errors.New("all arguments must have the same type")
		}

		return ty, nil
	},
	Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
		for _, v := range args {
			v, err := convert.Convert(v, retType)
			if err != nil {
				return cty.UnknownVal(retType), err
			}

			if !v.IsKnown() {
				return cty.UnknownVal(retType), nil
			}

			if v.IsNull() || (retType == cty.String && v.RawEquals(cty.StringVal(""))) {
				continue
			}

			return v, nil
		}

		return cty.NilVal, errors.New("no non-null, non-empty-string arguments")
	},
})

// lengthFunc is Terraform's length: unlike stdlib.LengthFunc, it also
// accepts strings and counts their characters.
var lengthFunc = function.New(&function.Spec{
	Params: []function.Parameter{{
		Name:             "value",
		Type:             cty.DynamicPseudoType,
		AllowUnknown:     true,
		AllowDynamicType: true,
	}},
	Type: function.StaticReturnType(cty.Number),
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		v := args[0]
		ty := v.Type()

		switch {
		case ty == cty.DynamicPseudoType:
			return cty.UnknownVal(cty.Number), nil
		case ty.IsTupleType():
			return cty.NumberIntVal(int64(ty.Length())), nil
		case ty.IsObjectType():
			return cty.NumberIntVal(int64(len(ty.AttributeTypes()))), nil
		case ty == cty.String:
			if !v.IsKnown() {
				return cty.UnknownVal(cty.Number), nil
			}
			return stdlib.Strlen(v)
		case ty.IsListType() || ty.IsSetType() || ty.IsMapType():
			return v.Length(), nil
		}

		return cty.UnknownVal(cty.Number), errors.New("argument must be a string, a collection type, or a structural type")
	},
})
