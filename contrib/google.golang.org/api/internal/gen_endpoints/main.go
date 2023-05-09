// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// This program generates a tree of endpoints for span tagging based on the
// API definitions in github.com/google/google-api-go-client.

package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/yosida95/uritemplate/v3"
)

const (
	// The github.com/googleapis/google-api-go-client version to use.
	version = "v0.121.0"
	outFile = "../../endpoints_gen.go"
)

func downloadUrl() string {
	return fmt.Sprintf("https://github.com/googleapis/google-api-go-client/archive/refs/tags/%s.zip", version)
}

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
		Hostname     string
		HTTPMethod   string
		PathTemplate string
		PathMatcher  *regexp.Regexp

		ServiceName  string
		ResourceName string
	}
)

// String returns a constructor without field names.
func (e Endpoint) String() string {
	return fmt.Sprintf(`{Hostname: "%s", HTTPMethod: "%s", PathTemplate: "%s", PathMatcher: regexp.MustCompile(`+"`"+`%s`+"`"+`), ServiceName: "%s", ResourceName: "%s"}`,
		e.Hostname, e.HTTPMethod, e.PathTemplate, e.PathMatcher.String(), e.ServiceName, e.ResourceName)
}

func main() {
	var es []Endpoint

	root := downloadGoogleApiSrc()
	defer os.RemoveAll(root)

	log.Println("Parsing GCP service json API definitions...")

	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		if filepath.Ext(p) == ".json" {
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
				res, err := handleResource(&def, resource)
				if err != nil {
					return err
				}
				es = append(es, res...)
			}
		}

		return nil
	})
	checkError(err)

	f, err := os.Create(outFile)
	checkError(err)
	defer f.Close()

	sort.Slice(es, func(i, j int) bool {
		return es[i].String() < es[j].String()
	})

	log.Printf("Generating templated Go code in %s...\n", outFile)
	template.Must(template.New("").Parse(tpl)).Execute(f, map[string]interface{}{
		"Endpoints": es,
		"Year":      time.Now().Year(),
	})
	log.Println("Done!")
}

func checkError(err error) {
	if err != nil {
		log.Fatalln(err)
	}
}

func handleResource(def *APIDefinition, resource *APIResource) ([]Endpoint, error) {
	var es []Endpoint
	if resource.Methods != nil {
		for _, method := range resource.Methods {
			mes, err := handleMethod(def, method)
			if err != nil {
				return nil, err
			}
			es = append(es, mes...)
		}
	}
	if resource.Resources != nil {
		for _, child := range resource.Resources {
			res, err := handleResource(def, child)
			if err != nil {
				return nil, err
			}
			es = append(es, res...)
		}
	}
	return es, nil
}

func handleMethod(def *APIDefinition, method *APIMethod) ([]Endpoint, error) {
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
	return []Endpoint{{
		Hostname:     hostname,
		HTTPMethod:   method.HTTPMethod,
		PathTemplate: path,
		PathMatcher:  uritpl.Regexp(),
		ServiceName:  "google." + def.Name,
		ResourceName: method.ID,
	}}, nil
}

func downloadGoogleApiSrc() string {
	zipUrl := downloadUrl()
	dir := os.TempDir()

	zipFile := path.Join(dir, "google-api-go-client.zip")
	defer os.Remove(zipFile)
	dst := path.Join(dir, "google-api-go-client")
	err := os.Mkdir(dst, os.ModePerm)
	checkError(err)

	log.Printf("Downloading %s into %s...\n", zipUrl, dst)

	out, err := os.Create(zipFile)
	defer out.Close()
	resp, err := http.Get(zipUrl)
	checkError(err)
	defer resp.Body.Close()
	_, err = io.Copy(out, resp.Body)
	checkError(err)

	log.Printf("Extracting %s into %s...\n", zipFile, dst)

	zf, err := zip.OpenReader(zipFile)
	checkError(err)
	defer zf.Close()

	for _, f := range zf.File {
		filePath := filepath.Join(dst, f.Name)
		if !strings.HasPrefix(filePath, filepath.Clean(dst)+string(os.PathSeparator)) {
			panic("invalid file path")
		}
		if f.FileInfo().IsDir() {
			os.MkdirAll(filePath, os.ModePerm)
			continue
		}
		err = os.MkdirAll(filepath.Dir(filePath), os.ModePerm)
		checkError(err)

		dstFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		checkError(err)

		fileInArchive, err := f.Open()
		checkError(err)

		_, err = io.Copy(dstFile, fileInArchive)
		checkError(err)

		dstFile.Close()
		fileInArchive.Close()
	}
	return dst
}

var tpl = `// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright {{ .Year }} Datadog, Inc.

// Code generated by internal/gen_endpoints/main.go DO NOT EDIT

//go:generate go run internal/gen_endpoints/main.go

package api

import (
	"regexp"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/google.golang.org/api/internal"
)

func init() {
	apiEndpoints = internal.NewTree([]internal.Endpoint{
		{{- range .Endpoints }}
		{{ . }},
		{{- end }}
	}...)
}
`
