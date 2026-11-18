package tfpath

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/ext/typeexpr"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

// maxModuleDepth guards against modules that (indirectly) call themselves.
const maxModuleDepth = 64

// Config is a loaded Terraform configuration: the root module and every
// module it calls, resolved recursively.
type Config struct {
	rootDir     string
	workspace   string
	root        *instance
	sources     map[string][]byte
	modulesJSON map[string]string
	inputs      map[string]cty.Value
	cliVars     map[string]string
	funcs       map[string]function.Function
	warnings    []string
}

type module struct {
	dir       string
	key       string
	variables map[string]*variable
	locals    map[string]*hclsyntax.Attribute
	outputs   map[string]hcl.Expression
	resources []*resource
	calls     map[string]*moduleCall
}

type variable struct {
	name     string
	def      hcl.Expression
	typ      cty.Type
	defaults *typeexpr.Defaults
}

type resource struct {
	data  bool
	typ   string
	name  string
	block *hclsyntax.Block
}

func (r *resource) addr() string {
	if r.data {
		return "data." + r.typ + "." + r.name
	}
	return r.typ + "." + r.name
}

type moduleCall struct {
	name  string
	block *hclsyntax.Block
	mod   *module
}

// Load parses the root module in opts.Dir and every module it calls.
//
// Module sources are resolved through .terraform/modules/modules.json when
// it exists (i.e. after terraform init). Otherwise only local paths such as
// "./modules/web" are followed. Calls that cannot be resolved are reported
// by Warnings and are treated as unknown.
func Load(opts *Options) (*Config, error) {
	if opts == nil {
		opts = &Options{}
	}

	dir := opts.Dir
	if dir == "" {
		dir = "."
	}

	rootDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	ws := opts.Workspace
	if ws == "" {
		ws = "default"
	}

	c := &Config{
		rootDir:   rootDir,
		workspace: ws,
		sources:   map[string][]byte{},
		inputs:    map[string]cty.Value{},
		cliVars:   opts.Vars,
		funcs:     functions(rootDir),
	}

	if c.modulesJSON, err = loadModulesJSON(rootDir); err != nil {
		return nil, err
	}

	mod, err := c.loadModule(rootDir, "", 0)
	if err != nil {
		return nil, err
	}

	varFiles, err := autoVarFiles(rootDir)
	if err != nil {
		return nil, err
	}

	for _, f := range append(varFiles, opts.VarFiles...) {
		if err := c.loadVarFile(f); err != nil {
			return nil, err
		}
	}

	c.root = newInstance(c, mod, "", cty.NilVal, nil)

	return c, nil
}

// Warnings returns problems that did not prevent loading, such as module
// calls whose source could not be resolved.
func (c *Config) Warnings() []string {
	return c.warnings
}

func (c *Config) warnf(format string, args ...any) {
	c.warnings = append(c.warnings, fmt.Sprintf(format, args...))
}

func (c *Config) loadModule(dir, key string, depth int) (*module, error) {
	if depth > maxModuleDepth {
		return nil, fmt.Errorf("module nesting too deep at %s", dir)
	}

	files, err := filepath.Glob(filepath.Join(dir, "*.tf"))
	if err != nil {
		return nil, err
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no .tf files in %s", dir)
	}

	sort.Strings(files)

	m := &module{
		dir:       dir,
		key:       key,
		variables: map[string]*variable{},
		locals:    map[string]*hclsyntax.Attribute{},
		outputs:   map[string]hcl.Expression{},
		calls:     map[string]*moduleCall{},
	}

	var callOrder []*moduleCall
	parser := hclparse.NewParser()

	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}

		file, diags := parser.ParseHCL(src, path)
		if diags.HasErrors() {
			return nil, errors.New(diags.Error())
		}

		c.sources[path] = src
		body := file.Body.(*hclsyntax.Body)

		for _, b := range body.Blocks {
			switch {
			case b.Type == "variable" && len(b.Labels) == 1:
				v, err := parseVariable(b)
				if err != nil {
					return nil, err
				}
				m.variables[v.name] = v
			case b.Type == "locals":
				maps.Copy(m.locals, b.Body.Attributes)
			case b.Type == "output" && len(b.Labels) == 1:
				if a, ok := b.Body.Attributes["value"]; ok {
					m.outputs[b.Labels[0]] = a.Expr
				}
			case (b.Type == "resource" || b.Type == "data") && len(b.Labels) == 2:
				m.resources = append(m.resources, &resource{
					data:  b.Type == "data",
					typ:   b.Labels[0],
					name:  b.Labels[1],
					block: b,
				})
			case b.Type == "module" && len(b.Labels) == 1:
				call := &moduleCall{name: b.Labels[0], block: b}
				m.calls[call.name] = call
				callOrder = append(callOrder, call)
			}
		}
	}

	for _, call := range callOrder {
		childKey := call.name
		if key != "" {
			childKey = key + "." + call.name
		}

		childAddr := "module." + strings.ReplaceAll(childKey, ".", ".module.")

		childDir, err := c.resolveSource(m, call, childKey)
		if err != nil {
			c.warnf("%s: %s", childAddr, err)
			continue
		}

		child, err := c.loadModule(childDir, childKey, depth+1)
		if err != nil {
			c.warnf("%s: %s", childAddr, err)
			continue
		}
		call.mod = child
	}

	return m, nil
}

func parseVariable(b *hclsyntax.Block) (*variable, error) {
	v := &variable{name: b.Labels[0], typ: cty.DynamicPseudoType}

	if a, ok := b.Body.Attributes["type"]; ok {
		ty, defaults, diags := typeexpr.TypeConstraintWithDefaults(a.Expr)
		if diags.HasErrors() {
			return nil, errors.New(diags.Error())
		}
		v.typ = ty
		v.defaults = defaults
	}

	if a, ok := b.Body.Attributes["default"]; ok {
		v.def = a.Expr
	}

	return v, nil
}

func (c *Config) resolveSource(parent *module, call *moduleCall, key string) (string, error) {
	if dir, ok := c.modulesJSON[key]; ok {
		return dir, nil
	}

	a, ok := call.block.Body.Attributes["source"]
	if !ok {
		return "", errors.New("missing source")
	}

	v, diags := a.Expr.Value(nil)
	if diags.HasErrors() || v.Type() != cty.String || v.IsNull() {
		return "", errors.New("source must be a literal string")
	}

	src := v.AsString()
	if strings.HasPrefix(src, "./") || strings.HasPrefix(src, "../") {
		return filepath.Join(parent.dir, src), nil
	}

	return "", fmt.Errorf("cannot resolve source %q (run terraform init)", src)
}

func loadModulesJSON(rootDir string) (map[string]string, error) {
	path := filepath.Join(rootDir, ".terraform", "modules", "modules.json")

	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	var mj struct {
		Modules []struct {
			Key string `json:"Key"`
			Dir string `json:"Dir"`
		} `json:"Modules"`
	}

	if err := json.Unmarshal(b, &mj); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	dirs := map[string]string{}

	for _, m := range mj.Modules {
		if m.Key == "" {
			continue
		}

		dir := m.Dir
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(rootDir, dir)
		}
		dirs[m.Key] = dir
	}

	return dirs, nil
}

func autoVarFiles(rootDir string) ([]string, error) {
	var files []string

	for _, name := range []string{"terraform.tfvars", "terraform.tfvars.json"} {
		path := filepath.Join(rootDir, name)
		if _, err := os.Stat(path); err == nil {
			files = append(files, path)
		}
	}

	for _, pat := range []string{"*.auto.tfvars", "*.auto.tfvars.json"} {
		matches, err := filepath.Glob(filepath.Join(rootDir, pat))
		if err != nil {
			return nil, err
		}
		sort.Strings(matches)
		files = append(files, matches...)
	}

	return files, nil
}

func (c *Config) loadVarFile(path string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	parser := hclparse.NewParser()

	var file *hcl.File
	var diags hcl.Diagnostics

	if strings.HasSuffix(path, ".json") {
		file, diags = parser.ParseJSON(src, path)
	} else {
		file, diags = parser.ParseHCL(src, path)
	}

	if diags.HasErrors() {
		return errors.New(diags.Error())
	}

	attrs, diags := file.Body.JustAttributes()
	if diags.HasErrors() {
		return errors.New(diags.Error())
	}

	for name, a := range attrs {
		v, diags := a.Expr.Value(nil)
		if diags.HasErrors() {
			return errors.New(diags.Error())
		}
		c.inputs[name] = v
	}

	return nil
}
