package tfpath_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/winebarrel/tfpath"
)

func TestLoadDefaultsToCurrentDir(t *testing.T) {
	t.Chdir("testdata/basic")

	for _, opts := range []*tfpath.Options{nil, {}} {
		cfg := load(t, opts)
		assert.JSONEq(t, `{"local.name": "dev-app"}`, query(t, cfg, "local.name"))
	}
}

func TestVariablePrecedence(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.tf": `
variable "v1" { default = "default" }
variable "v2" { default = "default" }
variable "v3" { default = "default" }
variable "v4" { default = "default" }
variable "v5" { default = "default" }
variable "v6" { default = "default" }
variable "v7" { default = "default" }
variable "v8" { default = "default" }
`,
		"terraform.tfvars":         `v2 = "terraform.tfvars"` + "\n" + `v3 = "terraform.tfvars"` + "\n" + `v4 = "terraform.tfvars"` + "\n" + `v5 = "terraform.tfvars"` + "\n" + `v6 = "terraform.tfvars"` + "\n" + `v7 = "terraform.tfvars"` + "\n" + `v8 = "terraform.tfvars"`,
		"terraform.tfvars.json":    `{"v3": "terraform.tfvars.json", "v4": "terraform.tfvars.json", "v5": "terraform.tfvars.json", "v6": "terraform.tfvars.json", "v7": "terraform.tfvars.json", "v8": "terraform.tfvars.json"}`,
		"a.auto.tfvars":            `v4 = "a.auto.tfvars"` + "\n" + `v5 = "a.auto.tfvars"` + "\n" + `v6 = "a.auto.tfvars"` + "\n" + `v7 = "a.auto.tfvars"` + "\n" + `v8 = "a.auto.tfvars"`,
		"b.auto.tfvars.json":       `{"v5": "b.auto.tfvars.json", "v6": "b.auto.tfvars.json", "v7": "b.auto.tfvars.json", "v8": "b.auto.tfvars.json"}`,
		"vars/first.tfvars":        `v6 = "first.tfvars"` + "\n" + `v7 = "first.tfvars"` + "\n" + `v8 = "first.tfvars"`,
		"vars/second.tfvars.json":  `{"v7": "second.tfvars.json", "v8": "second.tfvars.json"}`,
		"not-auto.tfvars":          `v1 = "not loaded"`,
		"vars/ignored.auto.tfvars": `v1 = "not loaded"`,
	})

	cfg := load(t, &tfpath.Options{
		Dir: dir,
		VarFiles: []string{
			filepath.Join(dir, "vars/first.tfvars"),
			filepath.Join(dir, "vars/second.tfvars.json"),
		},
		Vars: map[string]string{"v8": "-var"},
	})

	assert.JSONEq(t, `{
		"var.v1": "default",
		"var.v2": "terraform.tfvars",
		"var.v3": "terraform.tfvars.json",
		"var.v4": "a.auto.tfvars",
		"var.v5": "b.auto.tfvars.json",
		"var.v6": "first.tfvars",
		"var.v7": "second.tfvars.json",
		"var.v8": "-var"
	}`, query(t, cfg, "var.*"))
}

func TestAutoVarFilesOrder(t *testing.T) {
	// *.auto.tfvars and *.auto.tfvars.json are loaded together in lexical
	// order of their file names, so b.auto.tfvars wins over
	// a.auto.tfvars.json.
	dir := writeFiles(t, map[string]string{
		"main.tf": `
variable "x" {}
variable "y" {}
`,
		"a.auto.tfvars.json": `{"x": "a.auto.tfvars.json", "y": "a.auto.tfvars.json"}`,
		"b.auto.tfvars":      `x = "b.auto.tfvars"`,
	})

	cfg := load(t, &tfpath.Options{Dir: dir})

	assert.JSONEq(t, `{
		"var.x": "b.auto.tfvars",
		"var.y": "a.auto.tfvars.json"
	}`, query(t, cfg, "var.*"))
}

func TestDirectoriesNamedTf(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.tf": `
module "only_dirs" {
  source = "./only_dirs"
}
`,
		"dir.tf/main.tf":            `resource "inside" "dir" {}`,
		"only_dirs/nested.tf/x.txt": "",
	})

	cfg := load(t, &tfpath.Options{Dir: dir})

	assert.Equal(t, []string{"module.only_dirs: no .tf files in " + filepath.Join(dir, "only_dirs")}, cfg.Warnings())
	assert.JSONEq(t, `{}`, query(t, cfg, "**.*.*"))
}

func TestVariableTypes(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.tf": `
variable "num" { type = number }
variable "flag" { type = bool }
variable "list" { type = list(number) }
variable "untyped" {}
variable "bad_expr" { type = list(string) }
variable "bad_eval" { type = list(string) }
variable "mismatch" { type = number }
variable "obj" {
  type = object({
    name = string
    tags = optional(map(string), {})
  })
}
variable "no_value" { type = string }
variable "bad_default" { default = local.x }
`,
	})

	cfg := load(t, &tfpath.Options{
		Dir: dir,
		Vars: map[string]string{
			"num":      "42",
			"flag":     "true",
			"list":     `["1", 2]`,
			"untyped":  "[1, 2]",
			"bad_expr": "[",
			"bad_eval": "[var.x]",
			"mismatch": "abc",
			"obj":      `{name = "n"}`,
		},
	})

	assert.JSONEq(t, `{
		"var.num": 42,
		"var.flag": true,
		"var.list": [1, 2],
		"var.untyped": "[1, 2]",
		"var.bad_expr": "[",
		"var.bad_eval": "[var.x]",
		"var.mismatch": "abc",
		"var.obj": {"name": "n", "tags": {}},
		"var.no_value": "${var.no_value}",
		"var.bad_default": "${var.bad_default}"
	}`, query(t, cfg, "var.*"))
}

func TestWorkspace(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.tf": `locals { ws = terraform.workspace }`,
	})

	assert.JSONEq(t, `{"local.ws": "default"}`, query(t, load(t, &tfpath.Options{Dir: dir}), "local.ws"))
	assert.JSONEq(t, `{"local.ws": "prod"}`, query(t, load(t, &tfpath.Options{Dir: dir, Workspace: "prod"}), "local.ws"))
}

func TestIgnoredFilesAndBlocks(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.tf": `
terraform {
  required_version = ">= 1.0"
}

provider "aws" {
  region = "ap-northeast-1"
}

moved {
  from = a.b
  to   = a.c
}

output "no_value" {
  description = "ignored because it has no value"
}

resource "a" "b" {
  x = 1
}
`,
		"extra.tf.json":    `{"resource": {"j": {"k": {"x": 1}}}}`,
		"sub/ignored.tf":   `resource "s" "t" {}`,
		"main.tf.disabled": `resource "d" "e" {}`,
	})

	cfg := load(t, &tfpath.Options{Dir: dir})

	assert.JSONEq(t, `{"a.b": {"x": 1}}`, query(t, cfg, "*.*", "output.*", "provider.*", "moved.*"))
}

func TestModuleResolutionWarnings(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.tf": `
module "no_source" {}

module "dynamic_source" {
  source = "./${var.x}"
}

module "registry" {
  source = "acme/foo/aws"
}

module "empty" {
  source = "./empty"
}

module "broken" {
  source = "./broken"
}

module "ok" {
  source = "./ok"
}

locals {
  from_unresolved = module.registry.out
}
`,
		"empty/README.md": "no tf files",
		"broken/main.tf":  `resource "x" {`,
		"ok/main.tf":      `resource "a" "b" { x = 1 }`,
	})

	cfg := load(t, &tfpath.Options{Dir: dir})

	warnings := cfg.Warnings()
	require.Len(t, warnings, 5)
	assert.Contains(t, warnings, "module.no_source: missing source")
	assert.Contains(t, warnings, "module.dynamic_source: source must be a literal string")
	assert.Contains(t, warnings, `module.registry: cannot resolve source "acme/foo/aws" (run terraform init)`)
	assert.Contains(t, warnings, "module.empty: no .tf files in "+filepath.Join(dir, "empty"))
	assert.Contains(t, warnings[4], "module.broken: ")

	assert.JSONEq(t, `{
		"module.ok.a.b": {"x": 1},
		"local.from_unresolved": "${module.registry.out}"
	}`, query(t, cfg, "module.*.*.*", "local.from_unresolved"))
}

func TestModuleRecursion(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.tf": `
module "self" {
  source = "./"
}
`,
	})

	cfg := load(t, &tfpath.Options{Dir: dir})

	require.Len(t, cfg.Warnings(), 1)
	assert.Contains(t, cfg.Warnings()[0], "module nesting too deep")
}

func TestModulesJSONResolution(t *testing.T) {
	remote := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(remote, "main.tf"), []byte(`
variable "name" {}

module "child" {
  source = "./child"
  name   = "${var.name}-child"
}
`), 0o644))

	dir := writeFiles(t, map[string]string{
		"main.tf": `
module "remote" {
  source = "acme/remote/aws"
  name   = "r"
}

module "local" {
  source = "./not-used"
}
`,
		// The nested registry module is resolved by its key, not by its
		// relative source; "local" resolves to a different directory than
		// its source says.
		".terraform/modules/modules.json": `{"Modules": [
			{"Key": "", "Source": "", "Dir": "."},
			{"Key": "remote", "Source": "acme/remote/aws", "Dir": "` + remote + `"},
			{"Key": "remote.child", "Source": "./child", "Dir": ".terraform/modules/remote.child"},
			{"Key": "local", "Source": "./not-used", "Dir": "real"}
		]}`,
		".terraform/modules/remote.child/main.tf": `
variable "name" {}
resource "x" "y" { name = var.name }
`,
		"real/main.tf": `resource "x" "z" {}`,
	})

	cfg := load(t, &tfpath.Options{Dir: dir})
	assert.Empty(t, cfg.Warnings())

	assert.JSONEq(t, `{
		"module.remote.module.child.x.y": {"name": "r-child"},
		"module.local.x.z": {}
	}`, query(t, cfg, "**.x.*"))
}

func TestLoadFailures(t *testing.T) {
	t.Run("invalid variable type", func(t *testing.T) {
		dir := writeFiles(t, map[string]string{"main.tf": `variable "x" { type = notatype }`})
		_, err := tfpath.Load(&tfpath.Options{Dir: dir})
		require.Error(t, err)
	})

	t.Run("invalid auto tfvars", func(t *testing.T) {
		dir := writeFiles(t, map[string]string{
			"main.tf":          `variable "x" {}`,
			"terraform.tfvars": `x = `,
		})
		_, err := tfpath.Load(&tfpath.Options{Dir: dir})
		require.Error(t, err)
	})

	t.Run("tfvars with a block", func(t *testing.T) {
		dir := writeFiles(t, map[string]string{
			"main.tf":          `variable "x" {}`,
			"terraform.tfvars": `x { }`,
		})
		_, err := tfpath.Load(&tfpath.Options{Dir: dir})
		require.Error(t, err)
	})

	t.Run("invalid tfvars json", func(t *testing.T) {
		dir := writeFiles(t, map[string]string{
			"main.tf":               `variable "x" {}`,
			"terraform.tfvars.json": `{`,
		})
		_, err := tfpath.Load(&tfpath.Options{Dir: dir})
		require.Error(t, err)
	})

	t.Run("unreadable modules.json", func(t *testing.T) {
		dir := writeFiles(t, map[string]string{
			"main.tf": `variable "x" {}`,
		})
		require.NoError(t, os.MkdirAll(filepath.Join(dir, ".terraform", "modules", "modules.json"), 0o755))
		_, err := tfpath.Load(&tfpath.Options{Dir: dir})
		require.Error(t, err)
	})

	t.Run("missing dir", func(t *testing.T) {
		_, err := tfpath.Load(&tfpath.Options{Dir: filepath.Join(t.TempDir(), "nope")})
		require.ErrorContains(t, err, "no .tf files")
	})
}
