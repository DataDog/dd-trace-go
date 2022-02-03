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
