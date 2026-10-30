// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package codeownership

import "testing"

func TestGitLabRules(t *testing.T) {
	cases := []ruleTest{
		{
			name:  "GitLab Terminal Globstar Matches One Path Segment",
			rules: "/archive/** @single-segment\n",
			files: []fileTest{
				{"/archive", nil},
				{"/archive/file.txt", []string{"@single-segment"}},
				{"/archive/deep/file.txt", nil},
			},
		},
		{
			name:  "GitLab Terminal Globstar Matches One Path Segment",
			rules: "/archive/**/* @recursive\n",
			files: []fileTest{
				{"/archive/deep/file.txt", []string{"@recursive"}},
			},
		},
		{
			name:  "GitLab Globstar Before Trailing Slash Matches Immediate And Nested Children",
			rules: "/archive/**/ @owner\n",
			files: []fileTest{
				{"/archive", nil},
				{"/archive/file.txt", []string{"@owner"}},
				{"/archive/deep/file.txt", []string{"@owner"}},
			},
		},
		{
			name:  "Patterns With Middle Slash Follow Dialect Semantics",
			rules: "*        @global\ndocs/*   @docs\na/**/b   @globstar\napps/    @apps\n**/logs  @logs\n",
			files: []fileTest{
				{"/docs/a.md", []string{"@docs"}},
				{"/examples/docs/a.md", []string{"@docs"}},
				{"/x/a/b", []string{"@globstar"}},
			},
		},
		{
			name:  "Globstar Matches Zero Directories",
			rules: "/db/**/index.md @index-docs\n/docs/**/*.md   @markdown-docs\n",
			files: []fileTest{
				{"/db/index.md", []string{"@index-docs"}},
				{"/db/v2/index.md", []string{"@index-docs"}},
				{"/docs/index.md", []string{"@markdown-docs"}},
				{"/docs/api/graphql/index.md", []string{"@markdown-docs"}},
				{"/docs/api/index.xml", nil},
			},
		},
		{
			name:  "Relative Paths Match At Any Depth",
			rules: "internal/README.md @user4\n",
			files: []fileTest{
				{"/internal/README.md", []string{"@user4"}},
				{"/docs/api/internal/README.md", []string{"@user4"}},
				{"/docs/README.md", nil},
			},
		},
		{
			name:  "Git Lab Sections Are Evaluated Independently And Combined",
			rules: "* @admin\n\n[README Owners]\nREADME.md @user1 @user2\ninternal/README.md @user4\n\n[README other owners]\nREADME.md @user3",
			files: []fileTest{
				{"/README.md", []string{"@admin", "@user1", "@user2", "@user3"}},
				{"/internal/README.md", []string{"@admin", "@user4", "@user3"}},
			},
		},
		{
			name:  "Git Lab Section Defaults Apply Only After ARule Matches",
			rules: "[Section] @team\n/src/ @owner\n",
			files: []fileTest{
				{"/src/code.go", []string{"@owner"}},
				{"/other/file.go", nil},
			},
		},
		{
			name:  "Section Default Owners Apply Only To Entries Without Explicit Owners",
			rules: "[Database] @database-team @agarcia\nmodel/db/\nconfig/db/database-setup.md @docs-team",
			files: []fileTest{
				{"/model/db/schema.rb", []string{"@database-team", "@agarcia"}},
				{"/config/db/database-setup.md", []string{"@docs-team"}},
				{"/other/file.txt", nil},
			},
		},
		{
			name:  "Optional Sections And Approval Counts Do Not Affect Matching",
			rules: "^[Go]\n*.go @go-owner\n[Big][5]\nbig/ @big-owner\n[Team] @default-team\nteam/\n",
			files: []fileTest{
				{"/x.go", []string{"@go-owner"}},
				{"/big/file.txt", []string{"@big-owner"}},
				{"/team/file.txt", []string{"@default-team"}},
			},
		},
		{
			name:  "Role Owners Are Kept As Owners",
			rules: "/config/setup.yml @@maintainer\n",
			files: []fileTest{
				{"/config/setup.yml", []string{"@@maintainer"}},
			},
		},
		{
			name:  "Exclusions Are Sticky Within Section",
			rules: "*             @default-owner\n!*.rb\n/special/*.rb @ruby-owner\n",
			files: []fileTest{
				{"/special/foo.rb", nil},
				{"/code.rb", nil},
				{"/other.txt", []string{"@default-owner"}},
			},
		},
		{
			name:  "Exclusions Apply Per Section",
			rules: "[Ruby]\n*.rb @ruby-team\n!/config/**/*.rb\n\n[Config]\n/config/ @ops-team",
			files: []fileTest{
				{"/config/routes.rb", []string{"@ops-team"}},
				{"/lib/foo.rb", []string{"@ruby-team"}},
				{"/config/other.xml", []string{"@ops-team"}},
			},
		},
		{
			name:        "Inline Comments Are Unsupported In Git Lab",
			rules:       "*.rb @ruby-owner # note to self\n",
			diagnostics: 1,
			files: []fileTest{
				{"/a.rb", []string{"@ruby-owner"}},
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
			name:  "Duplicate Entries Use Last Within Section",
			rules: "README.md @old\nREADME.md @new\n",
			files: []fileTest{
				{"/README.md", []string{"@new"}},
			},
		},
		{
			name:  "Git Lab Last Duplicate Pattern Replaces Earlier Entries Including Exclusions",
			rules: "[Ruby]\n*.rb @old\n!*.rb\n*.rb @new\n",
			files: []fileTest{
				{"/model.rb", []string{"@new"}},
			},
		},
		{
			name:  "Git Lab Last Duplicate Pattern Replaces Earlier Entries Including Exclusions",
			rules: "* @old\n!/**/*\n* @new\n",
			files: []fileTest{
				{"/nested/file.go", []string{"@new"}},
			},
		},
		{
			name:  "Git Lab Duplicate Normalization Unescapes Leading Hash Before Replacement",
			rules: "!#file\n\\#file @new\n",
			files: []fileTest{
				{"/#file", []string{"@new"}},
			},
		},
		{
			name:  "Git Lab Duplicate Normalization Unescapes Leading Hash Before Replacement",
			rules: "\\#file @old\n!#file\n",
			files: []fileTest{
				{"/#file", nil},
			},
		},
		{
			name:  "Git Lab Character Classes Support Sets Ranges And Negation Without Crossing Directories",
			rules: "digit[0-9].txt     @digit\nletter[ab].txt     @letter\nnon-digit[!0-9].txt @non-digit\npath[!x]file       @same-segment\n",
			files: []fileTest{
				{"/digit7.txt", []string{"@digit"}},
				{"/digitx.txt", nil},
				{"/lettera.txt", []string{"@letter"}},
				{"/letterc.txt", nil},
				{"/non-digita.txt", []string{"@non-digit"}},
				{"/non-digit7.txt", nil},
				{"/path/file", nil},
			},
		},
		{
			name:        "Git Lab Malformed Character Classes Cannot Abort Parser Initialization",
			rules:       "*               @fallback\nfile[z-a].txt   @invalid-range\nbroken\\\nfile[---!].txt  @punctuation\n",
			diagnostics: 2,
			files: []fileTest{
				{"/filex.txt", []string{"@fallback"}},
				{"/file-.txt", []string{"@punctuation"}},
				{"/file!.txt", []string{"@punctuation"}},
			},
		},
		{
			name:        "Git Lab Character Classes Cannot Start With Closing Bracket",
			rules:       "*              @fallback\nfile[]a].txt   @positive\nfile[!]].txt   @negated\n",
			diagnostics: 2,
			files: []fileTest{
				{"/filea.txt", []string{"@fallback"}},
				{"/file].txt", []string{"@fallback"}},
				{"/filex.txt", []string{"@fallback"}},
			},
		},
		{
			name:  "Git Lab Backslash Escapes Glob Metacharacters And Ordinary Characters",
			rules: "file\\?.txt @question\nliteral\\*.txt @star\nletter\\a.txt @ordinary\nbracket[\\]].txt @closing-bracket\nhyphen[\\-].txt @hyphen",
			files: []fileTest{
				{"/file?.txt", []string{"@question"}},
				{"/fileX.txt", nil},
				{"/literal*.txt", []string{"@star"}},
				{"/literal-value.txt", nil},
				{"/lettera.txt", []string{"@ordinary"}},
				{"/bracket].txt", []string{"@closing-bracket"}},
				{"/hyphen-.txt", []string{"@hyphen"}},
			},
		},
		{
			name:        "Whitespace Only Git Lab Section Header Starts Invalid Named Section",
			rules:       "[Docs] @docs\n[   ]  @blank\nREADME.md\n",
			diagnostics: 1,
			files: []fileTest{
				{"/README.md", []string{"@blank"}},
				{"/guide.md", nil},
			},
		},
		{
			name:        "Unparsable Git Lab Section Header Is Skipped Instead Of Becoming Pattern",
			rules:       "* @global\n[Broken\n",
			diagnostics: 1,
			files: []fileTest{
				{"/[Broken", []string{"@global"}},
			},
		},
		{
			name:        "Git Lab Malformed Section Suffix Cannot Leak Default Owners",
			rules:       "[Docs]] @leaked\nREADME.md\n",
			diagnostics: 2,
			files: []fileTest{
				{"/README.md", nil},
			},
		},
		{
			name:        "Git Lab Malformed Section Suffix Cannot Leak Default Owners",
			rules:       "[Docs][x] @leaked\nREADME.md\n",
			diagnostics: 2,
			files: []fileTest{
				{"/README.md", nil},
			},
		},
		{
			name:        "Git Lab Permissive Section Parsing Keeps Only The Recognized Owner Span",
			rules:       "[Docs][2]@owner\nREADME.md\n",
			diagnostics: 1,
			files: []fileTest{
				{"/README.md", []string{"@owner"}},
			},
		},
		{
			name:  "Git Lab Recognized Roles Are Kept As Owners",
			rules: "*.go @@developer\n",
			files: []fileTest{
				{"/file.go", []string{"@@developer"}},
			},
		},
		{
			name:  "Git Lab Recognized Roles Are Kept As Owners",
			rules: "*.go @@developers\n",
			files: []fileTest{
				{"/file.go", []string{"@@developers"}},
			},
		},
		{
			name:  "Git Lab Recognized Roles Are Kept As Owners",
			rules: "*.go @@maintainer\n",
			files: []fileTest{
				{"/file.go", []string{"@@maintainer"}},
			},
		},
		{
			name:  "Git Lab Recognized Roles Are Kept As Owners",
			rules: "*.go @@maintainers\n",
			files: []fileTest{
				{"/file.go", []string{"@@maintainers"}},
			},
		},
		{
			name:  "Git Lab Recognized Roles Are Kept As Owners",
			rules: "*.go @@owner\n",
			files: []fileTest{
				{"/file.go", []string{"@@owner"}},
			},
		},
		{
			name:  "Git Lab Recognized Roles Are Kept As Owners",
			rules: "*.go @@OwNeRs\n",
			files: []fileTest{
				{"/file.go", []string{"@@OwNeRs"}},
			},
		},
		{
			name:        "Git Lab Unknown Roles Are Ignored",
			rules:       "*.go @@banana @valid\n",
			diagnostics: 1,
			files: []fileTest{
				{"/file.go", []string{"@valid"}},
			},
		},
		{
			name:        "Owner Validation Follows Dialect Rules And Does Not Apply Defaults To Malformed Explicit Owners",
			rules:       "[Docs] @default malformed@\n*.md malformed@ @valid\nREADME.md malformed@\nGUIDE.md\n",
			diagnostics: 3,
			files: []fileTest{
				{"/other.md", []string{"@valid"}},
				{"/README.md", nil},
				{"/GUIDE.md", []string{"@default"}},
			},
		},
		{
			name:        "Owner Extraction Rejects Impossible GitHub References And Canonicalizes Git Lab References",
			rules:       "*.go  @! @good\n*.md  (@docs)\n*.txt docs@example.\n*.proto  @group/nested-team\n",
			diagnostics: 1,
			files: []fileTest{
				{"/file.go", []string{"@good"}},
				{"/file.md", []string{"@docs"}},
				{"/file.txt", []string{"docs@example"}},
				{"/file.proto", []string{"@group/nested-team"}},
			},
		},
		{
			name:  "Git Lab Namespace References May End With Hyphens",
			rules: "*.go @team-\n*.proto (@group-/subgroup-)\n",
			files: []fileTest{
				{"/file.go", []string{"@team-"}},
				{"/file.proto", []string{"@group-/subgroup-"}},
			},
		},
		{
			name:  "Git Lab Reference Extraction Scans Names Roles And Emails Independently",
			rules: "*.go docs@example.com,@alice\n*.proto alice@example.com!alias\n*.yaml (@@maintainer\n",
			files: []fileTest{
				{"/file.go", []string{"@alice", "docs@example.com"}},
				{"/file.proto", []string{"alice@example.com!alias"}},
				{"/file.yaml", []string{"@@maintainer"}},
			},
		},
		{
			name:  "Git Lab Exclusions Ignore Owner Text For Diagnostics",
			rules: "!*.go definitely-not-an-owner\n",
			files: []fileTest{
				{"/file.go", nil},
			},
		},
		{
			name:        "Git Lab Ownerless Entries Are Diagnosed Without Changing Matching Semantics",
			rules:       "*.md\n",
			diagnostics: 1,
			files: []fileTest{
				{"/README.md", nil},
			},
		},
		{
			name:  "Escaped Slashes Remain Path Separators",
			rules: "dir\\/file.txt @file\ndocs\\/        @docs\n",
			files: []fileTest{
				{"/dir/file.txt", []string{"@file"}},
				{"/docs/guide.md", []string{"@docs"}},
				{"/nested/dir/file.txt", []string{"@file"}},
			},
		},
		{
			name:  "Git Lab Duplicate Sections Are Combined Case Insensitively",
			rules: "[Docs]\n*.md @old\n[DOCS]\nREADME.md @new\n",
			files: []fileTest{
				{"/README.md", []string{"@new"}},
				{"/guide.md", []string{"@old"}},
			},
		},
		{
			name:  "Git Lab Duplicate Section Exclusions Are Sticky Across Occurrences",
			rules: "[Ruby]\n*.rb @ruby-team\n[RUBY]\n!/config/**/*.rb\n/config/routes.rb @ops\n",
			files: []fileTest{
				{"/lib/model.rb", []string{"@ruby-team"}},
				{"/config/routes.rb", nil},
			},
		},
		{
			name:  "Git Lab Duplicate Section Defaults Apply To Entries Under Each Header",
			rules: "[Docs] @old-default\n*.md\n[DOCS] @new-default\nREADME.md\n",
			files: []fileTest{
				{"/guide.md", []string{"@old-default"}},
				{"/README.md", []string{"@new-default"}},
			},
		},
		{
			name:  "Real World Git Lab Sectioned File",
			rules: "[Maintainers] @gl-dx/maintainers @gitlab-org/maintainers/rails-backend\n*\n\n/* @gitlab-org/maintainers/frontend @gitlab-org/maintainers/database\n*.rb @gitlab-org/maintainers/rails-backend\n/app/ @gitlab-org/maintainers/rails-backend\n/workhorse/ @gitlab-org/maintainers/gitlab-workhorse\n\n^[Database] @gitlab-org/maintainers/database\n/spec/lib/gitlab/background_migration/\n\n^[Frontend dependency patches] @markrian @xanf @thutterer\n/patches/",
			files: []fileTest{
				{"/random/path.txt", []string{"@gl-dx/maintainers", "@gitlab-org/maintainers/rails-backend"}},
				{"/README.md", []string{"@gitlab-org/maintainers/frontend", "@gitlab-org/maintainers/database"}},
				{"/app/models/user.rb", []string{"@gitlab-org/maintainers/rails-backend"}},
				{"/workhorse/Makefile", []string{"@gitlab-org/maintainers/gitlab-workhorse"}},
				{"/spec/lib/gitlab/background_migration/foo_spec.rb", []string{"@gitlab-org/maintainers/rails-backend", "@gitlab-org/maintainers/database"}},
				{"/patches/foo.diff", []string{"@gl-dx/maintainers", "@gitlab-org/maintainers/rails-backend", "@markrian", "@xanf", "@thutterer"}},
			},
		},
	}
	checkRules(t, GitLab, cases)
}
