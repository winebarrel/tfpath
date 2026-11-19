package tfpath_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/winebarrel/tfpath"
)

func queryOne(t *testing.T, cfg *tfpath.Config, query string) any {
	t.Helper()
	matches, err := cfg.Query(query)
	require.NoError(t, err)
	require.Len(t, matches, 1, query)
	return matches[0].Value
}

const dataMainTF = `
data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

module "web" {
  source = "./modules/web"
}

resource "aws_s3_bucket" "logs" {
  bucket = "logs-${data.aws_caller_identity.current.account_id}-${data.aws_region.current.name}"
  tags = {
    Arn   = data.aws_caller_identity.current.arn
    Owner = try(data.aws_caller_identity.current.user_id, "nobody")
  }
}
`

const dataWebTF = `
data "aws_region" "current" {}

resource "aws_sqs_queue" "q" {
  name = "web-${data.aws_region.current.name}"
}
`

func TestData(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.tf":             dataMainTF,
		"modules/web/main.tf": dataWebTF,
		"data.json": `{
  "data.aws_caller_identity.current": {"account_id": "123456789012"},
  "**.data.aws_region.current": {"name": "ap-northeast-1"}
}`,
	})

	t.Run("not given", func(t *testing.T) {
		cfg := load(t, &tfpath.Options{Dir: dir})
		assert.Equal(t, "logs-${data.aws_caller_identity.current.account_id}-${data.aws_region.current.name}", queryOne(t, cfg, "aws_s3_bucket.logs.bucket"))
	})

	t.Run("data file", func(t *testing.T) {
		cfg := load(t, &tfpath.Options{Dir: dir, DataFiles: []string{filepath.Join(dir, "data.json")}})
		assert.Equal(t, "logs-123456789012-ap-northeast-1", queryOne(t, cfg, "aws_s3_bucket.logs.bucket"))
		assert.Equal(t, "web-ap-northeast-1", queryOne(t, cfg, "module.web.aws_sqs_queue.q.name"))

		// Attributes that were not given are still unknown, even in try().
		assert.Equal(t, map[string]any{
			"Arn":   "${data.aws_caller_identity.current.arn}",
			"Owner": `${try(data.aws_caller_identity.current.user_id, "nobody")}`,
		}, queryOne(t, cfg, "aws_s3_bucket.logs.tags"))
	})

	t.Run("data overrides data file", func(t *testing.T) {
		cfg := load(t, &tfpath.Options{
			Dir:       dir,
			DataFiles: []string{filepath.Join(dir, "data.json")},
			Data: map[string]string{
				"module.web.data.aws_region.current.name": "us-east-1",
				"data.aws_caller_identity.current.arn":    "arn:aws:iam::123456789012:root",
			},
		})
		assert.Equal(t, "logs-123456789012-ap-northeast-1", queryOne(t, cfg, "aws_s3_bucket.logs.bucket"))
		assert.Equal(t, "arn:aws:iam::123456789012:root", queryOne(t, cfg, "aws_s3_bucket.logs.tags.Arn"))
		assert.Equal(t, "web-us-east-1", queryOne(t, cfg, "module.web.aws_sqs_queue.q.name"))
	})
}

func TestDataSpecificWins(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.tf": `
module "a" {
  source = "./modules/web"
}

module "b" {
  source = "./modules/web"
}
`,
		"modules/web/main.tf": dataWebTF,
		"data.json": `{
  "module.a.data.aws_region.current.name": "us-west-2",
  "module.*.data.aws_region.current": {"name": "ap-northeast-1"}
}`,
	})

	cfg := load(t, &tfpath.Options{Dir: dir, DataFiles: []string{filepath.Join(dir, "data.json")}})
	assert.Equal(t, "web-us-west-2", queryOne(t, cfg, "module.a.aws_sqs_queue.q.name"))
	assert.Equal(t, "web-ap-northeast-1", queryOne(t, cfg, "module.b.aws_sqs_queue.q.name"))
}

func TestDataInstances(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.tf": `
data "aws_ami" "by_index" {
  count = 2
}

data "aws_vpc" "by_key" {
  for_each = toset(["dev", "prod"])
}

data "aws_availability_zones" "available" {}

locals {
  amis    = [data.aws_ami.by_index[0].id, data.aws_ami.by_index[1].id]
  vpcs    = { for k, v in data.aws_vpc.by_key : k => v.cidr_block }
  prod    = data.aws_vpc.by_key["prod"].id
  subnets = data.aws_availability_zones.available.names
}

resource "aws_subnet" "s" {
  count             = length(data.aws_availability_zones.available.names)
  availability_zone = data.aws_availability_zones.available.names[count.index]
}
`,
		"data.json": `{
  "data.aws_ami.by_index.id": "ami-common",
  "data.aws_ami.by_index[1].id": "ami-second",
  "data.aws_vpc.by_key": {"cidr_block": "10.0.0.0/16"},
  "data.aws_vpc.by_key[\"prod\"]": {"cidr_block": "10.1.0.0/16"},
  "data.aws_availability_zones.available.names": ["ap-northeast-1a", "ap-northeast-1c"]
}`,
	})

	cfg := load(t, &tfpath.Options{Dir: dir, DataFiles: []string{filepath.Join(dir, "data.json")}})

	assert.Equal(t, []any{"ami-common", "ami-second"}, queryOne(t, cfg, "local.amis"))
	assert.Equal(t, map[string]any{"dev": "10.0.0.0/16", "prod": "10.1.0.0/16"}, queryOne(t, cfg, "local.vpcs"))
	assert.Equal(t, `${data.aws_vpc.by_key["prod"].id}`, queryOne(t, cfg, "local.prod"))
	assert.Equal(t, []any{"ap-northeast-1a", "ap-northeast-1c"}, queryOne(t, cfg, "local.subnets"))

	// count can depend on a data source.
	matches, err := cfg.Query("aws_subnet.s.availability_zone")
	require.NoError(t, err)
	assert.Equal(t, []tfpath.Match{
		{Path: "aws_subnet.s[0].availability_zone", Value: "ap-northeast-1a"},
		{Path: "aws_subnet.s[1].availability_zone", Value: "ap-northeast-1c"},
	}, matches)
}

func TestDataModuleKeysAndMapKeys(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.tf": `
module "svc" {
  source   = "./modules/svc"
  for_each = toset(["api", "batch"])
}
`,
		"modules/svc/main.tf": `
data "aws_eks_cluster" "this" {}

resource "kubernetes_namespace" "ns" {
  role = data.aws_eks_cluster.this.tags["kubernetes.io/role"]
}
`,
		"data.json": `{
  "module.svc.data.aws_eks_cluster.this.tags[\"kubernetes.io/role\"]": "worker",
  "module.svc[\"api\"].data.aws_eks_cluster.this.tags": {"kubernetes.io/role": "api"}
}`,
	})

	cfg := load(t, &tfpath.Options{Dir: dir, DataFiles: []string{filepath.Join(dir, "data.json")}})
	assert.Equal(t, "api", queryOne(t, cfg, `module.svc["api"].kubernetes_namespace.ns.role`))
	assert.Equal(t, "worker", queryOne(t, cfg, `module.svc["batch"].kubernetes_namespace.ns.role`))
}

func TestDataPartialInstances(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.tf": `
variable "create" {
  type = bool
}

data "aws_ami" "by_index" {
  count = 2
}

data "aws_vpc" "maybe" {
  count = var.create ? 1 : 0
}

locals {
  first  = data.aws_ami.by_index[0].id
  second = data.aws_ami.by_index[1].id
  vpc    = data.aws_vpc.maybe[0].id
}
`,
	})

	cfg := load(t, &tfpath.Options{Dir: dir, Data: map[string]string{
		"data.aws_ami.by_index[1].id": "ami-second",
		"data.aws_vpc.maybe.id":       "vpc-1",
	}})

	assert.Equal(t, "${data.aws_ami.by_index[0].id}", queryOne(t, cfg, "local.first"))
	assert.Equal(t, "ami-second", queryOne(t, cfg, "local.second"))
	// The number of instances is unknown, so the given value is not used.
	assert.Equal(t, "${data.aws_vpc.maybe[0].id}", queryOne(t, cfg, "local.vpc"))
}

func TestDataErrors(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.tf":     `data "aws_region" "current" {}`,
		"broken.json": `{"data.aws_region.current": `,
		"list.json":   `[]`,
		"badkey.json": `{"aws_region.current": {}}`,
	})

	tests := []struct {
		name string
		opts *tfpath.Options
		err  string
	}{
		{"missing file", &tfpath.Options{DataFiles: []string{filepath.Join(dir, "nope.json")}}, "nope.json"},
		{"broken file", &tfpath.Options{DataFiles: []string{filepath.Join(dir, "broken.json")}}, "broken.json"},
		{"not an object", &tfpath.Options{DataFiles: []string{filepath.Join(dir, "list.json")}}, "list.json"},
		{"invalid key in file", &tfpath.Options{DataFiles: []string{filepath.Join(dir, "badkey.json")}}, "badkey.json: invalid data source address"},
		{"managed resource", &tfpath.Options{Data: map[string]string{"aws_s3_bucket.b.bucket": "x"}}, "expected data.<type>.<name>"},
		{"no name", &tfpath.Options{Data: map[string]string{"data.aws_region": "x"}}, "expected data.<type>.<name>"},
		{"module without name", &tfpath.Options{Data: map[string]string{"module": "x"}}, "expected module name"},
		{"wildcard in attribute", &tfpath.Options{Data: map[string]string{"data.aws_region.current.*": "x"}}, "must not contain wildcards"},
		{"list index in attribute", &tfpath.Options{Data: map[string]string{"data.aws_region.current.names[0]": "x"}}, "must not contain wildcards or list indexes"},
		{"invalid syntax", &tfpath.Options{Data: map[string]string{"data.aws_region.current[": "x"}}, "invalid query"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.opts.Dir = dir
			_, err := tfpath.Load(tt.opts)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.err)
		})
	}
}
