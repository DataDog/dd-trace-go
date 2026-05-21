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
	"os"
)

func main() {
	var (
		root   = flag.String("root", ".", "repository root")
		format = flag.String("format", "table", "output format: table or json")
	)
	flag.Parse()

	if err := run(*root, *format, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "configaudit:", err)
		os.Exit(1)
	}
}

func run(root, format string, out *os.File) error {
	_ = root
	_ = format
	_ = out
	return nil
}
