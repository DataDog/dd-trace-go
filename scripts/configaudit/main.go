// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Command configaudit reports which DD_* environment-variable configurations
// have been migrated to internal/config and which have not.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() {
	var (
		root    = flag.String("root", ".", "repository root")
		format  = flag.String("format", "table", "output format: table or json")
		pkgPref = flag.String("package", "", "restrict output to call sites whose package path (relative to the module root) starts with this prefix")
	)
	flag.Parse()

	if err := run(*root, *format, *pkgPref, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "configaudit:", err)
		os.Exit(1)
	}
}

func run(root, format, pkgPrefix string, out io.Writer) error {
	known, err := loadKnown(filepath.Join(root, "internal", "env", "supported_configurations.json"))
	if err != nil {
		return err
	}
	migrated, err := loadMigrated(filepath.Join(root, "internal", "config"))
	if err != nil {
		return err
	}
	reads, err := scan(root, defaultRecognizers(), defaultExcludes())
	if err != nil {
		return err
	}
	reads = filterByPackage(reads, pkgPrefix)
	res := classify(known, migrated, reads)
	switch format {
	case "json":
		return renderJSON(out, res)
	case "table":
		return renderTable(out, res)
	default:
		return fmt.Errorf("unknown format %q", format)
	}
}
