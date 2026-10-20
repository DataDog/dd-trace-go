// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package codeownership

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"
)

func TestTheoryAndBoundaryCases(t *testing.T) {
	for _, tc := range []struct {
		name, rules, path string
		dialect           Dialect
		owners            []string
	}{
		{"inline comment", "* @global\n*.js @js-owner # comment", "/app.js", GitHub, []string{"@js-owner"}},
		{"email", "*.go docs@example.com", "/app.go", GitHub, []string{"docs@example.com"}},
		{"rooted", "/apps/ @root", "/x/apps/a.go", GitHub, nil},
		{"unrooted", "apps/ @anywhere", "/x/apps/a.go", GitHub, []string{"@anywhere"}},
		{"middle slash github", "* @global\ndocs/* @docs", "/x/docs/a.md", GitHub, []string{"@global"}},
		{"middle slash gitlab", "* @global\ndocs/* @docs", "/x/docs/a.md", GitLab, []string{"@docs"}},
		{"github middle globstar", "* @global\na/**/b @owner", "/x/a/b", GitHub, []string{"@global"}},
		{"gitlab middle globstar", "* @global\na/**/b @owner", "/x/a/b", GitLab, []string{"@owner"}},
		{"bare logs", "**/logs @logs", "/logs", GitHub, []string{"@logs"}},
		{"directory boundary", "apps/ @apps", "/myapps/main.go", GitHub, nil},
		{"filename boundary", "README.md @docs", "/MYREADME.md", GitHub, nil},
		{"at in pattern", "/pkg@v1/ @team", "/pkg@v1/main.go", GitHub, []string{"@team"}},
		{"windows", "/src/ @team", "\\src\\main.go", GitHub, []string{"@team"}},
		{"windows gitlab", "/src/ @team", "\\src\\main.go", GitLab, []string{"@team"}},
		{"whitespace comment", "  # *.go @bad\n* @good", "/main.go", GitHub, []string{"@good"}},
		{"whitespace gitlab comment", "  # *.go @bad\n* @good", "/main.go", GitLab, []string{"@good"}},
		{"escaped slash", "src\\/file.go @owner", "/src/file.go", GitHub, []string{"@owner"}},
		{"escaped slash gitlab", "src\\/file.go @owner", "/src/file.go", GitLab, []string{"@owner"}},
		{"escaped initial hash", "\\#file.go @owner", "/#file.go", GitLab, []string{"@owner"}},
		{"sections", "* @admin\n[Docs]\nREADME.md @one\n[Other]\nREADME.md @two", "/README.md", GitLab, []string{"@admin", "@one", "@two"}},
		{"empty", "* @team", "", GitHub, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Parse(strings.NewReader(tc.rules), tc.dialect)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := c.Match(tc.path)
			if !slices.Equal(got.Owners(), tc.owners) {
				t.Fatalf("got %v want %v", got.Owners(), tc.owners)
			}
		})
	}
	for _, role := range []string{"@@developer", "@@developers", "@@maintainer", "@@maintainers", "@@owner", "@@OwNeRs"} {
		c, _ := Parse(strings.NewReader("*.go "+role), GitLab)
		got, _ := c.Match("/file.go")
		if got.FirstOwner() != role {
			t.Fatalf("role=%q owners=%v", role, got.Owners())
		}
	}
}
func TestDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		rules   string
		dialect Dialect
		count   int
	}{
		{"trailing\\", GitHub, 1}, {"[Docs]", GitHub, 1}, {"* @good bad-owner", GitHub, 1},
		{"[Docs][2]@owner\nREADME.md", GitLab, 1},
		{"[Docs]] @leaked\nREADME.md", GitLab, 2},
		{"[Docs][x] @leaked\nREADME.md", GitLab, 2},
		{"[   ] @blank\nREADME.md", GitLab, 1},
		{"[Broken", GitLab, 1}, {"*.go @@banana @valid", GitLab, 1},
		{"!*.rb malformed@", GitLab, 0}, {"*.go", GitLab, 1},
	} {
		t.Run(tc.rules, func(t *testing.T) {
			c, err := Parse(strings.NewReader(tc.rules), tc.dialect)
			if err != nil {
				t.Fatal(err)
			}
			if got := c.Diagnostics(); got != tc.count {
				t.Fatalf("diagnostics=%d want=%d", got, tc.count)
			}
		})
	}
}
func TestOwnershipIsImmutableAndStable(t *testing.T) {
	c, err := Parse(strings.NewReader("* @first @second @first"), GitHub)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := c.Match("/a.go")
	owners := first.Owners()
	owners[0] = "@changed"
	if first.FirstOwner() != "@first" || first.Tag() != `["@first","@second"]` {
		t.Fatal("ownership was mutated")
	}
	again, _ := c.Match("/b.go")
	if first != again {
		t.Fatal("single-rule ownership was not reused")
	}
	gl, _ := Parse(strings.NewReader("* @first\n[Second]\n* @second @first"), GitLab)
	for range 50 {
		got, _ := gl.Match("/a.go")
		if got.Tag() != `["@first","@second"]` {
			t.Fatalf("unstable section order: %s", got.Tag())
		}
	}
	var workers sync.WaitGroup
	for range 32 {
		workers.Go(func() {
			for range 100 {
				got, _ := gl.Match("/a.go")
				if got.Tag() != `["@first","@second"]` {
					t.Error(got.Tag())
				}
			}
		})
	}
	workers.Wait()
}

// Rules encode their owners only when a match first needs the tag. Readers
// of a shared result that need it at the same time all see one value.
func TestOwnershipTagOnFirstUse(t *testing.T) {
	c, err := Parse(strings.NewReader("* @first @second\n/other/ @other"), GitHub)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range c.rules {
		if r.ownership.tag.Load() != nil {
			t.Fatalf("tag for %v encoded while parsing", r.ownership.owners)
		}
	}
	shared, _ := c.Match("/a.go")
	tags := make([]string, 32)
	var workers sync.WaitGroup
	for i := range tags {
		workers.Go(func() { tags[i] = shared.Tag() })
	}
	workers.Wait()
	for _, tag := range tags {
		if tag != `["@first","@second"]` {
			t.Fatalf("tags: %q", tags)
		}
	}
	other, _ := c.Match("/other/a.go")
	if other.tag.Load() != nil || other.Tag() != `["@other"]` {
		t.Fatal("tag of a rule that no test needed was encoded")
	}
	if empty := newOwnership(nil); empty.Tag() != "" || empty.tag.Load() != nil {
		t.Fatal("ownerless rule has a tag")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func TestReadFailuresAndFileLimit(t *testing.T) {
	if c, err := Parse(io.MultiReader(strings.NewReader("* @first\n"), failingReader{}), GitHub); c != nil || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("partial parse: %v %v", c, err)
	}
	if _, err := Parse(strings.NewReader(""), Dialect(99)); err == nil {
		t.Fatal("invalid dialect")
	}
	if _, err := Load("", GitHub); err == nil {
		t.Fatal("empty path")
	}
	if _, err := Load(filepath.Join(t.TempDir(), "gone"), GitHub); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	for _, size := range []int{GitHubMaximumFileSize, GitHubMaximumFileSize + 1} {
		file := filepath.Join(t.TempDir(), "CODEOWNERS")
		contents := "* @owner\n#" + strings.Repeat("x", size-len("* @owner\n#"))
		if err := os.WriteFile(file, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		for _, dialect := range []Dialect{GitHub, GitLab} {
			c, err := Load(file, dialect)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := c.Match("/a.go")
			want := "@owner"
			if dialect == GitHub && size > GitHubMaximumFileSize {
				want = ""
			}
			if got.FirstOwner() != want {
				t.Fatalf("size=%d dialect=%d owners=%v", size, dialect, got.Owners())
			}
		}
	}
	c, err := Parse(strings.NewReader("*.go"+strings.Repeat(" @owner", 10000)), GitHub)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := c.Match("/main.go")
	if got.Tag() != `["@owner"]` {
		t.Fatalf("long line: %s", got.Tag())
	}
}
func TestParserWorkBoundsAndDuplicateCompaction(t *testing.T) {
	for _, dialect := range []Dialect{GitHub, GitLab} {
		content := "* @fallback\n/" + strings.Repeat("a", maximumPatternLength+1) + " @bad"
		c, _ := Parse(strings.NewReader(content), dialect)
		got, _ := c.Match("/" + strings.Repeat("a", maximumPatternLength+1))
		if got.FirstOwner() != "@fallback" || c.Diagnostics() != 1 {
			t.Fatalf("overlong pattern: %v %d", got.Owners(), c.Diagnostics())
		}
	}
	c, err := Parse(strings.NewReader(strings.Repeat("*.go @duplicate\n", 20000)), GitLab)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.sections[0].rules) != 1 {
		t.Fatal("duplicate rules were not compacted")
	}
	got, _ := c.Match("/main.go")
	if got.FirstOwner() != "@duplicate" {
		t.Fatal(got.Tag())
	}
	for _, content := range []string{"* " + strings.Repeat("x", 4*1024*1024) + "@", "[Docs][" + strings.Repeat("x", 4*1024*1024)} {
		if _, err := Parse(strings.NewReader(content), GitLab); err != nil {
			t.Fatal(err)
		}
	}
}
func BenchmarkMatch(b *testing.B) {
	for _, dialect := range []Dialect{GitHub, GitLab} {
		b.Run(fmt.Sprint(dialect), func(b *testing.B) {
			c, err := Parse(strings.NewReader("* @global\n/src/**/test?.go @tests\n/docs/ @docs"), dialect)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				got, _ := c.Match("/src/a/test1.go")
				if got.FirstOwner() != "@tests" {
					b.Fatal(got.Tag())
				}
			}
		})
	}
}
func FuzzMatch(f *testing.F) {
	for _, seed := range []struct{ rules, path string }{{"* @team", "/a.go"}, {"/**/x? @team", "/a/x1"}, {"[Docs] @team\n*.go\n!excluded.go", "/main.go"}, {"bad\\", "/x"}} {
		f.Add(seed.rules, seed.path)
	}
	f.Fuzz(func(t *testing.T, rules, value string) {
		if len(rules) > 65536 || len(value) > 4096 {
			t.Skip()
		}
		for _, dialect := range []Dialect{GitHub, GitLab} {
			c, err := Parse(strings.NewReader(rules), dialect)
			if err != nil {
				t.Fatal(err)
			}
			a, ok := c.Match(value)
			d, again := c.Match(value)
			if ok != again || !reflect.DeepEqual(a, d) {
				t.Fatal("non-deterministic match")
			}
		}
	})
}

func TestPathologicalMatchingAndLargeOwnerLists(t *testing.T) {
	pattern := strings.Repeat("*a", 32) + "b"
	c, _ := Parse(strings.NewReader("* @global\n"+pattern+" @slow"), GitHub)
	nonMatch := "/" + strings.Repeat("a", 2000) + "c"
	for range 100 {
		got, _ := c.Match(nonMatch)
		if got.FirstOwner() != "@global" {
			t.Fatal(got.Tag())
		}
	}
	got, _ := c.Match("/" + strings.Repeat("a", 32) + "b")
	if got.FirstOwner() != "@slow" {
		t.Fatal(got.Tag())
	}
	var workers sync.WaitGroup
	start := make(chan struct{})
	done := make(chan struct{})
	for range 4 {
		workers.Go(func() {
			<-start
			got, _ := c.Match(nonMatch)
			if got.FirstOwner() != "@global" {
				t.Error(got.Tag())
			}
		})
	}
	close(start)
	go func() { workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent matching exceeded the matching timeout")
	}
	c, _ = Parse(strings.NewReader("* @global\n*"+strings.Repeat("a", 256)+"b @slow"), GitHub)
	got, _ = c.Match("/" + strings.Repeat("a", 2000) + "b")
	if got.FirstOwner() != "@global" || c.Diagnostics() != 0 {
		t.Fatal("match work limit was not applied")
	}
	var text strings.Builder
	text.WriteString("*.go")
	for i := range 5000 {
		fmt.Fprintf(&text, " @owner%d", i)
	}
	c, _ = Parse(strings.NewReader(text.String()), GitHub)
	for range 20 {
		got, _ := c.Match("/file.go")
		if len(got.owners) != 5000 {
			t.Fatal(len(got.owners))
		}
	}
	var absent *CodeOwners
	if _, ok := absent.Match("/file.go"); ok {
		t.Fatal("nil rules matched")
	}
}

func TestUnicodeTokenAndSectionBoundaries(t *testing.T) {
	c, _ := Parse(strings.NewReader("* @global\n*.go @first\u00a0@second"), GitHub)
	got, _ := c.Match("/a.go")
	if got.FirstOwner() != "@global" || c.Diagnostics() != 1 {
		t.Fatalf("unicode owner separator: %v %d", got.Owners(), c.Diagnostics())
	}
	c, _ = Parse(strings.NewReader("[Σ]\n*.go @first\n[ς]\n*.go @second"), GitLab)
	got, _ = c.Match("/a.go")
	if got.FirstOwner() != "@second" || len(got.owners) != 1 {
		t.Fatalf("unicode section folding: %v", got.Owners())
	}
}

func TestBOMOnlyAtFileStart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "CODEOWNERS")
	if err := os.WriteFile(file, []byte("\uFEFF* @global\n\uFEFF*.go @literal"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(file, GitHub)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := c.Match("/a.go")
	if got.FirstOwner() != "@global" {
		t.Fatal(got.Tag())
	}
	got, _ = c.Match("/\uFEFFa.go")
	if got.FirstOwner() != "@literal" {
		t.Fatal(got.Tag())
	}
}

func TestFileEncodingsAndLines(t *testing.T) {
	text := "* @fallback\r*.go @go\r\n*.md @docs\n"
	for _, encoding := range []string{"utf8", "utf8-bom", "utf16-le", "utf16-be", "utf32-le", "utf32-be"} {
		t.Run(encoding, func(t *testing.T) {
			data := []byte(text)
			if encoding == "utf8-bom" {
				data = append([]byte{0xef, 0xbb, 0xbf}, data...)
			}
			if strings.HasPrefix(encoding, "utf16") || strings.HasPrefix(encoding, "utf32") {
				var order binary.ByteOrder = binary.LittleEndian
				if strings.HasSuffix(encoding, "be") {
					order = binary.BigEndian
				}
				if strings.HasPrefix(encoding, "utf16") {
					units := append([]uint16{0xfeff}, utf16.Encode([]rune(text))...)
					data = make([]byte, len(units)*2)
					for i, unit := range units {
						order.PutUint16(data[i*2:], unit)
					}
				} else {
					runes := append([]rune{0xfeff}, []rune(text)...)
					data = make([]byte, len(runes)*4)
					for i, char := range runes {
						order.PutUint32(data[i*4:], uint32(char))
					}
				}
			}
			filename := filepath.Join(t.TempDir(), "CODEOWNERS")
			if err := os.WriteFile(filename, data, 0600); err != nil {
				t.Fatal(err)
			}
			c, err := Load(filename, GitHub)
			if err != nil {
				t.Fatal(err)
			}
			for value, want := range map[string]string{"/a.go": "@go", "/a.md": "@docs", "/a.txt": "@fallback"} {
				owners, _ := c.Match(value)
				if owners.FirstOwner() != want {
					t.Fatalf("%s: %v want=%s", value, owners.Owners(), want)
				}
			}
		})
	}
	// Parse receives decoded lines and has no file-size or BOM policy.
	c, err := Parse(strings.NewReader("* @owner\n#"+strings.Repeat("x", GitHubMaximumFileSize)), GitHub)
	if err != nil {
		t.Fatal(err)
	}
	owners, _ := c.Match("/a.go")
	if owners.FirstOwner() != "@owner" {
		t.Fatal("the file-size limit leaked into Parse")
	}
	c, err = Parse(strings.NewReader("\ufeff* @literal"), GitHub)
	if err != nil {
		t.Fatal(err)
	}
	owners, _ = c.Match("/a.go")
	if owners.FirstOwner() != "" {
		t.Fatal("Parse removed a literal BOM")
	}
	for _, text := range []string{"a\r\nb\r\rc\nd", "a\r\n", "\r\n"} {
		reader := &fileLineReader{reader: strings.NewReader(text)}
		var output strings.Builder
		buffer := make([]byte, 1) // Force CRLF to cross Read calls.
		for {
			n, err := reader.Read(buffer)
			output.Write(buffer[:n])
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		want := strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
		if output.String() != want {
			t.Fatalf("line endings: %q want %q", output.String(), want)
		}
	}
}

func TestLargeSectionUnionKeepsOrderAndImmutableOwners(t *testing.T) {
	var text strings.Builder
	text.WriteString("[First]\n*.go")
	want := make([]string, 0, 202)
	for i := range 200 {
		owner := fmt.Sprintf("@team%d", i)
		fmt.Fprintf(&text, " %s", owner)
		want = append(want, owner)
	}
	// A large duplicate-only section must not trigger quadratic rescanning.
	text.WriteString("\n[Same]\n*.go")
	for _, owner := range want {
		fmt.Fprintf(&text, " %s", owner)
	}
	text.WriteString("\n[More]\n*.go @extra @team0\n[Last]\n*.go @team199 @extra @final")
	want = append(want, "@extra", "@final")
	c, err := Parse(strings.NewReader(text.String()), GitLab)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		got, found := c.Match("/main.go")
		if !found || !slices.Equal(got.Owners(), want) {
			t.Fatalf("union: %v", got.Owners())
		}
		copy := got.Owners()
		copy[0] = "@changed"
	}
}

// A directory rule owns the files below the directory, not a file with its name.
func TestDirectoryRulesOwnOnlyDescendants(t *testing.T) {
	for _, dialect := range []Dialect{GitHub, GitLab} {
		for _, pattern := range []string{"/pkg/", "/pkg/**/"} {
			c, err := Parse(strings.NewReader(pattern+" @owner"), dialect)
			if err != nil {
				t.Fatal(err)
			}
			for range 3 {
				if _, found := c.Match("/pkg"); found {
					t.Fatal("file query inherited a directory requirement", dialect, pattern)
				}
				if got, found := c.Match("/pkg/test.go"); !found || got.FirstOwner() != "@owner" {
					t.Fatal("child file", dialect, pattern)
				}
			}
		}
	}
}
