// +build ignore

// This tool validates that all *.go files in the repository have the copyright text attached.
package main

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
)

var copyrightText = []byte("// Copyright 2016-2019 Datadog, Inc.")

func main() {
	var missing bool
	if err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if filepath.Ext(path) != ".go" || info.IsDir() || strings.Contains(path, "vendor") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		// read 1KB, header should be there
		snip := make([]byte, 1024)
		_, err = f.Read(snip)
		if err != nil {
			return err
		}
		if !bytes.Contains(snip, copyrightText) {
			// report missing header
			missing = true
			log.Printf("Copyright header missing in %q.\n", path)
		}
		return nil
	}); err != nil {
		log.Fatal(err)
	}
	if missing {
		// some files are missing the header, exit code 1 to fail CI
		os.Exit(1)
	}
}
