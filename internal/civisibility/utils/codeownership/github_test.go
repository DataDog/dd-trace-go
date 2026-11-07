// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package codeownership

import "testing"

func TestGitHubAnchoredFilenamePrefix(t *testing.T) {
	// The SDK's /dir/prefix* ownership rules include matching directories.
	// Keep the parser's general glob rules as well as this upstream regression.
	checkRules(t, GitHub, []ruleTest{
		{
			name:  "anchored prefix",
			rules: "/src/ci_* @owner\n",
			files: []fileTest{
				{"/src/ci_new.go", []string{"@owner"}},
				{"/src/ci_new_test.go", []string{"@owner"}},
				{"/src/ci_", []string{"@owner"}},
				{"/src/ci_group/nested.go", []string{"@owner"}},
				{"/src/app.go", nil},
				{"/src/ci.go", nil},
				{"/src/CI_new.go", nil},
				{"/src/nested/ci_new.go", nil},
				{"/other/src/ci_new.go", nil},
			},
		},
		{
			name:  "root prefix",
			rules: "/ci_* @owner\n",
			files: []fileTest{
				{"/ci_root.go", []string{"@owner"}},
				{"/src/ci_root.go", nil},
			},
		},
		{
			name:  "last exact rule wins",
			rules: "/src/ @directory\n/src/ci_* @prefix\n/src/ci_exact.go @exact\n",
			files: []fileTest{{"/src/ci_exact.go", []string{"@exact"}}},
		},
		{
			name:  "last prefix rule wins",
			rules: "/src/ @directory\n/src/ci_exact.go @exact\n/src/ci_* @prefix\n",
			files: []fileTest{{"/src/ci_exact.go", []string{"@prefix"}}},
		},
		{
			name:  "last directory rule wins",
			rules: "/src/ci_* @prefix\n/src/ @directory\n",
			files: []fileTest{{"/src/ci_exact.go", []string{"@directory"}}},
		},
	})
}

func TestGitHubRules(t *testing.T) {
	cases := []ruleTest{
		{
			name:  "GitHub Inline Comments And Email Owners",
			rules: "*       @global-owner1 @global-owner2\n*.js    @js-owner #This is an inline comment.\n*.go    docs@example.com\n",
			files: []fileTest{
				{"/app.js", []string{"@js-owner"}},
				{"/app.go", []string{"docs@example.com"}},
				{"/file.rb", []string{"@global-owner1", "@global-owner2"}},
			},
		},
		{
			name:  "GitHub Rooted Directory Pattern Matches Subdirectories Only At Root",
			rules: "/build/logs/ @doctocat\n",
			files: []fileTest{
				{"/build/logs/build-app/error.txt", []string{"@doctocat"}},
				{"/build/logs/error.txt", []string{"@doctocat"}},
				{"/x/build/logs/error.txt", nil},
			},
		},
		{
			name:  "GitHub Wildcard Segment Does Not Own Nested Files",
			rules: "*      @global\ndocs/* docs@example.com\n",
			files: []fileTest{
				{"/docs/getting-started.md", []string{"docs@example.com"}},
				{"/docs/build-app/troubleshooting.md", []string{"@global"}},
			},
		},
		{
			name:  "GitHub Directory Pattern Owns Everything Underneath",
			rules: "*      @global\n/docs/ @doctocat\n",
			files: []fileTest{
				{"/docs/getting-started.md", []string{"@doctocat"}},
				{"/docs/build-app/troubleshooting.md", []string{"@doctocat"}},
			},
		},
		{
			name:  "GitHub Directory And Terminal Globstar Patterns Require Descendants",
			rules: "/docs/      @directory\n/archive/** @globstar\n",
			files: []fileTest{
				{"/docs", nil},
				{"/docs/file.txt", []string{"@directory"}},
				{"/archive", nil},
				{"/archive/file.txt", []string{"@globstar"}},
				{"/archive/deep/file.txt", []string{"@globstar"}},
			},
		},
		{
			name:  "GitHub Rooted Versus Unrooted Patterns",
			rules: "/apps/ @root-apps",
			files: []fileTest{
				{"/apps/a.go", []string{"@root-apps"}},
				{"/x/apps/a.go", nil},
			},
		},
		{
			name:  "GitHub Rooted Versus Unrooted Patterns",
			rules: "apps/ @anywhere",
			files: []fileTest{
				{"/x/apps/a.go", []string{"@anywhere"}},
			},
		},
		{
			name:  "Patterns With Middle Slash Follow Dialect Semantics",
			rules: "*        @global\ndocs/*   @docs\na/**/b   @globstar\napps/    @apps\n**/logs  @logs\n",
			files: []fileTest{
				{"/docs/a.md", []string{"@docs"}},
				{"/examples/docs/a.md", []string{"@global"}},
				{"/a/b", []string{"@globstar"}},
				{"/a/x/b", []string{"@globstar"}},
				{"/x/a/b", []string{"@global"}},
				{"/x/apps/a.md", []string{"@apps"}},
				{"/x/logs/a.md", []string{"@logs"}},
			},
		},
		{
			name:  "GitHub Globstar Directory Pattern Owns Directory Contents",
			rules: "**/logs @octocat\n*.tmp   @temp-team\n",
			files: []fileTest{
				{"/build/logs/error.txt", []string{"@octocat"}},
				{"/deeply/nested/logs/x.txt", []string{"@octocat"}},
				{"/logs", []string{"@octocat"}},
				{"/catalog/data.tmp", []string{"@temp-team"}},
			},
		},
		{
			name:  "Ownerless Entry Leaves Subtree Unowned",
			rules: "*            @global-owner1 @global-owner2\n*.go         docs@example.com\n/apps/       @octocat\n/apps/github\n",
			files: []fileTest{
				{"/apps/github", nil},
				{"/apps/github/proj/main.go", nil},
				{"/other.go", []string{"docs@example.com"}},
			},
		},
		{
			name:  "Paths Are Case Sensitive",
			rules: "Readme.MD @docs\n*.Txt     @txt\n",
			files: []fileTest{
				{"/readme.md", nil},
				{"/Readme.MD", []string{"@docs"}},
				{"/a.txt", nil},
				{"/a.Txt", []string{"@txt"}},
			},
		},
		{
			name:  "Last Matching Rule Wins Globally For Git Hub",
			rules: "*.js @first\n*.js @second\n",
			files: []fileTest{
				{"/x/y.js", []string{"@second"}},
			},
		},
		{
			name:        "Git Lab Section Syntax Does Not Scope Git Hub Rules",
			rules:       "*.js          @first\n[Docs]        @ignored\n*.md          @docs\n*.js          @second\n",
			diagnostics: 1,
			files: []fileTest{
				{"/x/y.js", []string{"@second"}},
				{"/README.md", []string{"@docs"}},
			},
		},
		{
			name:  "Comment Lines With Leading Whitespace Are Ignored",
			rules: "   # indented comment\n   *.md @md-owner\n",
			files: []fileTest{
				{"/a.md", []string{"@md-owner"}},
				{"/#", nil},
			},
		},
		{
			name:  "Directory Patterns Match With Windows Separators",
			rules: "**/logs     @octocat\n/build/logs/ @doctocat\n",
			files: []fileTest{
				{"\\build\\logs\\error.txt", []string{"@doctocat"}},
				{"\\scripts\\logs\\x.txt", []string{"@octocat"}},
			},
		},
		{
			name:        "GitHub Backslash Escapes Metacharacters Spaces And Inline Comment Markers",
			rules:       "literal\\*.txt @star\nfile\\?.txt @question\nbracket\\[name\\].txt @bracket\npath\\ with\\ spaces/ @spaces\nmiddle\\#hash.txt @hash\ntrailing\\",
			diagnostics: 1,
			files: []fileTest{
				{"/literal*.txt", []string{"@star"}},
				{"/literal-value.txt", nil},
				{"/file?.txt", []string{"@question"}},
				{"/fileX.txt", nil},
				{"/bracket[name].txt", []string{"@bracket"}},
				{"/path with spaces/file.txt", []string{"@spaces"}},
				{"/middle#hash.txt", []string{"@hash"}},
			},
		},
		{
			name:        "Owner Validation Follows Dialect Rules And Does Not Apply Defaults To Malformed Explicit Owners",
			rules:       "*     @global\n*.go  docs@\n*.proto  @@maintainer\n",
			diagnostics: 2,
			files: []fileTest{
				{"/file.go", []string{"@global"}},
				{"/file.proto", []string{"@global"}},
			},
		},
		{
			name:        "Owner Extraction Rejects Impossible GitHub References And Canonicalizes Git Lab References",
			rules:       "*     @fallback\n*.go  @!\n*.proto  user@example.\n*.yaml  (@valid)\n*.ts  @bad+owner\n*.go  @org/team/nested\n",
			diagnostics: 5,
			files: []fileTest{
				{"/file.go", []string{"@fallback"}},
				{"/file.proto", []string{"@fallback"}},
				{"/file.yaml", []string{"@fallback"}},
				{"/file.ts", []string{"@fallback"}},
				{"/file.go", []string{"@fallback"}},
			},
		},
		{
			name:  "GitHub Accepts Enterprise Managed User Names",
			rules: "*.go @mona-cat_octo\n*.proto @octo_admin\n",
			files: []fileTest{
				{"/file.go", []string{"@mona-cat_octo"}},
				{"/file.proto", []string{"@octo_admin"}},
			},
		},
		{
			name:  "Escaped Slashes Remain Path Separators",
			rules: "dir\\/file.txt @file\ndocs\\/        @docs\n",
			files: []fileTest{
				{"/dir/file.txt", []string{"@file"}},
				{"/docs/guide.md", []string{"@docs"}},
				{"/nested/dir/file.txt", nil},
			},
		},
		{
			name:  "Former Globstar Sentinel Text Is Matched Literally",
			rules: "*              @global\n§§DOUBLESTAR§§ @literal\n",
			files: []fileTest{
				{"/§§DOUBLESTAR§§", []string{"@literal"}},
				{"/anything-else", []string{"@global"}},
			},
		},
		{
			name:        "Unsupported GitHub Syntax Is Ignored",
			rules:       "* @global\n!secret.txt @negation\nfile[0].go @range\n\\#hash.txt @hash\n",
			diagnostics: 3,
			files: []fileTest{
				{"/!secret.txt", []string{"@global"}},
				{"/file[0].go", []string{"@global"}},
				{"/#hash.txt", []string{"@global"}},
			},
		},
		{
			name:  "Real World Hashicorp Terraform Git Hub File",
			rules: "# The rules are evaluated in order, if a file matches multiple patterns, the last match \"wins\".\n* @hashicorp/terraform-core\n\n# Remote-state backend                           # Maintainer\n/internal/backend/remote-state/azure             @hashicorp/terraform-core @hashicorp/terraform-azure\n#/internal/backend/remote-state/consul           Unmaintained\n/internal/backend/remote-state/s3                @hashicorp/terraform-core @hashicorp/terraform-aws\n\n# Cloud backend\n/internal/backend/remote    @hashicorp/terraform-core @hashicorp/tf-core-cloud\n/internal/cloud             @hashicorp/terraform-core @hashicorp/tf-core-cloud\n\n# Provisioners\nbuiltin/provisioners/file               @hashicorp/terraform-core\nbuiltin/provisioners/local-exec         @hashicorp/terraform-core\n\n# Actions\n/internal/command/jsonplan/action_invocations.go @hashicorp/team-tf-actions @hashicorp/terraform-core",
			files: []fileTest{
				{"/main.go", []string{"@hashicorp/terraform-core"}},
				{"/internal/backend/remote-state/s3/backend.go", []string{"@hashicorp/terraform-core", "@hashicorp/terraform-aws"}},
				{"/internal/backend/remote-state/consul/backend.go", []string{"@hashicorp/terraform-core"}},
				{"/x/builtin/provisioners/file/resource.go", []string{"@hashicorp/terraform-core"}},
				{"/internal/command/jsonplan/action_invocations.go", []string{"@hashicorp/team-tf-actions", "@hashicorp/terraform-core"}},
				{"/internal/cloud/backend_run.go", []string{"@hashicorp/terraform-core", "@hashicorp/tf-core-cloud"}},
			},
		},
		{
			name:  "Multiple Leading Slashes Are Normalized",
			rules: "*.md   @md\n/docs/ @doctocat\n",
			files: []fileTest{
				{"//docs/getting-started.md", []string{"@doctocat"}},
				{"/docs/getting-started.md", []string{"@doctocat"}},
				{"", nil},
			},
		},
		{
			name:  "Null Path Returns No Owners",
			rules: "* @global\n",
			files: []fileTest{
				{"", nil},
			},
		},
		{
			name:  "Question Mark Does Not Match Slash",
			rules: "a?c @segment\n",
			files: []fileTest{
				{"/abc", []string{"@segment"}},
				{"/aXc", []string{"@segment"}},
				{"/a/c", nil},
			},
		},
		{
			name:  "Double Star Is Globstar Only As AWhole Segment",
			rules: "foo**bar @stars\n",
			files: []fileTest{
				{"/fooXbar", []string{"@stars"}},
			},
		},
		{
			name:  "Double Star Is Globstar Only As AWhole Segment",
			rules: "/foo**/bar @component-stars\n",
			files: []fileTest{
				{"/foo/bar", []string{"@component-stars"}},
				{"/fooX/bar", []string{"@component-stars"}},
				{"/foobar", nil},
				{"/foo/x/bar", nil},
			},
		},
		{
			name:  "Double Star Is Globstar Only As AWhole Segment",
			rules: "**/index.md @index\n",
			files: []fileTest{
				{"/docs/index.md", []string{"@index"}},
				{"/index.md", []string{"@index"}},
			},
		},
	}
	checkRules(t, GitHub, cases)
}
