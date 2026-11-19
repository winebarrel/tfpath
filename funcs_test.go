package tfpath_test

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
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
		{`templatestring("Hello, $${name}!", { name = "Jane" })`, `"Hello, Jane!"`},
		{`templatestring("$${upper(x)}", { x = "a" })`, `"A"`},
		{`templatestring("$${x}", { x = [1] })`, `[1]`},
		{`templatestring("$${y}", { x = 1 })`, `"${templatestring(\"$${y}\", { x = 1 })}"`},
		{`templatestring("$${templatestring(\"a\", {})}", {})`, `"${templatestring(\"$${templatestring(\\\"a\\\", {})}\", {})}"`},
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
		{`alltrue(["true", true])`, `true`},
		{`alltrue([true, false])`, `false`},
		{`alltrue([])`, `true`},
		{`anytrue([false, "true"])`, `true`},
		{`anytrue([])`, `false`},
		{`index(["a", "b", "c"], "b")`, `1`},
		{`index(["a"], "z")`, `"${index([\"a\"], \"z\")}"`},
		{`matchkeys(["i-123", "i-abc", "i-def"], ["us-west", "us-east", "us-east"], ["us-east"])`, `["i-abc", "i-def"]`},
		{`matchkeys(["a"], ["x"], ["y"])`, `[]`},
		{`one([])`, `null`},
		{`one(["hello"])`, `"hello"`},
		{`one(toset(["a", "a"]))`, `"a"`},
		{`one(["a", "b"])`, `"${one([\"a\", \"b\"])}"`},
		{`sum([10, 13, 6, 4.5])`, `33.5`},
		{`sum(["1", 2])`, `3`},
		{`sum([])`, `"${sum([])}"`},
		{`transpose({ a = ["1", "2"], b = ["2", "3"] })`, `{"1": ["a"], "2": ["a", "b"], "3": ["b"]}`},
		// encoding and hashing
		{`base64decode("aGk=")`, `"hi"`},
		{`base64encode("hi")`, `"aGk="`},
		{`csvdecode("a,b\n1,2\n")`, `[{"a": "1", "b": "2"}]`},
		{`jsondecode("{\"a\":[1]}")`, `{"a": [1]}`},
		{`jsonencode({ a = [1] })`, `"{\"a\":[1]}"`},
		{`md5("x")`, `"9dd4e461268c8034f5c8564e155c67a6"`},
		{`sha1("x")`, `"11f6ad8ec52a2984abaafd7c3b516503785c2072"`},
		{`sha256("x")`, `"2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881"`},
		{`sha512("hello world")`, `"309ecc489c12d6eb4cc40f50c902f2b4d0ed77ee511a7c7a9bcd3ca86d4cd86f989dd35bc5ff499670da34255b45b0cfd830e81f605dcf7dc5542e93ae9cd76f"`},
		{`base64sha256("hello world")`, `"uU0nuZNNPgilLlLX2n2r+sSE7+N6U4DukIj3rOLvzek="`},
		{`base64sha512("hello world")`, `"MJ7MSJwS1utMxA9QyQLytNDtd+5RGnx6m808qG1M2G+YndNbxf9JlnDaNCVbRbDP2DDoH2Bdz33FVC6TrpzXbw=="`},
		{`base64decode("/w==")`, `"${base64decode(\"/w==\")}"`},
		{`textencodebase64("Hello World", "UTF-16LE")`, `"SABlAGwAbABvACAAVwBvAHIAbABkAA=="`},
		{`textdecodebase64("SABlAGwAbABvACAAVwBvAHIAbABkAA==", "UTF-16LE")`, `"Hello World"`},
		{`textencodebase64("a", "nope")`, `"${textencodebase64(\"a\", \"nope\")}"`},
		{`urlencode("Hello World!")`, `"Hello+World%21"`},
		{`uuidv5("dns", "www.terraform.io")`, `"a5008fae-b28c-5ba5-96cd-82b4c53552d6"`},
		{`uuidv5("url", "https://www.terraform.io/")`, `"9db6f67c-dd95-5ea0-aa5b-e70e5c5f7cf5"`},
		{`uuidv5("6ba7b810-9dad-11d1-80b4-00c04fd430c8", "www.terraform.io")`, `"a5008fae-b28c-5ba5-96cd-82b4c53552d6"`},
		{`yamldecode("a: 1\nb: [x]\n")`, `{"a": 1, "b": ["x"]}`},
		{`yamlencode({ a = "b" })`, `"\"a\": \"b\"\n"`},
		// network
		{`cidrhost("10.12.112.0/20", 16)`, `"10.12.112.16"`},
		{`cidrhost("10.12.112.0/20", 268)`, `"10.12.113.12"`},
		{`cidrhost("fd00:fd12:3456:7890:00a2::/72", 34)`, `"fd00:fd12:3456:7890::22"`},
		{`cidrnetmask("172.16.0.0/12")`, `"255.240.0.0"`},
		{`cidrnetmask("fd00::/8")`, `"${cidrnetmask(\"fd00::/8\")}"`},
		{`cidrsubnet("172.16.0.0/12", 4, 2)`, `"172.18.0.0/16"`},
		{`cidrsubnet("10.1.2.0/24", 4, 15)`, `"10.1.2.240/28"`},
		{`cidrsubnet("fd00:fd12:3456:7890::/56", 16, 162)`, `"fd00:fd12:3456:7800:a200::/72"`},
		{`cidrsubnets("10.1.0.0/16", 4, 4, 8, 4)`, `["10.1.0.0/20", "10.1.16.0/20", "10.1.32.0/24", "10.1.48.0/20"]`},
		{`cidrsubnets("10.0.0.0/30", 1, 1, 1)`, `"${cidrsubnets(\"10.0.0.0/30\", 1, 1, 1)}"`},
		// date and time
		{`formatdate("YYYY-MM-DD", "2026-01-02T03:04:05Z")`, `"2026-01-02"`},
		{`timeadd("2026-01-01T00:00:00Z", "1h")`, `"2026-01-01T01:00:00Z"`},
		{`timecmp("2017-11-22T00:00:00Z", "2017-11-22T01:00:00Z")`, `-1`},
		{`timecmp("2017-11-22T01:00:00+01:00", "2017-11-22T00:00:00Z")`, `0`},
		{`timecmp("2017-11-22T02:00:00Z", "2017-11-22T01:00:00Z")`, `1`},
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
		{`sensitive("x")`, `"x"`},
		{`issensitive(sensitive("x"))`, `true`},
		{`issensitive("x")`, `false`},
		{`issensitive(nonsensitive(sensitive("x")))`, `false`},
		{`ephemeralasnull({ a = "x" })`, `{"a": "x"}`},
		// terraform provider
		{`provider::terraform::encode_tfvars({ a = "b", n = 1 })`, `"a = \"b\"\nn = 1\n"`},
		{`provider::terraform::decode_tfvars("a = \"b\"\nn = [1]\n")`, `{"a": "b", "n": [1]}`},
		{`provider::terraform::encode_expr(["a", 1])`, `"[\"a\", 1]"`},
		// errors and unsupported functions are kept as source text
		{`base64decode("!!")`, `"${base64decode(\"!!\")}"`},
		{`templatefile("x.tpl", {})`, `"${templatefile(\"x.tpl\", {})}"`},
		{`timestamp()`, `"${timestamp()}"`},
		{`plantimestamp()`, `"${plantimestamp()}"`},
		{`uuid()`, `"${uuid()}"`},
		{`bcrypt("x")`, `"${bcrypt(\"x\")}"`},
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

func TestFileFunctionsExtra(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bin.dat"), []byte{0xff}, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "greet.tpl"), []byte("Hello, ${name}! %{ for x in items }[${upper(x)}]%{ endfor }"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "loop.tpl"), []byte(`${templatefile("loop.tpl", {})}`), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "files", "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "files", "a.txt"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "files", "sub", "b.txt"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "files", "c.json"), nil, 0o644))

	home, err := os.UserHomeDir()
	require.NoError(t, err)

	t.Chdir(t.TempDir())

	tests := []struct {
		expr     string
		expected any
	}{
		{`filebase64("hello.txt")`, "aGVsbG8="},
		{`filebase64("bin.dat")`, "/w=="},
		{`file("bin.dat")`, `${file("bin.dat")}`},
		{`filemd5("hello.txt")`, "5d41402abc4b2a76b9719d911017c592"},
		{`filesha1("hello.txt")`, "aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d"},
		{`filesha256("hello.txt")`, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"},
		{`filesha512("hello.txt")`, "9b71d224bd62f3785d96d46ad3ea3d73319bfbc2890caadae2dff72519673ca72323c3d99ba5c11d7c7acc6e14b8c5da0c4663475c2e5c3adef46f73bcdec043"},
		{`filebase64sha256("hello.txt")`, "LPJNul+wow4m6DsqxbninhsWHlwfp0JecwQzYpOLmCQ="},
		{`filebase64sha512("hello.txt")`, "m3HSJL1i83hdltRq0+o9czGb+8KJDKra4t/3JRlnPKcjI8PZm6XBHXx6zG4UuMXaDEZjR1wuXDre9G9zvN7AQw=="},
		{`fileset(".", "files/**/*.txt")`, []any{"files/a.txt", "files/sub/b.txt"}},
		{`fileset("files", "*")`, []any{"a.txt", "c.json"}},
		{`fileset("files", "*.nope")`, []any{}},
		{`templatefile("greet.tpl", { name = "Jane", items = ["a", "b"] })`, "Hello, Jane! [A][B]"},
		{`templatefile("greet.tpl", { name = "Jane" })`, `${templatefile("greet.tpl", { name = "Jane" })}`},
		{`templatefile("loop.tpl", {})`, `${templatefile("loop.tpl", {})}`},
		{`abspath("files/../hello.txt")`, filepath.ToSlash(filepath.Join(dir, "hello.txt"))},
		{`basename("foo/bar/baz.txt")`, "baz.txt"},
		{`dirname("foo/bar/baz.txt")`, filepath.FromSlash("foo/bar")},
		{`pathexpand("~/x")`, filepath.Join(home, "x")},
		{`pathexpand("/etc")`, "/etc"},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			assert.Equal(t, tt.expected, evalLocal(t, dir, tt.expr))
		})
	}
}

func TestRSADecrypt(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	ciphertext, err := rsa.EncryptPKCS1v15(rand.Reader, &key.PublicKey, []byte("secret")) //nolint:staticcheck
	require.NoError(t, err)

	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	b64 := base64.StdEncoding.EncodeToString(ciphertext)

	dir := t.TempDir()
	assert.Equal(t, "secret", evalLocal(t, dir, `rsadecrypt("`+b64+`", <<EOT
`+string(keyPEM)+`EOT
)`))
	assert.Equal(t, `${rsadecrypt("!!", "x")}`, evalLocal(t, dir, `rsadecrypt("!!", "x")`))
}

func TestSensitiveAndEphemeralVariables(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
variable "secret" {
  default   = "s3cr3t"
  sensitive = true
}

variable "token" {
  default   = "t0ken"
  ephemeral = true
}

variable "plain" {
  default = "p"
}

locals {
  secret_is_sensitive = issensitive(var.secret)
  plain_is_sensitive  = issensitive(var.plain)
  revealed            = issensitive(nonsensitive(var.secret))
  upper_secret        = upper(var.secret)
  token               = ephemeralasnull(var.token)
  tokens              = ephemeralasnull({ a = var.token, b = var.plain })
}
`), 0o644))

	cfg := load(t, &tfpath.Options{Dir: dir})

	for query, expected := range map[string]any{
		"var.secret":                "s3cr3t",
		"local.secret_is_sensitive": true,
		"local.plain_is_sensitive":  false,
		"local.revealed":            false,
		"local.upper_secret":        "S3CR3T",
		"local.token":               nil,
		"local.tokens":              map[string]any{"a": nil, "b": "p"},
	} {
		matches, err := cfg.Query(query)
		require.NoError(t, err)
		require.Len(t, matches, 1, query)
		assert.Equal(t, expected, matches[0].Value, query)
	}
}

// The gzip bytes depend on the Go version, so check a round trip.
func TestBase64Gzip(t *testing.T) {
	v := evalLocal(t, t.TempDir(), `base64gzip("test")`)
	s, ok := v.(string)
	require.True(t, ok, v)

	b, err := base64.StdEncoding.DecodeString(s)
	require.NoError(t, err)

	r, err := gzip.NewReader(bytes.NewReader(b))
	require.NoError(t, err)

	out, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Equal(t, "test", string(out))
}
