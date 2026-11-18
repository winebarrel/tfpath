# tfpath

[![CI](https://github.com/winebarrel/tfpath/actions/workflows/ci.yml/badge.svg)](https://github.com/winebarrel/tfpath/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/winebarrel/tfpath.svg)](https://pkg.go.dev/github.com/winebarrel/tfpath)
[![codecov](https://codecov.io/gh/winebarrel/tfpath/branch/main/graph/badge.svg)](https://codecov.io/gh/winebarrel/tfpath)
[![AI Generated](https://img.shields.io/badge/AI%20Generated-Claude-orange?logo=anthropic)](https://claude.com/product/claude-code)

`tfpath` reads Terraform `.tf` files, including every module they call, and prints the values matching a query as JSON. Variables, locals, `count` / `for_each` and built-in functions are evaluated as far as the configuration alone allows; values only known after apply (resource attributes, data sources, ...) are kept as their source expression.

It does not need providers, credentials or state.

## Installation

Download a binary from the [releases page](https://github.com/winebarrel/tfpath/releases/latest), or:

```
go install github.com/winebarrel/tfpath/cmd/tfpath@latest
```

## Usage

```
Usage: tfpath <queries> ... [flags]

Arguments:
  <queries> ...    Queries such as 'module.*.aws_s3_bucket.*.bucket'.

Flags:
  -h, --help              Show help.
  -C, --dir="."           Root module directory.
      --var-file=FILE     Load variable values from a .tfvars(.json) file
                          (repeatable).
      --var=NAME=VALUE    Set a root module variable (repeatable).
  -c, --compact           Print compact JSON.
      --version
```

## Example

```hcl
# main.tf
variable "env" {
  default = "dev"
}

module "web" {
  source = "./modules/web"
  bucket = "${var.env}-web"
}

module "svc" {
  source   = "./modules/svc"
  for_each = toset(["api", "batch"])
  name     = "${var.env}-${each.key}"
}
```

```hcl
# modules/web/main.tf
variable "bucket" {}

resource "aws_s3_bucket" "this" {
  bucket = var.bucket
  tags = {
    Name = var.bucket
    Role = aws_iam_role.web.name
  }
}
```

```
$ tfpath --var env=prod 'module.*.aws_s3_bucket.*'
{
  "module.web.aws_s3_bucket.this": {
    "bucket": "prod-web",
    "tags": {
      "Name": "prod-web",
      "Role": "${aws_iam_role.web.name}"
    }
  }
}

$ tfpath 'module.svc.aws_sqs_queue.*.name'
{
  "module.svc[\"api\"].aws_sqs_queue.q.name": "dev-api",
  "module.svc[\"batch\"].aws_sqs_queue.q.name": "dev-batch"
}
```

The output is a JSON object keyed by the concrete path of each match. Multiple queries are merged into one object.

## Queries

A query is a Terraform address followed by a path into the value:

| Query | Matches |
| --- | --- |
| `aws_s3_bucket.logs` | a resource (all instances when it has `count` / `for_each`) |
| `aws_subnet.private[0]`, `aws_instance.web["a"]` | one resource instance |
| `data.aws_iam_policy_document.p` | a data source |
| `var.env`, `local.name`, `output.arn` | a variable, local value or output |
| `module.web.<address>` | `<address>` inside a module call (all instances unless a key is given) |
| `aws_s3_bucket.logs.tags.Name` | a path inside a value |
| `aws_security_group.sg.ingress[0].from_port` | nested blocks are lists |
| `x.y.tags["kubernetes.io/role"]` | keys that are not identifiers |

Wildcards:

- `*` / `[*]` matches any name, key or list element (`module.*.aws_s3_bucket.*.bucket`, `aws_instance.web[*].ami`). A leading `*` matches managed resource types only.
- `**` matches any number of module levels in the module path (`**.aws_s3_bucket.*` searches the root module and every module), and any depth inside a value (`aws_security_group.sg.**.from_port`).

## Evaluation

- Root variables come from, in increasing precedence: `default`, `terraform.tfvars(.json)`, `*.auto.tfvars(.json)`, `--var-file`, `--var`. Module variables come from the arguments of the module call. Type constraints, including `optional()` defaults, are applied.
- `local`, `path.*`, `terraform.workspace` (always `default` on the CLI), `count.index`, `each.*`, dynamic block iterators and module outputs are evaluated.
- Most string, collection, encoding, hash and type conversion functions are supported, as are `file` and `fileexists`. Calls to other functions are kept as source text.
- Anything that depends on a resource, a data source or a variable without a value is unknown. An unknown value is printed as its source text, e.g. `"${aws_iam_role.web.name}"`. Object and tuple literals are evaluated element by element, so only the unknown parts are replaced.
- When `count` / `for_each` is unknown, the resource or module is printed once without a key.

## Modules

Module sources are resolved through `.terraform/modules/modules.json` when it exists, so run `terraform init` to follow registry and git modules. Without it, only local paths (`./...`, `../...`) are followed. Module calls that cannot be resolved are reported as warnings on stderr and skipped.

## Library

```go
import "github.com/winebarrel/tfpath"

cfg, err := tfpath.Load(&tfpath.Options{
	Dir:  "path/to/root",
	Vars: map[string]string{"env": "prod"},
})
if err != nil {
	log.Fatal(err)
}

matches, err := cfg.Query("module.*.aws_s3_bucket.*.bucket")
if err != nil {
	log.Fatal(err)
}

for _, m := range matches {
	fmt.Println(m.Path, m.Value)
}
```

`Match.Value` is what `encoding/json` would decode (`map[string]any`, `[]any`, `string`, `json.Number`, `bool`, `nil`). Use `tfpath.ParseQuery` and `Config.Match` to parse a query once and run it against several configurations, and `Config.Warnings` to get unresolved module calls.
