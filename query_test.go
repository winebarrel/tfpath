package tfpath_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/winebarrel/tfpath"
)

const queryFixture = `
variable "v" {
  default = { list = [1, 2], map = { a = "x" } }
}

locals {
  l = "local"
}

output "o" {
  value = "output"
}

resource "r" "single" {
  list = ["a", "b"]
  map  = { k1 = "v1", "k.2" = "v2", "a\"b" = "v3" }
  deep = { a = { b = { c = 1 } } }
}

resource "r" "counted" {
  count = 2
  i     = count.index
}

resource "r" "keyed" {
  for_each = toset(["0", "a"])
  k        = each.key
}

resource "other" "single" {
  x = 1
}

data "r" "single" {
  d = true
}

module "m" {
  source   = "./m"
  for_each = toset(["a", "b"])
}

module "c" {
  source = "./m"
  count  = 1
}
`

const queryModuleFixture = `
resource "r" "single" {
  in = "module"
}

module "n" {
  source = "./n"
}
`

func queryConfig(t *testing.T) *tfpath.Config {
	t.Helper()
	dir := writeFiles(t, map[string]string{
		"main.tf":     queryFixture,
		"m/main.tf":   queryModuleFixture,
		"m/n/main.tf": `resource "r" "single" { in = "nested" }`,
	})
	return load(t, &tfpath.Options{Dir: dir})
}

func TestQueryAddresses(t *testing.T) {
	cfg := queryConfig(t)

	tests := []struct {
		query    string
		expected string
	}{
		{`r.single.list`, `{"r.single.list": ["a", "b"]}`},
		{`data.r.single`, `{"data.r.single": {"d": true}}`},
		{`var.v.list`, `{"var.v.list": [1, 2]}`},
		{`local.l`, `{"local.l": "local"}`},
		{`output.o`, `{"output.o": "output"}`},
		// An address without a key matches every instance.
		{`r.counted.i`, `{"r.counted[0].i": 0, "r.counted[1].i": 1}`},
		{`r.counted[1]`, `{"r.counted[1]": {"i": 1}}`},
		{`r.counted[*].i`, `{"r.counted[0].i": 0, "r.counted[1].i": 1}`},
		{`r.keyed["a"].k`, `{"r.keyed[\"a\"].k": "a"}`},
		// Keys are typed: [0] is a count index, ["0"] a for_each key.
		{`r.keyed[0]`, `{}`},
		{`r.keyed["0"].k`, `{"r.keyed[\"0\"].k": "0"}`},
		{`r.counted["0"]`, `{}`},
		{`r.single[0]`, `{}`},
		{`r.counted[2]`, `{}`},
		// A leading * matches managed resource types only.
		{`*.single`, `{
			"r.single": {"list": ["a", "b"], "map": {"k1": "v1", "k.2": "v2", "a\"b": "v3"}, "deep": {"a": {"b": {"c": 1}}}},
			"other.single": {"x": 1}
		}`},
		{`r.*.k`, `{"r.keyed[\"0\"].k": "0", "r.keyed[\"a\"].k": "a"}`},
		{`data.*.*.d`, `{"data.r.single.d": true}`},
		{`var.*.map.a`, `{"var.v.map.a": "x"}`},
		{`nope.single`, `{}`},
		{`var.nope`, `{}`},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			assert.JSONEq(t, tt.expected, query(t, cfg, tt.query))
		})
	}
}

func TestQueryModules(t *testing.T) {
	cfg := queryConfig(t)

	tests := []struct {
		query    string
		expected string
	}{
		{`module.m.r.single.in`, `{"module.m[\"a\"].r.single.in": "module", "module.m[\"b\"].r.single.in": "module"}`},
		{`module.m["b"].r.single.in`, `{"module.m[\"b\"].r.single.in": "module"}`},
		{`module.m[*].r.single.in`, `{"module.m[\"a\"].r.single.in": "module", "module.m[\"b\"].r.single.in": "module"}`},
		{`module.c[0].r.single.in`, `{"module.c[0].r.single.in": "module"}`},
		{`module.c["0"].r.single.in`, `{}`},
		{`module.*.module.n.r.single.in`, `{
			"module.m[\"a\"].module.n.r.single.in": "nested",
			"module.m[\"b\"].module.n.r.single.in": "nested",
			"module.c[0].module.n.r.single.in": "nested"
		}`},
		// ** matches zero or more module levels.
		{`**.r.single.in`, `{
			"module.m[\"a\"].r.single.in": "module",
			"module.m[\"b\"].r.single.in": "module",
			"module.c[0].r.single.in": "module",
			"module.m[\"a\"].module.n.r.single.in": "nested",
			"module.m[\"b\"].module.n.r.single.in": "nested",
			"module.c[0].module.n.r.single.in": "nested"
		}`},
		{`module.m["a"].**.r.single.in`, `{
			"module.m[\"a\"].r.single.in": "module",
			"module.m[\"a\"].module.n.r.single.in": "nested"
		}`},
		// Repeated ** reaches the same value in several ways but each
		// path is reported once.
		{`**.**.module.n.r.single.in`, `{
			"module.m[\"a\"].module.n.r.single.in": "nested",
			"module.m[\"b\"].module.n.r.single.in": "nested",
			"module.c[0].module.n.r.single.in": "nested"
		}`},
		{`module.nope.r.single`, `{}`},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			assert.JSONEq(t, tt.expected, query(t, cfg, tt.query))
		})
	}
}

func TestQueryIncompleteAddresses(t *testing.T) {
	cfg := queryConfig(t)

	// A query must reach a resource, data source, variable, local or
	// output; a module or a resource type alone matches nothing.
	for _, q := range []string{
		`module`,
		`module.m`,
		`module.m["a"]`,
		`data`,
		`data.r`,
		`r`,
		`var`,
		`local`,
		`output`,
		`**`,
	} {
		t.Run(q, func(t *testing.T) {
			assert.JSONEq(t, `{}`, query(t, cfg, q))
		})
	}
}

func TestQueryValuePaths(t *testing.T) {
	cfg := queryConfig(t)

	tests := []struct {
		query    string
		expected string
	}{
		{`r.single.list[1]`, `{"r.single.list[1]": "b"}`},
		{`r.single.list[*]`, `{"r.single.list[0]": "a", "r.single.list[1]": "b"}`},
		{`r.single.list.*`, `{"r.single.list[0]": "a", "r.single.list[1]": "b"}`},
		{`r.single.map.*`, `{"r.single.map.k1": "v1", "r.single.map[\"k.2\"]": "v2", "r.single.map[\"a\\\"b\"]": "v3"}`},
		{`r.single.map[*]`, `{"r.single.map.k1": "v1", "r.single.map[\"k.2\"]": "v2", "r.single.map[\"a\\\"b\"]": "v3"}`},
		{`r.single.map["k1"]`, `{"r.single.map.k1": "v1"}`},
		{`r.single.map["k.2"]`, `{"r.single.map[\"k.2\"]": "v2"}`},
		{`r.single.map["a\"b"]`, `{"r.single.map[\"a\\\"b\"]": "v3"}`},
		{`r.single.deep.a.b.c`, `{"r.single.deep.a.b.c": 1}`},
		// ** inside a value matches any depth, including zero.
		{`r.single.deep.**.c`, `{"r.single.deep.a.b.c": 1}`},
		{`r.single.deep.**`, `{
			"r.single.deep": {"a": {"b": {"c": 1}}},
			"r.single.deep.a": {"b": {"c": 1}},
			"r.single.deep.a.b": {"c": 1},
			"r.single.deep.a.b.c": 1
		}`},
		{`r.single.**.c`, `{"r.single.deep.a.b.c": 1}`},
		// Mismatched steps match nothing.
		{`r.single.list[2]`, `{}`},
		{`r.single.list[-1]`, `{}`},
		{`r.single.list.x`, `{}`},
		{`r.single.list["0"]`, `{}`},
		{`r.single.map[0]`, `{}`},
		{`r.single.map.nope`, `{}`},
		{`r.single.deep.a.b.c.d`, `{}`},
		{`r.single.deep.a.b.c.*`, `{}`},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			assert.JSONEq(t, tt.expected, query(t, cfg, tt.query))
		})
	}
}

func TestMatchOrder(t *testing.T) {
	cfg := queryConfig(t)

	q, err := tfpath.ParseQuery(`r.counted[*].i`)
	require.NoError(t, err)

	matches := cfg.Match(q)
	require.Len(t, matches, 2)
	assert.Equal(t, "r.counted[0].i", matches[0].Path)
	assert.Equal(t, "r.counted[1].i", matches[1].Path)
}

func TestParseQuerySyntax(t *testing.T) {
	valid := []string{
		`a`,
		`a.b`,
		`a-b.c_d.E9`,
		`a[0]`,
		`a[ 0 ]`,
		`a[-1]`,
		`a["k"]`,
		`a["k\"]"]`,
		`a["k.l"].b`,
		`a[*]`,
		`a[0][1]["x"]`,
		`*.*`,
		`**.a`,
		`a.**`,
	}

	for _, q := range valid {
		t.Run("valid "+q, func(t *testing.T) {
			parsed, err := tfpath.ParseQuery(q)
			require.NoError(t, err)
			assert.Equal(t, q, parsed.String())
		})
	}

	invalid := map[string]string{
		``:           "expected name at offset 0",
		`.a`:         "expected name at offset 0",
		`a.`:         "expected name at offset 2",
		`a..b`:       "expected name at offset 2",
		`[0]`:        "expected name at offset 0",
		`a[`:         "unclosed '[' at offset 1",
		`a["k`:       "unclosed '[' at offset 1",
		`a[0`:        "unclosed '[' at offset 1",
		`a[]`:        `invalid index "" at offset 1`,
		`a[x]`:       `invalid index "x" at offset 1`,
		`a[1.5]`:     `invalid index "1.5" at offset 1`,
		`a["\q"]`:    `invalid key "\q" at offset 1`,
		`a*`:         `invalid wildcard "a*" at offset 0`,
		`***`:        `invalid wildcard "***" at offset 0`,
		`a b`:        `unexpected ' ' at offset 1`,
		`a/b`:        `unexpected '/' at offset 1`,
		`a[0]b`:      `unexpected 'b' at offset 4`,
		`a.b[0].[1]`: "expected name at offset 7",
	}

	for q, msg := range invalid {
		t.Run("invalid "+q, func(t *testing.T) {
			_, err := tfpath.ParseQuery(q)
			require.ErrorContains(t, err, msg)
			require.ErrorContains(t, err, `invalid query "`)
		})
	}
}
