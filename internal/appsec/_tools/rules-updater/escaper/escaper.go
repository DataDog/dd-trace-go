// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package main

import (
	_ "embed"
	"fmt"
	"os"
	"strconv"
	"text/template"
)

//go:embed rules.json
var jsonStr string

//go:embed template.txt
var ruleGoTemplate string

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "Usage: %s <version>", os.Args[0])
		os.Exit(1)
	}

	tmpl, err := template.New("rule.go").Parse(ruleGoTemplate)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Text template creation failed")
		os.Exit(1)
	}
	err = tmpl.Execute(os.Stdout, struct {
		Version string
		Rules   string
	}{
		Version: os.Args[1],
		Rules:   strconv.Quote(jsonStr),
	})

	if err != nil {
		fmt.Fprintln(os.Stderr, "Text template execution failed")
		os.Exit(1)
	}
}
