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

func load(t *testing.T, opts *tfpath.Options) *tfpath.Config {
	t.Helper()
	cfg, err := tfpath.Load(opts)
	require.NoError(t, err)
	return cfg
}

func query(t *testing.T, cfg *tfpath.Config, queries ...string) string {
	t.Helper()
	out := map[string]any{}
	for _, q := range queries {
		matches, err := cfg.Query(q)
		require.NoError(t, err)
		for _, m := range matches {
			out[m.Path] = m.Value
		}
	}
	b, err := json.Marshal(out)
	require.NoError(t, err)
	return string(b)
}

func TestResources(t *testing.T) {
	cfg := load(t, &tfpath.Options{Dir: "testdata/basic"})

	assert.JSONEq(t, `{
		"aws_s3_bucket.logs": {"bucket": "dev-app-logs", "tags": {"Env": "dev"}},
		"aws_iam_role_policy_attachment.this": {
			"policy_arn": "arn:aws:iam::aws:policy/${var.suffix}",
			"role": "${aws_iam_role.app.name}"
		}
	}`, query(t, cfg, "aws_s3_bucket.logs", "aws_iam_role_policy_attachment.this"))
}

func TestCount(t *testing.T) {
	cfg := load(t, &tfpath.Options{Dir: "testdata/basic"})

	assert.JSONEq(t, `{
		"aws_subnet.private[0].availability_zone": "ap-northeast-1a",
		"aws_subnet.private[1].availability_zone": "ap-northeast-1c"
	}`, query(t, cfg, "aws_subnet.private.availability_zone"))

	assert.JSONEq(t, `{
		"aws_subnet.private[1].tags": {"Name": "private-1", "Role": "${aws_iam_role.app.name}"}
	}`, query(t, cfg, "aws_subnet.private[1].tags"))
}

func TestForEach(t *testing.T) {
	cfg := load(t, &tfpath.Options{Dir: "testdata/basic"})

	assert.JSONEq(t, `{
		"aws_instance.web[\"a\"].instance_type": "t3.micro",
		"aws_instance.web[\"b\"].instance_type": "t3.small"
	}`, query(t, cfg, "aws_instance.web[*].instance_type"))

	assert.JSONEq(t, `{
		"aws_instance.web[\"b\"].root_block_device[0].volume_size": 8080
	}`, query(t, cfg, `aws_instance.web["b"].root_block_device[0].volume_size`))
}

func TestBlocks(t *testing.T) {
	cfg := load(t, &tfpath.Options{Dir: "testdata/basic"})

	assert.JSONEq(t, `{
		"aws_security_group.sg": {
			"name": "dev-app",
			"ingress": [{"from_port": 80, "to_port": 80}, {"from_port": 443, "to_port": 443}],
			"egress": [{"from_port": 0}]
		},
		"data.aws_iam_policy_document.p.statement[0].resources": ["${aws_s3_bucket.logs.arn}/*"]
	}`, query(t, cfg, "aws_security_group.sg", "data.*.p.statement[0].resources"))
}

func TestValues(t *testing.T) {
	cfg := load(t, &tfpath.Options{Dir: "testdata/basic"})

	assert.JSONEq(t, `{
		"var.env": "dev",
		"var.port": 8080,
		"var.suffix": "${var.suffix}",
		"var.tags": {},
		"local.name": "dev-app",
		"local.upper_name": "DEV-APP",
		"local.common_tags": {"Env": "dev"},
		"local.zones": ["a", "c"],
		"output.bucket_arn": "${aws_s3_bucket.logs.arn}",
		"output.name": "dev-app"
	}`, query(t, cfg, "var.*", "local.*", "output.*"))
}

func TestVarFilesAndVars(t *testing.T) {
	cfg := load(t, &tfpath.Options{
		Dir:      "testdata/basic",
		VarFiles: []string{"testdata/basic/prod.tfvars"},
		Vars:     map[string]string{"suffix": "ReadOnly", "port": "443"},
	})

	assert.JSONEq(t, `{
		"aws_s3_bucket.logs": {"bucket": "prod-app-logs", "tags": {"Env": "prod", "Team": "infra"}},
		"aws_iam_role_policy_attachment.this.policy_arn": "arn:aws:iam::aws:policy/ReadOnly",
		"var.port": 443
	}`, query(t, cfg, "aws_s3_bucket.logs", "aws_iam_role_policy_attachment.this.policy_arn", "var.port"))
}

func TestModules(t *testing.T) {
	cfg := load(t, &tfpath.Options{Dir: "testdata/modules"})
	assert.Empty(t, cfg.Warnings())

	assert.JSONEq(t, `{
		"module.web.aws_s3_bucket.this.bucket": "acme-web",
		"module.svc[\"api\"].aws_sqs_queue.q": {"name": "acme-api"},
		"module.svc[\"api\"].var.size": 1,
		"module.svc[\"batch\"].var.size": 2,
		"module.web.module.inner.null_resource.n.triggers.x": "ACME-WEB",
		"aws_s3_bucket_policy.p": {"bucket": "acme-web", "policy": "${module.web.arn}"}
	}`, query(t, cfg,
		"module.*.aws_s3_bucket.*.bucket",
		`module.svc["api"].aws_sqs_queue.q`,
		"module.svc.var.size",
		"**.null_resource.*.triggers.x",
		"aws_s3_bucket_policy.p",
	))
}

func TestDeepMatchesRoot(t *testing.T) {
	cfg := load(t, &tfpath.Options{Dir: "testdata/modules"})

	assert.JSONEq(t, `{
		"aws_s3_bucket_policy.p.bucket": "acme-web",
		"module.web.aws_s3_bucket.this": {"bucket": "acme-web"}
	}`, query(t, cfg, "**.aws_s3_bucket_policy.p.bucket", "**.aws_s3_bucket.this"))
}

func TestModulesJSON(t *testing.T) {
	cfg := load(t, &tfpath.Options{Dir: "testdata/modules_json"})

	assert.Equal(t, []string{
		`module.missing: cannot resolve source "git::https://example.com/missing.git" (run terraform init)`,
	}, cfg.Warnings())

	assert.JSONEq(t, `{
		"module.remote.aws_sns_topic.t": {"name": "from-registry"}
	}`, query(t, cfg, "**.*.*", "module.missing.*.*"))
}

func TestUnknownExpansion(t *testing.T) {
	cfg := load(t, &tfpath.Options{Dir: "testdata/edge"})

	assert.JSONEq(t, `{
		"aws_instance.x": {"ami": "${each.key}"},
		"aws_instance.y": {"ami": "ami-${count.index}"},
		"aws_instance.z.ebs": [{"name": "${d.value}"}]
	}`, query(t, cfg, "aws_instance.x", "aws_instance.y", "aws_instance.z.ebs", "aws_instance.x[0]"))
}

func TestComplexVars(t *testing.T) {
	cfg := load(t, &tfpath.Options{
		Dir:      "testdata/edge",
		VarFiles: []string{"testdata/edge/edge.tfvars.json"},
		Vars:     map[string]string{"conf": "{size = 3}"},
	})

	assert.JSONEq(t, `{
		"aws_instance.x[\"n1\"].ami": "n1",
		"aws_instance.x[\"n2\"].ami": "n2",
		"aws_instance.z.ebs": [{"name": "n1"}, {"name": "n2"}],
		"aws_instance.z.provisioner[0]": {"local-exec": {"command": "echo"}},
		"var.conf": {"size": 3, "zone": "a"},
		"output.conf": {"size": 3, "zone": "a"}
	}`, query(t, cfg, "aws_instance.x.ami", "aws_instance.z.ebs", "aws_instance.z.provisioner[0]", "var.conf", "output.conf"))
}

func TestFunctionsAndCycles(t *testing.T) {
	cfg := load(t, &tfpath.Options{Dir: "testdata/edge", Workspace: "stg"})

	assert.JSONEq(t, `{
		"local.a": "${local.b}",
		"local.b": "${local.a}",
		"local.enc": "aGk=",
		"local.rep": "a_b_c",
		"local.hash": "9dd4e461268c8034f5c8564e155c67a6",
		"local.file": "hello",
		"local.has": true,
		"local.ws": "stg",
		"local.pfx": true,
		"local.tpl": "${templatefile(\"x.tpl\", {})}"
	}`, query(t, cfg, "local.a", "local.b", "local.enc", "local.rep", "local.hash", "local.file", "local.has", "local.ws", "local.pfx", "local.tpl"))
}

func TestDeepInValue(t *testing.T) {
	cfg := load(t, &tfpath.Options{Dir: "testdata/basic"})

	assert.JSONEq(t, `{
		"aws_security_group.sg.egress[0].from_port": 0,
		"aws_security_group.sg.ingress[0].from_port": 80,
		"aws_security_group.sg.ingress[1].from_port": 443
	}`, query(t, cfg, "aws_security_group.sg.**.from_port"))
}

func TestNonIdentifierKeys(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
resource "x" "y" {
  tags = { "kubernetes.io/role" = "elb" }
}
`), 0o644))

	cfg := load(t, &tfpath.Options{Dir: dir})

	assert.JSONEq(t, `{
		"x.y.tags[\"kubernetes.io/role\"]": "elb"
	}`, query(t, cfg, "x.y.tags.*", `x.y.tags["kubernetes.io/role"]`))
}

func TestLoadErrors(t *testing.T) {
	_, err := tfpath.Load(&tfpath.Options{Dir: t.TempDir()})
	require.ErrorContains(t, err, "no .tf files")

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`resource "x" {`), 0o644))
	_, err = tfpath.Load(&tfpath.Options{Dir: dir})
	require.Error(t, err)

	_, err = tfpath.Load(&tfpath.Options{Dir: "testdata/basic", VarFiles: []string{"testdata/nope.tfvars"}})
	require.Error(t, err)
}

func TestParseQuery(t *testing.T) {
	for _, q := range []string{
		`module.web["a"].aws_s3_bucket.this[0].tags["Name"]`,
		`module.*.**.x.y[*]`,
		`var.x`,
	} {
		parsed, err := tfpath.ParseQuery(q)
		require.NoError(t, err, q)
		assert.Equal(t, q, parsed.String())
	}

	for _, q := range []string{
		``,
		`.x`,
		`x..y`,
		`x.y[`,
		`x.y[abc]`,
		`x.y["a]`,
		`x.a*`,
		`[0]`,
		`x y`,
	} {
		_, err := tfpath.ParseQuery(q)
		require.Error(t, err, q)
	}
}

func TestModuleOutputs(t *testing.T) {
	cfg := load(t, &tfpath.Options{Dir: "testdata/modules"})

	assert.JSONEq(t, `{
		"local.api_label": "acme-api:1",
		"local.cnt_label": "cnt1:1",
		"local.cnt_labels": ["cnt0:1", "cnt1:1"],
		"local.path_module": "modules/web",
		"local.prefix": "acme",
		"local.svc_labels": {"api": "acme-api:1", "batch": "acme-batch:2"},
		"local.web_all": "${module.web}",
		"module.cnt[0].aws_sqs_queue.q.name": "cnt0",
		"module.cnt[1].output.label": "cnt1:1"
	}`, query(t, cfg, "local.*", "module.cnt[0].aws_sqs_queue.q.name", "module.cnt[1].output.label"))
}

func TestMoreFunctions(t *testing.T) {
	cfg := load(t, &tfpath.Options{Dir: "testdata/edge"})

	assert.JSONEq(t, `{
		"local.bad": "${base64decode(\"!!\")}",
		"local.dec": "hi",
		"local.ends": true,
		"local.incl": false,
		"local.nofile": "${file(\"nope.txt\")}",
		"local.rep2": "a+b",
		"local.sha1": "11f6ad8ec52a2984abaafd7c3b516503785c2072",
		"local.sha2": "2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881"
	}`, query(t, cfg, "local.bad", "local.dec", "local.ends", "local.incl", "local.nofile", "local.rep2", "local.sha1", "local.sha2"))
}

func TestBrokenInputs(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`variable "x" {}`), 0o644))

	bad := filepath.Join(dir, "bad.tfvars")
	require.NoError(t, os.WriteFile(bad, []byte(`x = `), 0o644))
	_, err := tfpath.Load(&tfpath.Options{Dir: dir, VarFiles: []string{bad}})
	require.Error(t, err)

	ref := filepath.Join(dir, "ref.tfvars")
	require.NoError(t, os.WriteFile(ref, []byte(`x = var.y`), 0o644))
	_, err = tfpath.Load(&tfpath.Options{Dir: dir, VarFiles: []string{ref}})
	require.Error(t, err)

	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".terraform", "modules"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".terraform", "modules", "modules.json"), []byte(`{`), 0o644))
	_, err = tfpath.Load(&tfpath.Options{Dir: dir})
	require.ErrorContains(t, err, "modules.json")
}

func TestQueryError(t *testing.T) {
	cfg := load(t, &tfpath.Options{Dir: "testdata/basic"})
	_, err := cfg.Query("x..y")
	require.Error(t, err)
}
