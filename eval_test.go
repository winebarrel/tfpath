package tfpath_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/winebarrel/tfpath"
)

func TestUnknownReferences(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.tf": `
locals {
  resource   = aws_iam_role.r.arn
  data       = data.aws_caller_identity.c.account_id
  self       = self.id
  count      = count.index
  each       = each.key
  module_idx = module["x"]
  undeclared = local.nope
  template   = "arn:${aws_iam_role.r.arn}:x"
  partial    = { known = "k", unknown = aws_iam_role.r.arn, nested = [1, aws_iam_role.r.id] }
  merged     = merge({ a = 1 }, { b = aws_iam_role.r.arn })
  cond       = aws_iam_role.r.arn != "" ? "a" : "b"
  heredoc    = <<-EOT
    arn=${aws_iam_role.r.arn}
  EOT
  known_heredoc = <<-EOT
    hello
  EOT
}
`,
	})

	cfg := load(t, &tfpath.Options{Dir: dir})

	assert.JSONEq(t, `{
		"local.resource": "${aws_iam_role.r.arn}",
		"local.data": "${data.aws_caller_identity.c.account_id}",
		"local.self": "${self.id}",
		"local.count": "${count.index}",
		"local.each": "${each.key}",
		"local.module_idx": "${module[\"x\"]}",
		"local.undeclared": "${local.nope}",
		"local.template": "arn:${aws_iam_role.r.arn}:x",
		"local.partial": {"known": "k", "unknown": "${aws_iam_role.r.arn}", "nested": [1, "${aws_iam_role.r.id}"]},
		"local.merged": "${merge({ a = 1 }, { b = aws_iam_role.r.arn })}",
		"local.cond": "${aws_iam_role.r.arn != \"\" ? \"a\" : \"b\"}",
		"local.heredoc": "<<-EOT\n    arn=${aws_iam_role.r.arn}\n  EOT",
		"local.known_heredoc": "hello\n"
	}`, query(t, cfg, "local.*"))
}

func TestLocals(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.tf": `
locals {
  a = "a"
}

locals {
  b       = "${local.a}b"
  indexed = local["a"]
  for     = [for s in ["x", "y"] : upper(s)]
  formap  = { for s in ["x", "y"] : s => "${s}${local.a}" }
  splat   = [{ n = 1 }, { n = 2 }][*].n
  null    = null
  big     = 12345678901234567890
  float   = 1.5
  key_expr = { (local.a) = 1, "quoted" = 2, bare = 3 }
  unknown_key = { (aws_x.y.id) = 1 }
}
`,
	})

	cfg := load(t, &tfpath.Options{Dir: dir})

	assert.JSONEq(t, `{
		"local.a": "a",
		"local.b": "ab",
		"local.indexed": "a",
		"local.for": ["X", "Y"],
		"local.formap": {"x": "xa", "y": "ya"},
		"local.splat": [1, 2],
		"local.null": null,
		"local.big": 12345678901234567890,
		"local.float": 1.5,
		"local.key_expr": {"a": 1, "quoted": 2, "bare": 3},
		"local.unknown_key": {"${(aws_x.y.id)}": 1}
	}`, query(t, cfg, "local.*"))

	matches, err := cfg.Query("local.big")
	require.NoError(t, err)
	require.Len(t, matches, 1)
	assert.Equal(t, json.Number("12345678901234567890"), matches[0].Value)
}

func TestPath(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.tf": `
locals {
  module = path.module
  root   = path.root
  cwd    = path.cwd
}

module "child" {
  source = "./modules/child"
}
`,
		"modules/child/main.tf": `
locals {
  module = path.module
  file   = file("${path.module}/data.txt")
}

module "grandchild" {
  source = "./grandchild"
}
`,
		"modules/child/data.txt":              "child data",
		"modules/child/grandchild/main.tf":    `locals { module = path.module }`,
		"modules/child/grandchild/.gitignore": "",
	})

	cfg := load(t, &tfpath.Options{Dir: dir})

	abs, err := filepath.Abs(dir)
	require.NoError(t, err)

	expected, err := json.Marshal(map[string]any{
		"local.module":              ".",
		"local.root":                ".",
		"local.cwd":                 abs,
		"module.child.local.module": "modules/child",
		"module.child.local.file":   "child data",
		"module.child.module.grandchild.local.module": "modules/child/grandchild",
	})
	require.NoError(t, err)

	assert.JSONEq(t, string(expected), query(t, cfg, "**.local.*"))
}

func TestCountExpansion(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.tf": `
resource "x" "two" {
  count = 2
  i     = count.index
}

resource "x" "string_count" {
  count = "1"
  i     = count.index
}

resource "x" "zero" {
  count = 0
}

resource "x" "fraction" {
  count = 1.5
  i     = count.index
}

resource "x" "not_number" {
  count = "abc"
  i     = count.index
}

resource "x" "null" {
  count = null
  i     = count.index
}

resource "x" "unknown" {
  count = length(aws_x.y.ids)
  i     = count.index
}
`,
	})

	cfg := load(t, &tfpath.Options{Dir: dir})

	assert.JSONEq(t, `{
		"x.two[0]": {"i": 0},
		"x.two[1]": {"i": 1},
		"x.string_count[0]": {"i": 0},
		"x.fraction": {"i": "${count.index}"},
		"x.not_number": {"i": "${count.index}"},
		"x.null": {"i": "${count.index}"},
		"x.unknown": {"i": "${count.index}"}
	}`, query(t, cfg, "x.*"))
}

func TestForEachExpansion(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.tf": `
resource "x" "set" {
  for_each = toset(["b", "a"])
  k        = each.key
  v        = each.value
}

resource "x" "map" {
  for_each = { one = 1, two = 2 }
  k        = each.key
  v        = each.value
}

resource "x" "objects" {
  for_each = { a = { size = 1 } }
  size     = each.value.size
}

resource "x" "number_set" {
  for_each = toset([1, 2])
  k        = each.key
}

resource "x" "list" {
  for_each = ["a"]
  k        = each.key
}

resource "x" "unknown" {
  for_each = aws_x.y.tags
  k        = each.key
  v        = each.value
}

resource "x" "empty" {
  for_each = {}
}

resource "x" "non_string_keys" {
  for_each = toset([["a"]])
  k        = each.key
}
`,
	})

	cfg := load(t, &tfpath.Options{Dir: dir})

	assert.JSONEq(t, `{
		"x.set[\"a\"]": {"k": "a", "v": "a"},
		"x.set[\"b\"]": {"k": "b", "v": "b"},
		"x.map[\"one\"]": {"k": "one", "v": 1},
		"x.map[\"two\"]": {"k": "two", "v": 2},
		"x.objects[\"a\"]": {"size": 1},
		"x.number_set[\"1\"]": {"k": "1"},
		"x.number_set[\"2\"]": {"k": "2"},
		"x.list": {"k": "${each.key}"},
		"x.unknown": {"k": "${each.key}", "v": "${each.value}"},
		"x.non_string_keys": {"k": "${each.key}"}
	}`, query(t, cfg, "x.*"))
}

func TestBlockRendering(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.tf": `
resource "x" "y" {
  count      = 1
  depends_on = [aws_x.z]

  lifecycle {
    ignore_changes = [tags]
  }

  nested {
    a = 1
    inner {
      b = 2
    }
  }

  nested {
    a = 2
  }

  provisioner "local-exec" {
    command = "echo"
  }

  labeled "a" "b" {
    c = 3
  }

  dynamic "map_iter" {
    for_each = { k1 = "v1" }
    content {
      key   = map_iter.key
      value = map_iter.value
    }
  }

  dynamic "outer" {
    for_each = [1, 2]
    iterator = o
    content {
      n = o.value
      dynamic "inner" {
        for_each = [o.value * 10]
        content {
          m = inner.value
        }
      }
    }
  }

  dynamic "no_content" {
    for_each = [1]
  }

  dynamic "no_for_each" {
    content {
      v = no_for_each.value
    }
  }
}
`,
	})

	cfg := load(t, &tfpath.Options{Dir: dir})

	assert.JSONEq(t, `{
		"x.y[0]": {
			"depends_on": ["${aws_x.z}"],
			"lifecycle": [{"ignore_changes": ["${tags}"]}],
			"nested": [{"a": 1, "inner": [{"b": 2}]}, {"a": 2}],
			"provisioner": [{"local-exec": {"command": "echo"}}],
			"labeled": [{"a": {"b": {"c": 3}}}],
			"map_iter": [{"key": "k1", "value": "v1"}],
			"outer": [{"n": 1, "inner": [{"m": 10}]}, {"n": 2, "inner": [{"m": 20}]}],
			"no_for_each": [{"v": "${no_for_each.value}"}]
		}
	}`, query(t, cfg, "x.y"))
}

func TestModuleExpansion(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.tf": `
module "zero" {
  source = "./m"
  count  = 0
  name   = "zero"
}

module "unknown_count" {
  source = "./m"
  count  = length(aws_x.y.ids)
  name   = "c${count.index}"
}

module "unknown_for_each" {
  source   = "./m"
  for_each = aws_x.y.tags
  name     = each.key
}

module "set" {
  source   = "./m"
  for_each = toset(["a"])
  name     = each.value
}

module "unknown_arg" {
  source = "./m"
  name   = aws_x.y.name
}

locals {
  zero_names          = module.zero[*].name
  unknown_count_names = module.unknown_count[*].name
  unknown_each        = module.unknown_for_each["a"].name
  set_name            = module.set["a"].name
  whole_set           = module.set
  arn                 = module.set["a"].arn
}
`,
		"m/main.tf": `
variable "name" {}

resource "r" "this" {
  name = var.name
}

output "name" {
  value = var.name
}

output "arn" {
  value = r.this.arn
}
`,
	})

	cfg := load(t, &tfpath.Options{Dir: dir})

	assert.JSONEq(t, `{
		"local.zero_names": [],
		"local.unknown_count_names": "${module.unknown_count[*].name}",
		"local.unknown_each": "${module.unknown_for_each[\"a\"].name}",
		"local.set_name": "a",
		"local.whole_set": "${module.set}",
		"local.arn": "${module.set[\"a\"].arn}",
		"module.unknown_count.r.this.name": "${var.name}",
		"module.unknown_count.var.name": "c${count.index}",
		"module.unknown_for_each.r.this.name": "${var.name}",
		"module.unknown_for_each.var.name": "${each.key}",
		"module.set[\"a\"].r.this.name": "a",
		"module.set[\"a\"].var.name": "a",
		"module.unknown_arg.var.name": "${aws_x.y.name}",
		"module.unknown_arg.r.this.name": "${var.name}"
	}`, query(t, cfg, "local.*", "module.*.r.this.name", "module.*.var.name"))
}

func TestModuleOutputChains(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.tf": `
module "a" {
  source = "./a"
  in     = "x"
}

module "b" {
  source = "./b"
  in     = module.a.out
}

locals {
  result = module.b.out
}
`,
		"a/main.tf": `
variable "in" {}

module "inner" {
  source = "./inner"
  in     = "${var.in}-inner"
}

output "out" {
  value = module.inner.out
}
`,
		"a/inner/main.tf": `
variable "in" {}

output "out" {
  value = upper(var.in)
}
`,
		"b/main.tf": `
variable "in" {}

output "out" {
  value = "${var.in}!"
}
`,
	})

	cfg := load(t, &tfpath.Options{Dir: dir})

	assert.JSONEq(t, `{
		"local.result": "X-INNER!",
		"module.b.var.in": "X-INNER",
		"module.a.module.inner.output.out": "X-INNER"
	}`, query(t, cfg, "local.result", "module.b.var.in", "module.a.module.inner.output.out"))
}

func TestModuleOutputCycle(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.tf": `
module "a" {
  source = "./m"
  in     = module.b.out
}

module "b" {
  source = "./m"
  in     = module.a.out
}
`,
		"m/main.tf": `
variable "in" {}

output "out" {
  value = var.in
}
`,
	})

	cfg := load(t, &tfpath.Options{Dir: dir})

	assert.JSONEq(t, `{
		"module.a.output.out": "${var.in}",
		"module.b.output.out": "${var.in}"
	}`, query(t, cfg, "module.*.output.out"))
}
