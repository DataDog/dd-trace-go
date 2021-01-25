// +build ignore

package main

import (
	"encoding/json"
	"fmt"
	"os"

	"gopkg.in/DataDog/dd-trace-go.v1/profiler/internal/stackparse"
)

// usage: go run stack2json.go < example.txt
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	gs, errs := stackparse.Parse(os.Stdin)
	out, err := json.MarshalIndent(gs, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", out)

	if errs != nil {
		for i, e := range errs.Errors {
			fmt.Printf("error %d: %s\n", i+1, e)
		}
	}
	return errs
}
