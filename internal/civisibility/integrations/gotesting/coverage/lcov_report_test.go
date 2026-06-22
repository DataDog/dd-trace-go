// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package coverage

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	civisibilityutils "github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
)

func TestWriteLCOVReportFromProfile(t *testing.T) {
	ResetForTesting()
	civisibilityutils.ResetCITags()
	t.Cleanup(func() {
		ResetForTesting()
		civisibilityutils.ResetCITags()
	})
	civisibilityutils.AddCITagsMap(map[string]string{
		constants.GitRepositoryURL: "https://github.com/acme/project.git",
	})

	profilePath := writeLCOVTestProfile(t, `mode: count
github.com/acme/project/b.go:10.1,12.1 1 1
github.com/acme/project/b.go:11.1,13.5 1 0
github.com/acme/project/a.go:5.1,5.8 1 0
github.com/acme/project/a.go:5.1,6.1 1 1
github.com/acme/project/ignored.go:1.1,3.1 0 1
`)

	var report bytes.Buffer
	if err := WriteLCOVReportFromProfile(profilePath, &report); err != nil {
		t.Fatalf("WriteLCOVReportFromProfile() error = %v", err)
	}

	expected := `SF:a.go
DA:5,1
LH:1
LF:1
end_of_record
SF:b.go
DA:10,1
DA:11,1
DA:12,0
DA:13,0
LH:2
LF:4
end_of_record
`
	if report.String() != expected {
		t.Fatalf("unexpected LCOV report\nwant:\n%s\ngot:\n%s", expected, report.String())
	}
}

func TestWriteLCOVReportFromProfileSkipsUnresolvedFileNamesWithColon(t *testing.T) {
	profilePath := writeLCOVTestProfile(t, `mode: set
C:/workspace/project/file.go:3.1,4.1 1 1
`)

	var report bytes.Buffer
	if err := WriteLCOVReportFromProfile(profilePath, &report); err != nil {
		t.Fatalf("WriteLCOVReportFromProfile() error = %v", err)
	}

	if report.Len() != 0 {
		t.Fatalf("expected unresolved drive path to be skipped, got:\n%s", report.String())
	}
}

func TestWriteLCOVReportFromProfileUsesRepositoryRelativePathsFromCITags(t *testing.T) {
	ResetForTesting()
	civisibilityutils.ResetCITags()
	t.Cleanup(func() {
		ResetForTesting()
		civisibilityutils.ResetCITags()
	})

	workspace := t.TempDir()
	civisibilityutils.AddCITagsMap(map[string]string{
		constants.CIWorkspacePath:  workspace,
		constants.GitRepositoryURL: "https://github.com/acme/project.git",
	})

	profilePath := writeLCOVTestProfile(t, fmt.Sprintf(`mode: count
github.com/acme/project/pkg/imported.go:10.1,11.1 1 1
github.com/acme/project/pkg/aliased.go:1.1,2.1 1 0
%s:2.1,3.1 1 1
./pkg/relative.go:7.1,8.1 1 1
github.com/other/project/pkg/external.go:1.1,2.1 1 1
%s:1.1,2.1 1 1
%s:3.1,4.1 1 1
`,
		filepath.Join(workspace, "pkg", "aliased.go"),
		filepath.Join(filepath.Dir(workspace), "outside.go"),
		filepath.Join(workspace, "pkg", "absolute.go")))

	var report bytes.Buffer
	if err := WriteLCOVReportFromProfile(profilePath, &report); err != nil {
		t.Fatalf("WriteLCOVReportFromProfile() error = %v", err)
	}

	expected := `SF:pkg/absolute.go
DA:3,1
LH:1
LF:1
end_of_record
SF:pkg/aliased.go
DA:1,0
DA:2,1
LH:1
LF:2
end_of_record
SF:pkg/imported.go
DA:10,1
LH:1
LF:1
end_of_record
SF:pkg/relative.go
DA:7,1
LH:1
LF:1
end_of_record
`
	if report.String() != expected {
		t.Fatalf("unexpected LCOV report\nwant:\n%s\ngot:\n%s", expected, report.String())
	}
}

func TestWriteLCOVReportFromProfileUsesModuleRelativePaths(t *testing.T) {
	ResetForTesting()
	civisibilityutils.ResetCITags()
	t.Cleanup(func() {
		ResetForTesting()
		civisibilityutils.ResetCITags()
	})

	modulePath = "github.com/acme/project/v2/internal/module"
	moduleDir = t.TempDir()
	profilePath := writeLCOVTestProfile(t, fmt.Sprintf(`mode: count
github.com/acme/project/v2/internal/module/pkg/imported.go:10.1,11.1 1 1
%s:3.1,4.1 1 1
`, filepath.Join(moduleDir, "local.go")))

	var report bytes.Buffer
	if err := WriteLCOVReportFromProfile(profilePath, &report); err != nil {
		t.Fatalf("WriteLCOVReportFromProfile() error = %v", err)
	}

	expected := `SF:internal/module/local.go
DA:3,1
LH:1
LF:1
end_of_record
SF:internal/module/pkg/imported.go
DA:10,1
LH:1
LF:1
end_of_record
`
	if report.String() != expected {
		t.Fatalf("unexpected LCOV report\nwant:\n%s\ngot:\n%s", expected, report.String())
	}
}

func TestWriteLCOVReportFromProfileSkipsProfileWithoutExecutableLines(t *testing.T) {
	profilePath := writeLCOVTestProfile(t, `mode: count
github.com/acme/project/ignored.go:1.1,3.1 0 1
`)

	var report bytes.Buffer
	if err := WriteLCOVReportFromProfile(profilePath, &report); err != nil {
		t.Fatalf("WriteLCOVReportFromProfile() error = %v", err)
	}
	if report.Len() != 0 {
		t.Fatalf("expected empty LCOV report, got:\n%s", report.String())
	}
}

func TestWriteLCOVReportFromProfileRejectsInvalidProfile(t *testing.T) {
	profilePath := writeLCOVTestProfile(t, `not a coverprofile
`)

	var report bytes.Buffer
	if err := WriteLCOVReportFromProfile(profilePath, &report); err == nil {
		t.Fatal("WriteLCOVReportFromProfile() expected error")
	}
}

func writeLCOVTestProfile(t *testing.T, content string) string {
	t.Helper()

	profilePath := filepath.Join(t.TempDir(), "coverage.out")
	if err := os.WriteFile(profilePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write coverage profile: %v", err)
	}
	return profilePath
}
