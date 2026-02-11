// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

//go:build ignore

// msgp_checklocks_ignore injects // +checklocksignore annotations above
// msgp-generated methods whose receiver matches the specified type.
//
// Generated msgp code (DecodeMsg, EncodeMsg, Msgsize) accesses struct fields
// directly without acquiring locks. These accesses are safe because msgp
// serialization only runs on finished spans that are no longer concurrently
// modified. However, checklocks cannot infer this lifecycle invariant, so we
// suppress its analysis for these methods.
//
// Usage (in go:generate directives, chained after msgp):
//
//	//go:generate go run ../../scripts/msgp_checklocks_ignore.go -type Span -file span_msgp.go
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
)

func main() {
	typeName := flag.String("type", "", "receiver type name to match (e.g. Span)")
	fileName := flag.String("file", "", "generated file to process")
	flag.Parse()

	if *typeName == "" || *fileName == "" {
		fmt.Fprintf(os.Stderr, "usage: msgp_checklocks_ignore -type <Type> -file <file.go>\n")
		os.Exit(1)
	}

	// Match func declarations with pointer or value receiver of the target type.
	// Examples:
	//   func (z *Span) EncodeMsg(en *msgp.Writer) (err error) {
	//   func (z Span) Msgsize() (s int) {
	receiverPattern := regexp.MustCompile(
		`^func\s+\(\w+\s+\*?` + regexp.QuoteMeta(*typeName) + `\)`,
	)

	content, err := os.ReadFile(*fileName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading %s: %v\n", *fileName, err)
		os.Exit(1)
	}

	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	var out []string
	annotation := "// +checklocksignore"
	for i, line := range lines {
		// Skip if annotation already present on the previous line.
		if receiverPattern.MatchString(line) {
			if i == 0 || strings.TrimSpace(lines[i-1]) != annotation {
				out = append(out, annotation)
			}
		}
		out = append(out, line)
	}

	if err := os.WriteFile(*fileName, []byte(strings.Join(out, "\n")+"\n"), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing %s: %v\n", *fileName, err)
		os.Exit(1)
	}
}
