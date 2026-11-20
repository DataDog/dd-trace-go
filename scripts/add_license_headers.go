// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build ignore

// add_license_headers prepends the repository's Apache license header to Go
// files that lack it. Code generators such as gqlgen rewrite their output files
// from scratch and drop any header on every run, so //go:generate directives
// chain this script after the generator to keep the copyright check green.
//
// Usage (in go:generate directives, chained after the generator):
//
//	//go:generate go run ../../scripts/add_license_headers.go <dir>...
//
// Only directories are accepted: "go run" treats any further ".go" argument as
// another source file of the program, so a single file cannot be passed.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// header mirrors the block that scripts/check_copyright.go requires. The
// "2023-present" year keeps the line stable across regenerations.
const header = `// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: add_license_headers <dir>...\n")
		os.Exit(1)
	}

	// Same detection as scripts/check_copyright.go: a file passes the check
	// when the copyright line appears in its first kilobyte.
	copyrightRegexp := regexp.MustCompile(`// Copyright 20[0-9]{2}[0-9,\-(present)]* Datadog, Inc.`)

	for _, arg := range os.Args[1:] {
		info, err := os.Stat(arg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if !info.IsDir() {
			fmt.Fprintf(os.Stderr, "error: %s is not a directory\n", arg)
			os.Exit(1)
		}
		var files []string
		err = filepath.Walk(arg, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			// Skip the same directories scripts/check_copyright.go skips.
			if fi.IsDir() && (fi.Name() == "vendor" || fi.Name() == ".otelc-build") {
				return filepath.SkipDir
			}
			if !fi.IsDir() && strings.HasSuffix(path, ".go") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		for _, file := range files {
			if err := addHeader(copyrightRegexp, file); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
		}
	}
}

func addHeader(copyrightRegexp *regexp.Regexp, file string) error {
	content, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	snip := content
	if len(snip) > 1024 {
		snip = snip[:1024]
	}
	if copyrightRegexp.Match(snip) {
		// The file already carries the header; nothing to do.
		return nil
	}
	if _, err := io.WriteString(os.Stdout, "added license header to "+file+"\n"); err != nil {
		return err
	}
	return os.WriteFile(file, []byte(header+string(content)), 0644)
}
