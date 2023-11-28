// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// This program generates a tree of endpoints for span tagging based on the
// API definitions in github.com/google/google-api-go-client.

package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/yosida95/uritemplate/v3"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// The github.com/googleapis/google-api-go-client version to use.
	version = "v0.121.0"
)

var (
	outFile string
)

type (
	APIDefinition struct {
		ID            string                  `json:"id"`
		Name          string                  `json:"name"`
		CanonicalName string                  `json:"canonicalName"`
		BaseURL       string                  `json:"baseUrl"`
		BasePath      string                  `json:"basePath"`
		Resources     map[string]*APIResource `json:"resources"`
		RootURL       string                  `json:"rootUrl"`
	}
	APIResource struct {
		Methods   map[string]*APIMethod   `json:"methods"`
		Resources map[string]*APIResource `json:"resources"`
	}
	APIMethod struct {
		ID         string `json:"id"`
		FlatPath   string `json:"flatPath"`
		Path       string `json:"path"`
		HTTPMethod string `json:"httpMethod"`
	}
	Endpoint struct {
		Hostname     string `json:"hostname"`
		HTTPMethod   string `json:"http_method"`
		PathTemplate string `json:"path_template"`
		PathRegex    string `json:"path_regex"`
		ServiceName  string `json:"service_name"`
		ResourceName string `json:"resource_name"`
	}
)

// String returns a constructor without field names.
func (e *Endpoint) String() string {
	return fmt.Sprintf(`{Hostname: "%s", HTTPMethod: "%s", PathTemplate: "%s", PathMatcher: regexp.MustCompile(`+"`"+`%s`+"`"+`), ServiceName: "%s", ResourceName: "%s"}`,
		e.Hostname, e.HTTPMethod, e.PathTemplate, e.PathRegex, e.ServiceName, e.ResourceName)
}

func googleAPIClientURL() string {
	return fmt.Sprintf("https://github.com/googleapis/google-api-go-client/archive/refs/tags/%s.zip", version)
}

func main() {
	flag.StringVar(&outFile, "o", "gen_endpoints.json", "Path to the output file")

	var es []*Endpoint

	root, err := downloadGoogleAPISrc()
	assertNoError(err)
	defer os.RemoveAll(root)

	log.Println("Parsing GCP service json API definitions...")

	err = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() || filepath.Ext(p) != ".json" {
			return nil
		}
		var def APIDefinition
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()

		if err := json.NewDecoder(f).Decode(&def); err != nil {
			return err
		}
		for _, resource := range def.Resources {
			res, err := buildEndpoints(&def, resource)
			if err != nil {
				return err
			}
			es = append(es, res...)
		}
		return nil
	})
	assertNoError(err)

	f, err := os.Create(outFile)
	assertNoError(err)
	defer f.Close()

	sort.Slice(es, func(i, j int) bool {
		return es[i].String() < es[j].String()
	})

	err = writeJSON(es, outFile)
	assertNoError(err)
	log.Println("Done!")
}

func assertNoError(err error) {
	if err != nil {
		log.Fatalln(err)
	}
}

func buildEndpoints(def *APIDefinition, resource *APIResource) ([]*Endpoint, error) {
	var endpoints []*Endpoint
	for _, method := range resource.Methods {
		e, err := buildEndpointFromMethod(def, method)
		if err != nil {
			return nil, err
		}
		endpoints = append(endpoints, e)
	}
	for _, child := range resource.Resources {
		es, err := buildEndpoints(def, child)
		if err != nil {
			return nil, err
		}
		endpoints = append(endpoints, es...)
	}
	return endpoints, nil
}

func buildEndpointFromMethod(def *APIDefinition, method *APIMethod) (*Endpoint, error) {
	u, err := url.Parse(def.RootURL)
	if err != nil {
		return nil, err
	}
	hostname := u.Hostname()

	path := method.FlatPath
	if path == "" {
		path = method.Path
	}
	path = def.BasePath + path

	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	uritpl, err := uritemplate.New(path)
	if err != nil {
		return nil, err
	}
	return &Endpoint{
		Hostname:     hostname,
		HTTPMethod:   method.HTTPMethod,
		PathTemplate: path,
		PathRegex:    uritpl.Regexp().String(),
		ServiceName:  "google." + def.Name,
		ResourceName: method.ID,
	}, nil
}

func downloadGoogleAPISrc() (string, error) {
	zipURL := googleAPIClientURL()
	dir := os.TempDir()

	zipFile := path.Join(dir, "google-api-go-client.zip")
	defer os.Remove(zipFile)
	dst := path.Join(dir, "google-api-go-client")
	err := os.Mkdir(dst, os.ModePerm)
	assertNoError(err)

	log.Printf("Downloading %s into %s...\n", zipURL, dst)

	out, err := os.Create(zipFile)
	defer out.Close()
	resp, err := http.Get(zipURL)
	assertNoError(err)
	defer resp.Body.Close()
	_, err = io.Copy(out, resp.Body)
	assertNoError(err)

	log.Printf("Extracting %s into %s...\n", zipFile, dst)

	zf, err := zip.OpenReader(zipFile)
	assertNoError(err)
	defer zf.Close()

	for _, f := range zf.File {
		filePath := filepath.Join(dst, f.Name)
		if !strings.HasPrefix(filePath, filepath.Clean(dst)+string(os.PathSeparator)) {
			return "", fmt.Errorf("invalid file path: %s", filePath)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(filePath, os.ModePerm); err != nil {
				return "", err
			}
			continue
		}
		if err := extractFile(f, filePath); err != nil {
			return "", err
		}
	}
	return dst, nil
}

func extractFile(f *zip.File, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), os.ModePerm); err != nil {
		return err
	}
	dstFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
	if err != nil {
		return err
	}
	defer dstFile.Close()

	fileInArchive, err := f.Open()
	if err != nil {
		return err
	}
	defer fileInArchive.Close()

	_, err = io.Copy(dstFile, fileInArchive)
	return err
}

func writeJSON(es []*Endpoint, outFile string) error {
	log.Printf("Generating json file in %s...\n", outFile)
	b, err := json.Marshal(es)
	if err != nil {
		return err
	}
	buf := &bytes.Buffer{}
	if err := json.Compact(buf, b); err != nil {
		return err
	}
	return os.WriteFile(outFile, buf.Bytes(), 0644)
}
