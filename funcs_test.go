package tfpath_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/winebarrel/tfpath"
)

func evalLocal(t *testing.T, dir, expr string) any {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte("locals {\n  v = "+expr+"\n}\n"), 0o644))

	cfg := load(t, &tfpath.Options{Dir: dir})
	matches, err := cfg.Query("local.v")
	require.NoError(t, err)
	require.Len(t, matches, 1)

	return matches[0].Value
}

func TestFunctions(t *testing.T) {
	tests := []struct {
		expr     string
		expected string
	}{
		// numeric
		{`abs(-1)`, `1`},
		{`ceil(1.2)`, `2`},
		{`floor(1.8)`, `1`},
		{`log(8, 2)`, `3`},
		{`max(1, 3, 2)`, `3`},
		{`min(1, 3, 2)`, `1`},
		{`parseint("ff", 16)`, `255`},
		{`pow(2, 3)`, `8`},
		{`signum(-5)`, `-1`},
		// string
		{`chomp("a\n")`, `"a"`},
		{`endswith("abc", "bc")`, `true`},
		{`format("%s-%03d", "a", 7)`, `"a-007"`},
		{`formatlist("%s!", ["a", "b"])`, `["a!", "b!"]`},
		{`indent(2, "a\nb")`, `"a\n  b"`},
		{`join(",", ["a", "b"])`, `"a,b"`},
		{`lower("AB")`, `"ab"`},
		{`regex("[0-9]+", "ab12")`, `"12"`},
		{`regexall("[0-9]", "a1b2")`, `["1", "2"]`},
		{`replace("a-b-c", "-", "+")`, `"a+b+c"`},
		{`replace("a1b22", "/[0-9]+/", "#")`, `"a#b#"`},
		{`replace("a/b", "/", "_")`, `"a_b"`},
		{`split(",", "a,b")`, `["a", "b"]`},
		{`startswith("abc", "ab")`, `true`},
		{`strcontains("abc", "d")`, `false`},
		{`strrev("abc")`, `"cba"`},
		{`substr("hello", 1, 3)`, `"ell"`},
		{`title("hello world")`, `"Hello World"`},
		{`trim("xxaxx", "x")`, `"a"`},
		{`trimprefix("prefix-a", "prefix-")`, `"a"`},
		{`trimspace("  a  ")`, `"a"`},
		{`trimsuffix("a.txt", ".txt")`, `"a"`},
		{`upper("ab")`, `"AB"`},
		// collection
		{`chunklist([1, 2, 3], 2)`, `[[1, 2], [3]]`},
		{`coalesce(null, "a")`, `"a"`},
		{`coalesce("", "a")`, `"a"`},
		{`coalesce(null, "", "b", "c")`, `"b"`},
		{`coalesce(1, "a")`, `"1"`},
		{`coalesce([], ["a"])`, `[]`},
		{`coalesce(null, ["a"])`, `["a"]`},
		{`coalesce("", "")`, `"${coalesce(\"\", \"\")}"`},
		{`coalesce({ a = 1 }, "x")`, `"${coalesce({ a = 1 }, \"x\")}"`},
		{`coalesce("", aws_x.y.id, "a")`, `"${coalesce(\"\", aws_x.y.id, \"a\")}"`},
		{`coalescelist([], ["a"])`, `["a"]`},
		{`compact(["a", "", "b"])`, `["a", "b"]`},
		{`concat(["a"], ["b"])`, `["a", "b"]`},
		{`contains(["a"], "a")`, `true`},
		{`distinct(["a", "a", "b"])`, `["a", "b"]`},
		{`element(["a", "b"], 3)`, `"b"`},
		{`flatten([["a"], ["b", ["c"]]])`, `["a", "b", "c"]`},
		{`keys({ b = 1, a = 2 })`, `["a", "b"]`},
		{`length(["a", "b"])`, `2`},
		{`length("abc")`, `3`},
		{`length("héllo")`, `5`},
		{`length("👨‍👩‍👧")`, `1`},
		{`length("")`, `0`},
		{`length({ a = 1, b = 2 })`, `2`},
		{`length(tomap({ a = "1" }))`, `1`},
		{`length(toset(["a", "a"]))`, `1`},
		{`length(tolist(["a"]))`, `1`},
		{`length([aws_x.y.id, 1])`, `2`},
		{`length(aws_x.y.ids)`, `"${length(aws_x.y.ids)}"`},
		{`length("${aws_x.y.id}")`, `"${length(\"${aws_x.y.id}\")}"`},
		{`length(tolist(aws_x.y.ids))`, `"${length(tolist(aws_x.y.ids))}"`},
		{`length(1)`, `"${length(1)}"`},
		{`length(null)`, `"${length(null)}"`},
		{`lookup({ a = 1 }, "b", 0)`, `0`},
		{`merge({ a = 1 }, { b = 2 })`, `{"a": 1, "b": 2}`},
		{`range(3)`, `[0, 1, 2]`},
		{`reverse([1, 2])`, `[2, 1]`},
		{`setintersection(["a", "b"], ["b"])`, `["b"]`},
		{`setproduct(["a"], ["b"])`, `[["a", "b"]]`},
		{`setsubtract(["a", "b"], ["a"])`, `["b"]`},
		{`setunion(["a"], ["b"])`, `["a", "b"]`},
		{`slice(["a", "b", "c"], 1, 2)`, `["b"]`},
		{`sort(["b", "a"])`, `["a", "b"]`},
		{`values({ a = 1, b = 2 })`, `[1, 2]`},
		{`zipmap(["a"], [1])`, `{"a": 1}`},
		// encoding and hashing
		{`base64decode("aGk=")`, `"hi"`},
		{`base64encode("hi")`, `"aGk="`},
		{`csvdecode("a,b\n1,2\n")`, `[{"a": "1", "b": "2"}]`},
		{`jsondecode("{\"a\":[1]}")`, `{"a": [1]}`},
		{`jsonencode({ a = [1] })`, `"{\"a\":[1]}"`},
		{`md5("x")`, `"9dd4e461268c8034f5c8564e155c67a6"`},
		{`sha1("x")`, `"11f6ad8ec52a2984abaafd7c3b516503785c2072"`},
		{`sha256("x")`, `"2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881"`},
		// date and time
		{`formatdate("YYYY-MM-DD", "2026-01-02T03:04:05Z")`, `"2026-01-02"`},
		{`timeadd("2026-01-01T00:00:00Z", "1h")`, `"2026-01-01T01:00:00Z"`},
		// type conversion
		{`tobool("true")`, `true`},
		{`tolist(["a"])`, `["a"]`},
		{`tomap({ a = "b" })`, `{"a": "b"}`},
		{`tonumber("1")`, `1`},
		{`toset(["b", "a", "b"])`, `["a", "b"]`},
		{`tostring(1)`, `"1"`},
		{`try(aws_x.y.id, "fallback")`, `"${try(aws_x.y.id, \"fallback\")}"`},
		{`try(local.nope, "fallback")`, `"fallback"`},
		{`can(regex("[", "a"))`, `false`},
		// errors and unsupported functions are kept as source text
		{`base64decode("!!")`, `"${base64decode(\"!!\")}"`},
		{`templatefile("x.tpl", {})`, `"${templatefile(\"x.tpl\", {})}"`},
		{`provider::aws::arn_parse("x")`, `"${provider::aws::arn_parse(\"x\")}"`},
		{`upper(aws_x.y.id)`, `"${upper(aws_x.y.id)}"`},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			dir := t.TempDir()
			b, err := json.Marshal(evalLocal(t, dir, tt.expr))
			require.NoError(t, err)
			assert.JSONEq(t, tt.expected, string(b))
		})
	}
}

func TestFileFunctions(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "data.txt"), []byte("relative"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "subdir"), 0o755))

	other := filepath.Join(t.TempDir(), "abs.txt")
	require.NoError(t, os.WriteFile(other, []byte("absolute"), 0o644))

	// Relative paths are resolved against the root module directory, not
	// the current directory.
	t.Chdir(t.TempDir())

	assert.Equal(t, "relative", evalLocal(t, dir, `file("data.txt")`))
	assert.Equal(t, "absolute", evalLocal(t, dir, `file("`+filepath.ToSlash(other)+`")`))
	assert.Equal(t, "${file(\"nope.txt\")}", evalLocal(t, dir, `file("nope.txt")`))
	assert.Equal(t, true, evalLocal(t, dir, `fileexists("data.txt")`))
	assert.Equal(t, true, evalLocal(t, dir, `fileexists("`+filepath.ToSlash(other)+`")`))
	assert.Equal(t, false, evalLocal(t, dir, `fileexists("nope.txt")`))
	assert.Equal(t, false, evalLocal(t, dir, `fileexists("subdir")`))
}
