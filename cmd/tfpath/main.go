package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/alecthomas/kong"
	"github.com/winebarrel/tfpath"
)

var version string

func init() {
	log.SetFlags(0)
}

type cli struct {
	Queries  []string          `arg:"" help:"Queries such as 'module.*.aws_s3_bucket.*.bucket'."`
	Dir      string            `short:"C" default:"." help:"Root module directory."`
	VarFile  []string          `name:"var-file" sep:"none" placeholder:"FILE" help:"Load variable values from a .tfvars(.json) file (repeatable)."`
	Var      map[string]string `name:"var" mapsep:"none" placeholder:"NAME=VALUE" help:"Set a root module variable (repeatable)."`
	DataFile []string          `name:"data-file" sep:"none" placeholder:"FILE" help:"Load data source values from a JSON file (repeatable)."`
	Data     map[string]string `name:"data" mapsep:"none" placeholder:"ADDR=VALUE" help:"Set a data source attribute to a string, e.g. data.aws_region.current.name=us-east-1 (repeatable)."`
	Compact  bool              `short:"c" help:"Print compact JSON."`
	Version  kong.VersionFlag
}

func main() {
	opts := &cli{}
	parser := kong.Must(opts, kong.Vars{"version": version})
	parser.Model.HelpFlag.Help = "Show help."
	if _, err := parser.Parse(os.Args[1:]); err != nil {
		parser.FatalIfErrorf(err)
	}

	queries := make([]*tfpath.Query, len(opts.Queries))
	for i, s := range opts.Queries {
		q, err := tfpath.ParseQuery(s)
		if err != nil {
			log.Fatal(err)
		}
		queries[i] = q
	}

	cfg, err := tfpath.Load(&tfpath.Options{
		Dir:       opts.Dir,
		VarFiles:  opts.VarFile,
		Vars:      opts.Var,
		DataFiles: opts.DataFile,
		Data:      opts.Data,
	})
	if err != nil {
		log.Fatal(err)
	}

	for _, w := range cfg.Warnings() {
		log.Printf("warning: %s", w)
	}

	out := map[string]any{}
	for _, q := range queries {
		for _, m := range cfg.Match(q) {
			out[m.Path] = m.Value
		}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	if !opts.Compact {
		enc.SetIndent("", "  ")
	}

	if err := enc.Encode(out); err != nil {
		log.Fatal(fmt.Errorf("encode: %w", err))
	}
}
