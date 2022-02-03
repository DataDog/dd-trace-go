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

	escaped := fmt.Sprintf("%s", strconv.Quote(jsonStr))
	fmt.Printf(ruleGoTemplate, os.Args[1], os.Args[1], escaped)
}
