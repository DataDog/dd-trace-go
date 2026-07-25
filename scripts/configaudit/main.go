// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Command configaudit reports which DD_* environment-variable configurations
// have been migrated to internal/config and which have not.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// noncleanError reports a completed audit that found migration or coverage
// failures. The result has already been rendered when this error is returned.
type noncleanError struct {
	Result AuditResult
}

func (e *noncleanError) Error() string {
	return "configuration audit found unresolved migration or coverage findings"
}

type auditFunc func(root, pkgPrefix string) (AuditResult, error)

func main() {
	var (
		root    = flag.String("root", ".", "repository root")
		format  = flag.String("format", "table", "output format: table or json")
		pkgPref = flag.String("package", "", "restrict output to call sites whose package path (relative to the module root) starts with this prefix")
	)
	flag.Parse()

	if err := run(*root, *format, *pkgPref, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "configaudit:", err)
		os.Exit(1)
	}
}

func run(root, format, pkgPrefix string, out io.Writer) error {
	return runWithAudit(root, format, pkgPrefix, out, collectAudit)
}

func runWithAudit(root, format, pkgPrefix string, out io.Writer, audit auditFunc) error {
	res, err := audit(root, pkgPrefix)
	if err != nil {
		return err
	}
	var renderErr error
	switch format {
	case "json":
		renderErr = renderJSON(out, res)
	case "table":
		renderErr = renderTable(out, res)
	default:
		return fmt.Errorf("unknown format %q", format)
	}
	if renderErr != nil {
		return renderErr
	}
	if !res.Clean() {
		return &noncleanError{Result: res}
	}
	return nil
}

func collectAudit(root, pkgPrefix string) (AuditResult, error) {
	return collectAuditWithRawReadAllowlist(root, pkgPrefix, defaultRawReadAllowlist())
}

func collectAuditWithRawReadAllowlist(root, pkgPrefix string, allow rawReadAllowlist) (AuditResult, error) {
	scope, err := buildAuditScope(root)
	if err != nil {
		return AuditResult{}, err
	}
	known, err := loadKnown(filepath.Join(root, "internal", "env", "supported_configurations.json"))
	if err != nil {
		return AuditResult{}, err
	}
	migrated, err := loadMigrated(filepath.Join(root, "internal", "config"))
	if err != nil {
		return AuditResult{}, err
	}
	reads, err := scan(root, defaultRecognizers(), defaultExcludes())
	if err != nil {
		return AuditResult{}, err
	}
	findings, err := scanSyntax(root, allow)
	if err != nil {
		return AuditResult{}, err
	}
	for _, finding := range findings {
		if finding.Key != "" && !finding.Suppressed {
			reads[finding.Key] = appendUniqueCallSite(reads[finding.Key], finding.CallSite)
		}
	}
	reads = filterByPackage(reads, pkgPrefix)
	res := classify(known, migrated, reads)
	res.Scope = scope
	res.CoverageErrors = scanCoverage(root).Errors
	for _, finding := range findings {
		if !matchesPackage(finding.CallSite, pkgPrefix) {
			continue
		}
		if finding.Unresolved && !finding.Suppressed {
			res.Unresolved = append(res.Unresolved, finding)
		}
		if finding.Suppressed {
			res.Suppressions = append(res.Suppressions, finding)
		}
	}
	return res, nil
}

func appendUniqueCallSite(sites []CallSite, candidate CallSite) []CallSite {
	for _, site := range sites {
		if site.File == candidate.File && site.Line == candidate.Line && site.Func == candidate.Func && site.Package == candidate.Package {
			return sites
		}
	}
	return append(sites, candidate)
}

func matchesPackage(site CallSite, prefix string) bool {
	return prefix == "" || strings.HasPrefix(shortPkg(site.Package), prefix)
}
