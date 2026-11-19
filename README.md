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
      --data-file=FILE    Load data source values from a JSON file
                          (repeatable).
      --data=ADDR=VALUE   Set a data source attribute to a string, e.g.
                          data.aws_region.current.name=us-east-1 (repeatable).
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
- Terraform's built-in functions are supported, including `templatefile`, the filesystem functions (relative paths are resolved against the root module directory) and `provider::terraform::*`. Functions whose result changes on every run (`timestamp`, `plantimestamp`, `uuid`, `bcrypt`) and provider-defined functions are kept as source text.
- Variables declared with `sensitive = true` / `ephemeral = true` are seen as such by `issensitive` and `ephemeralasnull`. Values are printed in clear text.
- Anything that depends on a resource, a data source without given values (see [Data sources](#data-sources)) or a variable without a value is unknown. An unknown value is printed as its source text, e.g. `"${aws_iam_role.web.name}"`. Object and tuple literals are evaluated element by element, so only the unknown parts are replaced.
- When `count` / `for_each` is unknown, the resource or module is printed once without a key.

## Data sources

Values of data sources can be given with `--data-file` and `--data`, so that expressions referring to them are evaluated.

```json
{
  "data.aws_caller_identity.current": {
    "account_id": "123456789012"
  },
  "**.data.aws_region.current.name": "ap-northeast-1",
  "data.aws_subnet.private[0].id": "subnet-0123"
}
```

```
$ tfpath --data-file data.json --data 'module.web.data.aws_region.current.name=us-east-1' 'aws_s3_bucket.*'
```

- A key is a data source address, optionally followed by an attribute path. The module path can use `*` and `**` as in queries, and an address without a key applies to all instances of a module call or a data source with `count` / `for_each`.
- `--data` values are strings. Use `--data-file` for numbers, lists and objects.
- Attributes that are not given stay unknown. A reference to the whole data source (e.g. `jsonencode(data.x.y)`) sees only the given attributes.
- Sources are applied in order: each `--data-file`, then `--data`, so later ones override earlier ones. Within a source, when several entries set the same attribute, the more specific address wins (fewer wildcards and keyless module calls or instances, then the longer attribute path).
- Given values are used only in references. Querying a data source block still prints its arguments.

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
