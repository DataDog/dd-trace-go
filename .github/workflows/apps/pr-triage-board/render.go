// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// sourceURL points at the program that generates the board.
const sourceURL = "https://github.com/DataDog/dd-trace-go/tree/main/.github/workflows/apps/pr-triage-board"

// boardOptions are the inputs of the board that are not pull request data.
type boardOptions struct {
	Repos      []string       // repositories in display order
	TotalOpen  map[string]int // all open pull requests per repository
	Limit      int
	Now        time.Time
	Command    string // the command that produces the board
	Maintainer string // handle used to filter the board, if any
	Analyzed   int    // non-bot open pull requests analyzed, before the maintainer filter
}

// mentionPattern matches an @user or @org/team mention.
var mentionPattern = regexp.MustCompile(`@[A-Za-z0-9][A-Za-z0-9-]*(?:/[A-Za-z0-9_.-]+)?`)

// titleEscaper renders a pull request title as literal text inside one table
// cell. Backslash comes first so a pipe after a backslash stays escaped.
// Pull request titles are plain text, so every markdown marker is escaped.
var titleEscaper = strings.NewReplacer(
	`\`, `\\`,
	"|", `\|`,
	"*", `\*`,
	"_", `\_`,
	"`", "\\`",
	"[", `\[`,
	"]", `\]`,
	"~", `\~`,
	"<", "&lt;",
	">", "&gt;",
	"\n", " ",
	"\r", "",
)

// renderBoard renders the triage board as GitHub-flavored markdown. The output
// depends only on its arguments.
func renderBoard(items []Item, opts boardOptions) string {
	r := &renderer{opts: opts, multiRepo: len(opts.Repos) > 1}
	byBucket := make(map[Bucket][]Item)
	for _, it := range items {
		byBucket[it.Class.Bucket] = append(byBucket[it.Class.Bucket], it)
	}

	r.header(len(items))
	r.queueHealth(items)

	r.section(fmt.Sprintf("Quick Wins (<= %d LOC, Green CI, Clean Hygiene)", quickWinMaxLines),
		"Small pull requests with passing CI, clean hygiene and no maintainer review yet. Oldest first.",
		r.sorted(byBucket[BucketQuickWin], waitingKey),
		[]string{"PR", "Title", "Author", "Diff", "Waiting", "Owners"},
		func(it Item) []string {
			return []string{r.prCell(it), titleCell(it), authorCell(it), diffCell(it, false), r.age(it.Class.WaitingSince), ownersCell(it)}
		})

	r.section("Updated After Review (Author Pushed or Replied)",
		"A maintainer reviewed these. The author pushed or replied since, feedback is resolved and CI is green. Oldest first.",
		r.sorted(byBucket[BucketUpdatedAfterReview], waitingKey),
		[]string{"PR", "Title", "Author", "CI", "Diff", "Hygiene", "Updated", "Owners"},
		func(it Item) []string {
			return []string{r.prCell(it), titleCell(it), authorCell(it), ciCell(it), diffCell(it, false), hygieneCell(it), r.age(it.Class.WaitingSince), ownersCell(it)}
		})

	r.section("Blocked on Workflow Approval (First-Time Contributors)",
		"New contributors whose workflows wait for approval. Check the diff before you approve the run. Oldest first.",
		r.sorted(byBucket[BucketBlockedWorkflowApproval], waitingKey),
		[]string{"PR", "Title", "Author", "Diff", "Touches CI/Build?", "Hygiene", "Waiting", "Owners"},
		func(it Item) []string {
			touches := "No (code or docs)"
			if it.Class.TouchesBuild {
				touches = "**YES**"
			}
			return []string{r.prCell(it), titleCell(it), authorCell(it), diffCell(it, true), touches, hygieneCell(it), r.age(it.Class.WaitingSince), ownersCell(it)}
		})

	r.section("Waiting on Maintainer (Oldest First)",
		"Feedback is resolved or answered, CI is green and nothing conflicts. This section excludes Quick Wins and Updated After Review.",
		r.sorted(byBucket[BucketWaitingOnMaintainer], waitingKey),
		[]string{"PR", "Title", "Author", "CI", "Diff", "Hygiene", "Waiting Since", "Owners"},
		func(it Item) []string {
			since := fmt.Sprintf("%s (%s)", r.age(it.Class.WaitingSince), it.Class.WaitingSince.UTC().Format(time.DateOnly))
			return []string{r.prCell(it), titleCell(it), authorCell(it), ciCell(it), diffCell(it, true), hygieneCell(it), since, ownersCell(it)}
		})

	lastActorRow := func(it Item) []string {
		actor := "_none_"
		if it.Class.LastActor != "" {
			actor = fmt.Sprintf("`@%s` (%s)", it.Class.LastActor, it.Class.LastEvent)
		}
		return []string{r.prCell(it), titleCell(it), authorCell(it), actor, hygieneCell(it), r.age(it.PR.UpdatedAt), ownersCell(it)}
	}
	lastActorHeaders := []string{"PR", "Title", "Author", "Last Actor", "Hygiene", "Updated", "Owners"}
	r.collapsed("Waiting on Author (Changes Requested / Open Review Threads)",
		"A maintainer left feedback or requested changes. The author acts next.",
		r.sorted(byBucket[BucketWaitingOnAuthor], updatedKey), lastActorHeaders, lastActorRow)
	r.collapsed("Failing CI",
		"At least one blocking check fails.",
		r.sorted(byBucket[BucketFailingCI], updatedKey), lastActorHeaders, lastActorRow)
	r.collapsed("Needs Rebase (Merge Conflicts)",
		"The pull request conflicts with its base branch.",
		r.sorted(byBucket[BucketNeedsRebase], updatedKey), lastActorHeaders, lastActorRow)
	r.collapsed("Unclassified",
		"No rule matched. CI has usually not finished.",
		r.sorted(byBucket[BucketUnclassified], updatedKey), lastActorHeaders, lastActorRow)
	r.collapsed("Draft PRs",
		"Drafts are not ready for review.",
		r.sorted(byBucket[BucketDraft], createdKey),
		[]string{"PR", "Title", "Author", "Draft Age"},
		func(it Item) []string {
			return []string{r.prCell(it), titleCell(it), authorCell(it), r.age(it.PR.CreatedAt)}
		})

	return strings.TrimRight(r.sb.String(), "\n") + "\n"
}

type renderer struct {
	opts      boardOptions
	multiRepo bool
	sb        strings.Builder
}

func (r *renderer) line(format string, args ...any) {
	fmt.Fprintf(&r.sb, format, args...)
	r.sb.WriteByte('\n')
}

// header writes the board header. shown is the number of pull requests on the
// board, after the maintainer filter.
func (r *renderer) header(shown int) {
	r.line("> **Auto-updated snapshot**: `%s` · Analyzed `%d` non-bot open PRs (up to %d most recently active per repo).",
		r.opts.Now.UTC().Format("2006-01-02 15:04 UTC"), r.opts.Analyzed, r.opts.Limit)
	r.line(">")
	r.line("> **Automated via [`pr-triage-board`](%s)** · Generated by `%s`. The grouping is deterministic: fixed rules put each PR in the first matching bucket.", sourceURL, r.opts.Command)
	r.line(">")
	r.line("> PRs beyond the limit are the least recently active. Find them in the repository's pull request list.")
	if r.opts.Maintainer != "" {
		r.line(">")
		r.line("> Filtered to PRs where `@%s` is a current owner or a CODEOWNERS owner of the changed files.", strings.TrimPrefix(r.opts.Maintainer, "@"))
		r.line(">")
		r.line("> Showing `%d` of the analyzed non-bot open PRs.", shown)
	}
	r.line("")
}

var healthBuckets = []struct {
	title  string
	bucket Bucket
}{
	{"⚡ Quick Wins", BucketQuickWin},
	{"🔄 Updated After Review", BucketUpdatedAfterReview},
	{"⏸️ Blocked on Workflow Approval", BucketBlockedWorkflowApproval},
	{"👀 Waiting on Maintainer", BucketWaitingOnMaintainer},
	{"✍️ Waiting on Author", BucketWaitingOnAuthor},
	{"🔴 Failing CI", BucketFailingCI},
	{"⚠️ Needs Rebase", BucketNeedsRebase},
	{"❔ Unclassified", BucketUnclassified},
	{"📝 Draft", BucketDraft},
}

func (r *renderer) queueHealth(items []Item) {
	headers := make([]string, 0, len(healthBuckets)+2)
	headers = append(headers, "Repository")
	for _, hb := range healthBuckets {
		headers = append(headers, hb.title)
	}
	headers = append(headers, "**Analyzed (Total Open)**")

	row := func(label string, subset []Item, total int) []string {
		cells := []string{label}
		for _, hb := range healthBuckets {
			n := 0
			for _, it := range subset {
				if it.Class.Bucket == hb.bucket {
					n++
				}
			}
			cells = append(cells, strconv.Itoa(n))
		}
		return append(cells, fmt.Sprintf("**%d** (%d)", len(subset), total))
	}

	var rows [][]string
	sumTotal := 0
	for _, repo := range r.opts.Repos {
		var subset []Item
		for _, it := range items {
			if it.PR.Repo == repo {
				subset = append(subset, it)
			}
		}
		total := r.opts.TotalOpen[repo]
		sumTotal += total
		rows = append(rows, row("`"+repo+"`", subset, total))
	}
	if r.multiRepo {
		rows = append(rows, row("**Total**", items, sumTotal))
	}
	r.line("## Queue Health")
	r.line("")
	r.table(headers, rows)
	r.line("")
}

func (r *renderer) table(headers []string, rows [][]string) {
	r.line("| %s |", strings.Join(headers, " | "))
	r.line("|%s", strings.Repeat(" :--- |", len(headers)))
	for _, row := range rows {
		r.line("| %s |", strings.Join(row, " | "))
	}
}

func (r *renderer) section(title, description string, items []Item, headers []string, row func(Item) []string) {
	r.line("## %s", title)
	r.line("*%s*", description)
	r.line("")
	if len(items) == 0 {
		r.line("_No PRs currently in this bucket._")
	} else {
		r.table(headers, mapRows(items, row))
	}
	r.line("")
}

func (r *renderer) collapsed(title, description string, items []Item, headers []string, row func(Item) []string) {
	r.line("<details>")
	r.line("<summary><b>%s (%d)</b>: %s</summary>", title, len(items), description)
	r.line("")
	if len(items) == 0 {
		r.line("_None._")
	} else {
		r.table(headers, mapRows(items, row))
	}
	r.line("</details>")
	r.line("")
}

func mapRows(items []Item, row func(Item) []string) [][]string {
	rows := make([][]string, len(items))
	for i, it := range items {
		rows[i] = row(it)
	}
	return rows
}

type sortKey func(Item) time.Time

func waitingKey(it Item) time.Time { return it.Class.WaitingSince }
func updatedKey(it Item) time.Time { return it.PR.UpdatedAt }
func createdKey(it Item) time.Time { return it.PR.CreatedAt }

// sorted returns a copy of items ordered by key, oldest first. Ties break on
// repository order and pull request number.
func (r *renderer) sorted(items []Item, key sortKey) []Item {
	out := slices.Clone(items)
	slices.SortStableFunc(out, func(a, b Item) int {
		return cmp.Or(
			key(a).Compare(key(b)),
			cmp.Compare(slices.Index(r.opts.Repos, a.PR.Repo), slices.Index(r.opts.Repos, b.PR.Repo)),
			cmp.Compare(a.PR.Number, b.PR.Number),
		)
	})
	return out
}

func (r *renderer) age(t time.Time) string {
	days := max(int(r.opts.Now.Sub(t).Hours()/24), 0)
	if days == 0 {
		return "today"
	}
	return fmt.Sprintf("%dd", days)
}

func (r *renderer) prCell(it Item) string {
	label := fmt.Sprintf("#%d", it.PR.Number)
	if r.multiRepo {
		_, name, _ := strings.Cut(it.PR.Repo, "/")
		label = name + label
	}
	cell := fmt.Sprintf("[%s](%s)", label, it.PR.URL)
	if it.Class.Approved {
		cell += " *(approved)*"
	}
	return cell
}

// titleCell escapes the title and wraps mentions in backticks so that editing
// the issue does not notify anyone.
func titleCell(it Item) string {
	return mentionPattern.ReplaceAllString(titleEscaper.Replace(it.PR.Title), "`$0`")
}

func authorCell(it Item) string {
	return "`@" + it.PR.Author + "`"
}

func ciCell(it Item) string {
	switch it.Class.CI.State {
	case ciPass:
		return "`pass`"
	case ciFail:
		return "`fail`"
	}
	return "`running`"
}

func diffCell(it Item, withFiles bool) string {
	cell := fmt.Sprintf("`+%d/-%d`", it.PR.Additions, it.PR.Deletions)
	if withFiles {
		cell += fmt.Sprintf(" (%df)", it.PR.ChangedFiles)
	}
	return cell
}

func hygieneCell(it Item) string {
	if len(it.Class.Hygiene) == 0 {
		return "clean"
	}
	flags := make([]string, len(it.Class.Hygiene))
	for i, f := range it.Class.Hygiene {
		flags[i] = "`" + f + "`"
	}
	return strings.Join(flags, ", ")
}

// ownersCell lists the current owners and, when the pull request has none of
// the CODEOWNERS owners, a proposal.
func ownersCell(it Item) string {
	cell := "_none_"
	if len(it.Current) > 0 {
		cell = handles(it.Current)
	}
	if len(it.Proposed) > 0 {
		cell += "<br>↳ **Proposed:** " + handles(it.Proposed)
	}
	return cell
}

func handles(list []string) string {
	list = list[:min(len(list), maxListedOwners)]
	out := make([]string, len(list))
	for i, h := range list {
		out[i] = "`@" + h + "`"
	}
	return strings.Join(out, ", ")
}
