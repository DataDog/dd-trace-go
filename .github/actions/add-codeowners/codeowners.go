// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// codeowners annotates gotestsum JUnit XML report files with a `file`
// attribute on each `<testcase>` element, derived from the element's
// `classname` attribute. The `file` attribute is used downstream by
// datadog-ci to associate test results with code owners.
//
// Usage: codeowners [path]
//
// path is the directory to search for gotestsum-report*.xml files.
// It defaults to the current directory.
package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func run() error {
	dir := "."
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}

	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("result path does not exist")
	}

	tmpDir, err := os.MkdirTemp(dir, "codeowners-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	matches, err := filepath.Glob(filepath.Join(dir, "gotestsum-report*.xml"))
	if err != nil {
		return err
	}

	for _, xmlFile := range matches {
		if err := processFile(xmlFile, tmpDir); err != nil {
			return fmt.Errorf("processing %s: %w", xmlFile, err)
		}
	}
	return nil
}

// processFile rewrites xmlFile in place, adding a file="..." attribute to any
// <testcase> element that does not already have one. tmpDir is used for the
// temporary file written before atomically replacing xmlFile.
func processFile(xmlFile, tmpDir string) error {
	data, err := os.ReadFile(xmlFile)
	if err != nil {
		return err
	}

	out, err := annotate(data)
	if err != nil {
		return err
	}

	tmpFile := filepath.Join(tmpDir, filepath.Base(xmlFile))
	if err := os.WriteFile(tmpFile, out, 0644); err != nil {
		return err
	}

	return os.Rename(tmpFile, xmlFile)
}

// annotate streams through the XML in data, injecting a file="..." attribute
// on each <testcase> start element that does not already have one.
func annotate(data []byte) ([]byte, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "\t")

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.CharData:
			// We assume whitespace-only character data is formatting/indentation
			// as opposed to content contained between tags. We drop it because
			// anything other than space ends up escaped and doesn't look right.
			if len(bytes.TrimSpace(t)) == 0 {
				continue
			}
		case xml.StartElement:
			if t.Name.Local == "testcase" && !hasAttr(t.Attr, "file") {
				if path := relativePathFromPackageName(attrValue(t.Attr, "classname")); path != "" {
					t.Attr = append(t.Attr, xml.Attr{
						Name:  xml.Name{Local: "file"},
						Value: path,
					})
				}
			}
			tok = t
		}

		if err := enc.EncodeToken(tok); err != nil {
			return nil, err
		}
	}

	if err := enc.Flush(); err != nil {
		return nil, err
	}

	return append(buf.Bytes(), '\n'), nil
}

func hasName(name string) func(xml.Attr) bool {
	return func(a xml.Attr) bool { return a.Name.Local == name }
}

func hasAttr(attrs []xml.Attr, name string) bool {
	return slices.ContainsFunc(attrs, hasName(name))
}

func attrValue(attrs []xml.Attr, name string) string {
	if i := slices.IndexFunc(attrs, hasName(name)); i >= 0 {
		return attrs[i].Value
	}
	return ""
}

func relativePathFromPackageName(name string) string {
	// "/v2" appears in the package name but not the path in the repository
	s := strings.ReplaceAll(name, "/v2", "")
	_, after, found := strings.Cut(s, "dd-trace-go/")
	if !found {
		return ""
	}
	return "/" + after
}
