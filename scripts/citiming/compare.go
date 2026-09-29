// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"cmp"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
)

// Acceptance minima from the measurement plan: a stratum is comparable
// with at least minRunSamples distinct successful first-attempt run IDs
// per window, and PR feedback needs minRevisions measured revisions per
// selection signature per window. The aggregate is computed over the
// comparable strata and refused when they carry less than minCoverage of
// the baseline weight.
const (
	minRunSamples = 5
	minRevisions  = 5
	minCoverage   = 0.80
)

// stratum is one frozen comparison cell: a job on one kind of runner. It
// deliberately holds nothing the candidate treatment changes (workload
// family, resolved Go version), so baseline and candidate records of the
// same job land in the same cell. The logical job name carries the matrix
// dimensions (e.g. "test-contrib-matrix (chunk 3/6)"), so matrix variants
// do not collapse into one stratum. Strata and weights come from the
// baseline window.
type stratum struct {
	workflow string
	job      string
	runner   string
}

func stratify(rec jobObservation) stratum {
	labels := slices.Sorted(slices.Values(rec.Job.RunnerLabels))
	return stratum{
		workflow: rec.Run.WorkflowFile,
		job:      rec.Job.LogicalName,
		runner:   rec.Job.RunnerGroup + "|" + strings.Join(labels, ","),
	}
}

func (s stratum) String() string {
	return s.workflow + " / " + s.job + " / " + s.runner
}

func sortStrata(values []stratum) {
	slices.SortFunc(values, func(a, b stratum) int {
		return cmp.Or(
			cmp.Compare(a.workflow, b.workflow),
			cmp.Compare(a.job, b.job),
			cmp.Compare(a.runner, b.runner),
		)
	})
}

// median returns the median of values, or nil for an empty slice.
func median(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	n := len(sorted)
	if n%2 == 1 {
		return &sorted[n/2]
	}
	both := (sorted[n/2-1] + sorted[n/2]) / 2
	return &both
}

// percentile returns the p-quantile of already-sorted values.
func percentile(sorted []float64, p float64) *float64 {
	if len(sorted) == 0 {
		return nil
	}
	rank := float64(len(sorted)-1) * p
	low := int(rank)
	high := low + 1
	if high >= len(sorted) {
		return &sorted[low]
	}
	frac := rank - float64(low)
	value := sorted[low] + (sorted[high]-sorted[low])*frac
	return &value
}

type csvWriter struct {
	w *csv.Writer
}

func newCSVWriter(f io.Writer) *csvWriter {
	return &csvWriter{w: csv.NewWriter(f)}
}

func (c *csvWriter) write(fields ...string) {
	// Errors surface through err() on flush; per-field writes never fail
	// for in-memory strings.
	_ = c.w.Write(fields)
}

func (c *csvWriter) err() error {
	c.w.Flush()
	return c.w.Error()
}

// feedbackRow is one PR-feedback signature comparison row. excluded is
// empty when the signature is comparable.
type feedbackRow struct {
	sig              string
	baseN, candN     int
	baseP50, candP50 *float64
	excluded         string
}

// perfSplit splits successful first attempts from everything else and
// aggregates the cache evidence alongside the durations. Skipped jobs are
// absent work, not zero-duration successes; failures and retries are
// reported separately, never in the performance table. Timing and
// non-success counts use first attempts regardless of later attempts.
type perfSplit struct {
	values     map[stratum][]float64
	runIDs     map[stratum]map[int64]bool
	goVersions map[stratum]map[string]bool // known resolved versions only
	cache      map[stratum]*cacheAgg
	failures   map[stratum][]string
	retried    map[stratum]map[int64]bool // runs with a later attempt
}

// cacheAgg summarizes one stratum's successful first attempts: restore
// classification counts, save failures, and post-phase durations.
type cacheAgg struct {
	restores int
	exact    int
	unknown  int
	saveErrs int
	post     []float64
}

// nonExact counts restores that were neither exact hits nor
// unclassifiable: prefix restores, cold misses, disabled caches and
// errors all mean the workload was not served from an exact snapshot.
func (a *cacheAgg) nonExact() int {
	return a.restores - a.exact - a.unknown
}

func perfValues(records []jobObservation) perfSplit {
	split := perfSplit{
		values:     map[stratum][]float64{},
		runIDs:     map[stratum]map[int64]bool{},
		goVersions: map[stratum]map[string]bool{},
		cache:      map[stratum]*cacheAgg{},
		failures:   map[stratum][]string{},
		retried:    map[stratum]map[int64]bool{},
	}
	for _, rec := range records {
		if rec.Run.Attempt != 1 {
			continue
		}
		key := stratify(rec)
		switch {
		case rec.Job.Conclusion == "success" && rec.Job.Seconds != nil:
			split.values[key] = append(split.values[key], *rec.Job.Seconds)
			if split.runIDs[key] == nil {
				split.runIDs[key] = map[int64]bool{}
			}
			split.runIDs[key][rec.Run.ID] = true
			if v := rec.Workload.ResolvedGoVersion; v != "" {
				if split.goVersions[key] == nil {
					split.goVersions[key] = map[string]bool{}
				}
				split.goVersions[key][v] = true
			}
			agg := split.cache[key]
			if agg == nil {
				agg = &cacheAgg{}
				split.cache[key] = agg
			}
			for _, r := range rec.Cache.Restores {
				agg.restores++
				switch r.Result {
				case "exact":
					agg.exact++
				case "unknown":
					agg.unknown++
				}
			}
			for _, s := range rec.Cache.Saves {
				if s == "error" || s == "conflict" {
					agg.saveErrs++
				}
			}
			if rec.Cache.PostSeconds != nil {
				agg.post = append(agg.post, *rec.Cache.PostSeconds)
			}
		case rec.Job.Conclusion != "skipped" && rec.Job.Conclusion != "":
			split.failures[key] = append(split.failures[key], rec.Job.Conclusion)
		}
		if rec.Run.Retried && rec.Job.Conclusion != "skipped" {
			if split.retried[key] == nil {
				split.retried[key] = map[int64]bool{}
			}
			split.retried[key][rec.Run.ID] = true
		}
	}
	return split
}

// cacheRow carries the cache evidence for one stratum from both
// windows into the reports.
type cacheRow struct {
	stratum stratum
	baseRestores, baseNonExact, baseUnknown,
	baseSaveErrs int
	candRestores, candNonExact, candUnknown,
	candSaveErrs int
	basePostP50, candPostP50 *float64
}

func cacheBehaviorRows(base, cand perfSplit) []cacheRow {
	keys := map[stratum]bool{}
	for k := range base.cache {
		keys[k] = true
	}
	for k := range cand.cache {
		keys[k] = true
	}
	list := make([]stratum, 0, len(keys))
	for k := range keys {
		list = append(list, k)
	}
	sortStrata(list)
	rows := make([]cacheRow, 0, len(list))
	for _, k := range list {
		row := cacheRow{stratum: k}
		if a := base.cache[k]; a != nil {
			row.baseRestores = a.restores
			row.baseNonExact = a.nonExact()
			row.baseUnknown = a.unknown
			row.baseSaveErrs = a.saveErrs
			row.basePostP50 = median(a.post)
		}
		if a := cand.cache[k]; a != nil {
			row.candRestores = a.restores
			row.candNonExact = a.nonExact()
			row.candUnknown = a.unknown
			row.candSaveErrs = a.saveErrs
			row.candPostP50 = median(a.post)
		}
		rows = append(rows, row)
	}
	return rows
}

func feedbackBySignature(records []prFeedback) map[string][]float64 {
	groups := map[string][]float64{}
	for _, rec := range records {
		if rec.Seconds == nil {
			continue
		}
		groups[rec.SelectionSignature] = append(groups[rec.SelectionSignature], *rec.Seconds)
	}
	return groups
}

// comparisonRow is one baseline stratum. excluded is empty when the
// stratum is comparable and names the reason otherwise.
type comparisonRow struct {
	stratum              stratum
	baselineN, candN     int
	baseRuns, candRuns   int
	baselineP50, baseP95 *float64
	candP50, candP95     *float64
	excluded             string
}

// toolchainChanged reports whether both windows observed known Go
// versions for a stratum and the sets differ. An unknown (empty) version
// never counts as a change.
func toolchainChanged(base, cand map[string]bool) bool {
	return len(base) > 0 && len(cand) > 0 && !maps.Equal(base, cand)
}

func versionList(set map[string]bool) string {
	return strings.Join(slices.Sorted(maps.Keys(set)), ",")
}

// aggregate is the baseline-weighted job-minutes headline: every
// comparable unit (stratum or PR signature) contributes weight * p50, with
// the weight taken from the baseline window so a change in job mix cannot
// manufacture an improvement.
type aggregate struct {
	weight, eligibleWeight float64
	baseTotal, candTotal   float64
}

func (a *aggregate) add(weight int, eligible bool, baseP50, candP50 *float64) {
	w := float64(weight)
	a.weight += w
	if !eligible {
		return
	}
	a.eligibleWeight += w
	a.baseTotal += w * *baseP50
	a.candTotal += w * *candP50
}

func (a aggregate) coverage() float64 {
	if a.weight == 0 {
		return 0
	}
	return a.eligibleWeight / a.weight
}

// refusal explains why the aggregate cannot be reported, or returns "".
func (a aggregate) refusal() string {
	switch {
	case a.weight == 0:
		return "no baseline observations"
	case a.coverage() < minCoverage:
		return fmt.Sprintf("covered share %.1f%% is below the minimum of %.0f%%",
			a.coverage()*100, minCoverage*100)
	}
	return ""
}

func (a aggregate) improvement() *float64 {
	if a.refusal() != "" {
		return nil
	}
	return improvementOf(&a.baseTotal, &a.candTotal)
}

// nonSuccessRow counts first-attempt non-success outcomes and retried
// runs (runs with a later attempt) for one stratum.
type nonSuccessRow struct {
	stratum               stratum
	baseFail, baseRetried int
	candFail, candRetried int
}

func cmdCompare(args []string) error {
	flags := flagSet("compare")
	baselineDir := flags.String("baseline", "", "baseline output dir")
	candidateDir := flags.String("candidate", "", "candidate output dir")
	outputDir := flags.String("output-dir", "", "directory for comparison reports")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *baselineDir == "" || *candidateDir == "" || *outputDir == "" {
		return errors.New("compare needs --baseline, --candidate and --output-dir")
	}

	baseJobs, baseFeedback, err := loadObservations(*baselineDir)
	if err != nil {
		return err
	}
	candJobs, candFeedback, err := loadObservations(*candidateDir)
	if err != nil {
		return err
	}

	baseSplit := perfValues(slices.Collect(maps.Values(baseJobs)))
	candSplit := perfValues(slices.Collect(maps.Values(candJobs)))

	var jobAgg aggregate
	rows := make([]comparisonRow, 0, len(baseSplit.values))
	for _, key := range sortedKeys(baseSplit.values) {
		b := slices.Clone(baseSplit.values[key])
		slices.Sort(b)
		c := slices.Clone(candSplit.values[key])
		slices.Sort(c)
		row := comparisonRow{
			stratum:     key,
			baselineN:   len(b),
			candN:       len(c),
			baseRuns:    len(baseSplit.runIDs[key]),
			candRuns:    len(candSplit.runIDs[key]),
			baselineP50: percentile(b, 0.50),
			baseP95:     percentile(b, 0.95),
			candP50:     percentile(c, 0.50),
			candP95:     percentile(c, 0.95),
		}
		baseVersions, candVersions := baseSplit.goVersions[key], candSplit.goVersions[key]
		switch {
		case len(c) == 0:
			row.excluded = "no candidate data"
		case toolchainChanged(baseVersions, candVersions):
			row.excluded = fmt.Sprintf("toolchain changed (baseline %s; candidate %s)",
				versionList(baseVersions), versionList(candVersions))
		// Matrix jobs from a single run must not stand in for the
		// five independent run IDs the acceptance criteria require.
		case row.baseRuns < minRunSamples || row.candRuns < minRunSamples:
			row.excluded = fmt.Sprintf("below %d distinct runs (%d baseline, %d candidate)",
				minRunSamples, row.baseRuns, row.candRuns)
		}
		jobAgg.add(len(b), row.excluded == "", row.baselineP50, row.candP50)
		rows = append(rows, row)
	}

	baseFB := feedbackBySignature(slices.Collect(maps.Values(baseFeedback)))
	candFB := feedbackBySignature(slices.Collect(maps.Values(candFeedback)))
	var fbAgg aggregate
	fbRows := make([]feedbackRow, 0, len(baseFB))
	for _, sig := range sortedSigKeys(baseFB) {
		b := slices.Clone(baseFB[sig])
		slices.Sort(b)
		c := slices.Clone(candFB[sig])
		slices.Sort(c)
		row := feedbackRow{
			sig:     sig,
			baseN:   len(b),
			candN:   len(c),
			baseP50: percentile(b, 0.50),
			candP50: percentile(c, 0.50),
		}
		// The sample minimum applies to measured revisions: feedback
		// records without a duration cannot support the median.
		switch {
		case len(c) == 0:
			row.excluded = "no candidate revisions"
		case len(b) < minRevisions || len(c) < minRevisions:
			row.excluded = fmt.Sprintf("%d baseline, %d candidate measured revisions (minimum %d)",
				len(b), len(c), minRevisions)
		}
		fbAgg.add(len(b), row.excluded == "", row.baseP50, row.candP50)
		fbRows = append(fbRows, row)
	}

	cacheRows := cacheBehaviorRows(baseSplit, candSplit)

	if err := os.MkdirAll(*outputDir, 0o755); err != nil {
		return err
	}
	if err := writeComparisonCSV(*outputDir, rows, cacheRows, fbRows); err != nil {
		return err
	}
	data := reportData{
		rows:       rows,
		cacheRows:  cacheRows,
		jobAgg:     jobAgg,
		nonSuccess: nonSuccessRows(baseSplit, candSplit),
		fbRows:     fbRows,
		fbAgg:      fbAgg,
	}
	report := renderReport(data)
	if err := os.WriteFile(*outputDir+"/comparison.md", []byte(report), 0o644); err != nil {
		return err
	}
	fmt.Fprint(os.Stdout, report)
	fmt.Fprintf(os.Stdout, "\nWrote %s/comparison.md and %s/comparison.csv\n", *outputDir, *outputDir)
	if jobAgg.improvement() == nil {
		msg := "comparison not computable"
		if reason := jobAgg.refusal(); reason != "" {
			msg += ": " + reason
		}
		return errors.New(msg)
	}
	return nil
}

// nonSuccessRows lists every stratum with a first-attempt non-success
// outcome or a retried run in either window, including strata that never
// produced a timing sample.
func nonSuccessRows(base, cand perfSplit) []nonSuccessRow {
	keys := map[stratum]bool{}
	for _, split := range []perfSplit{base, cand} {
		for k := range split.failures {
			keys[k] = true
		}
		for k := range split.retried {
			keys[k] = true
		}
	}
	rows := make([]nonSuccessRow, 0, len(keys))
	for _, k := range slices.Collect(maps.Keys(keys)) {
		rows = append(rows, nonSuccessRow{
			stratum:     k,
			baseFail:    len(base.failures[k]),
			baseRetried: len(base.retried[k]),
			candFail:    len(cand.failures[k]),
			candRetried: len(cand.retried[k]),
		})
	}
	slices.SortFunc(rows, func(a, b nonSuccessRow) int {
		return cmp.Or(
			cmp.Compare(a.stratum.workflow, b.stratum.workflow),
			cmp.Compare(a.stratum.job, b.stratum.job),
			cmp.Compare(a.stratum.runner, b.stratum.runner))
	})
	return rows
}

func improvementOf(base, cand *float64) *float64 {
	if base == nil || cand == nil || *base == 0 {
		return nil
	}
	value := 1 - *cand / *base
	return &value
}

func sortedKeys(values map[stratum][]float64) []stratum {
	keys := slices.Collect(maps.Keys(values))
	sortStrata(keys)
	return keys
}

func sortedSigKeys(values map[string][]float64) []string {
	return slices.Sorted(maps.Keys(values))
}

// reportData bundles everything the rendered report needs.
type reportData struct {
	rows       []comparisonRow
	cacheRows  []cacheRow
	jobAgg     aggregate
	nonSuccess []nonSuccessRow
	fbRows     []feedbackRow
	fbAgg      aggregate
}

func writeComparisonCSV(dir string, rows []comparisonRow,
	cacheRows []cacheRow, fbRows []feedbackRow) error {

	cacheByStratum := map[stratum]cacheRow{}
	for _, row := range cacheRows {
		cacheByStratum[row.stratum] = row
	}
	f, err := os.Create(dir + "/comparison.csv")
	if err != nil {
		return err
	}
	defer f.Close()
	w := newCSVWriter(f)
	w.write("workflow_file", "job", "runner",
		"baseline_n", "candidate_n",
		"baseline_runs", "candidate_runs",
		"baseline_p50_s", "baseline_p95_s",
		"candidate_p50_s", "candidate_p95_s",
		"baseline_restore_non_exact", "baseline_restore_unknown",
		"baseline_save_errors", "baseline_post_p50_s",
		"candidate_restore_non_exact", "candidate_restore_unknown",
		"candidate_save_errors", "candidate_post_p50_s",
		"excluded")
	for _, row := range rows {
		c := cacheByStratum[row.stratum]
		w.write(row.stratum.workflow, row.stratum.job, row.stratum.runner,
			strconv.Itoa(row.baselineN), strconv.Itoa(row.candN),
			strconv.Itoa(row.baseRuns), strconv.Itoa(row.candRuns),
			fmtSeconds(row.baselineP50), fmtSeconds(row.baseP95),
			fmtSeconds(row.candP50), fmtSeconds(row.candP95),
			strconv.Itoa(c.baseNonExact), strconv.Itoa(c.baseUnknown),
			strconv.Itoa(c.baseSaveErrs), fmtSeconds(c.basePostP50),
			strconv.Itoa(c.candNonExact), strconv.Itoa(c.candUnknown),
			strconv.Itoa(c.candSaveErrs), fmtSeconds(c.candPostP50),
			row.excluded)
	}
	w.write("")
	w.write("pr_signature", "baseline_n", "candidate_n",
		"baseline_p50_s", "candidate_p50_s", "excluded")
	for _, row := range fbRows {
		w.write(row.sig, strconv.Itoa(row.baseN), strconv.Itoa(row.candN),
			fmtSeconds(row.baseP50), fmtSeconds(row.candP50), row.excluded)
	}
	return w.err()
}

// writeAggregate renders one baseline-weighted headline.
func writeAggregate(b *strings.Builder, a aggregate, unit string) {
	fmt.Fprintf(b, "\n- covered share: %.1f%% of baseline weight (minimum %.0f%%)\n",
		a.coverage()*100, minCoverage*100)
	fmt.Fprintf(b, "- baseline weighted %s: %.1f min\n", unit, a.baseTotal/60)
	fmt.Fprintf(b, "- candidate weighted %s: %.1f min\n", unit, a.candTotal/60)
	fmt.Fprintf(b, "- **improvement: %s%%**\n", pct(a.improvement()))
	if reason := a.refusal(); reason != "" {
		fmt.Fprintf(b, "- aggregate refused: %s\n", reason)
	}
}

func renderReport(d reportData) string {
	var b strings.Builder
	b.WriteString("# CI timing comparison\n\n")
	b.WriteString("Strata (workflow, job, runner) and weights are frozen from the\n")
	b.WriteString("baseline window. Only successful first attempts feed the\n")
	b.WriteString("performance table; failures and retries are reported separately\n")
	b.WriteString("below. The headline is baseline-weighted job-minutes over the\n")
	b.WriteString("comparable strata only: strata with no candidate data, too few\n")
	b.WriteString("distinct runs, or a changed toolchain are excluded and listed\n")
	fmt.Fprintf(&b, "below. The aggregate is refused when the comparable strata cover\n")
	fmt.Fprintf(&b, "less than %.0f%% of the baseline weight: extend the observation\n", minCoverage*100)
	b.WriteString("window instead of accepting a thin result.\n\n")
	b.WriteString("## Job duration\n\n")
	b.WriteString("| workflow | job | runner | base n | cand n | base p50 | cand p50 | base p95 | cand p95 |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|\n")
	for _, row := range d.rows {
		s := row.stratum
		fmt.Fprintf(&b, "| %s | %s | %s | %d | %d | %s | %s | %s | %s |\n",
			s.workflow, s.job, s.runner,
			row.baselineN, row.candN,
			fmtSeconds(row.baselineP50), fmtSeconds(row.candP50),
			fmtSeconds(row.baseP95), fmtSeconds(row.candP95))
	}
	writeAggregate(&b, d.jobAgg, "job-minutes")

	excluded := false
	for _, row := range d.rows {
		if row.excluded == "" {
			continue
		}
		if !excluded {
			b.WriteString("\n## Excluded strata\n\n")
			excluded = true
		}
		fmt.Fprintf(&b, "- %s: %s\n", row.stratum, row.excluded)
	}

	if len(d.nonSuccess) > 0 {
		b.WriteString("\n## Non-success outcomes and retries (first attempts)\n\n")
		for _, row := range d.nonSuccess {
			fmt.Fprintf(&b, "- %s: baseline %d non-success, %d retried runs; candidate %d non-success, %d retried runs\n",
				row.stratum, row.baseFail, row.baseRetried, row.candFail, row.candRetried)
		}
	}

	b.WriteString("\n## Cache behavior\n\n")
	b.WriteString("Restores that were not exact hits and unknown\n")
	b.WriteString("classifications, save errors, and post-phase p50 from the\n")
	b.WriteString("successful first attempts of each stratum.\n\n")
	b.WriteString("| workflow | job | runner | restores b (non-exact/unknown) | restores c | save errors b/c | post p50 b/c |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	for _, row := range d.cacheRows {
		s := row.stratum
		fmt.Fprintf(&b, "| %s | %s | %s | %d (%d/%d) | %d (%d/%d) | %d/%d | %s/%s |\n",
			s.workflow, s.job, s.runner,
			row.baseRestores, row.baseNonExact, row.baseUnknown,
			row.candRestores, row.candNonExact, row.candUnknown,
			row.baseSaveErrs, row.candSaveErrs,
			fmtSeconds(row.basePostP50), fmtSeconds(row.candPostP50))
	}

	b.WriteString("\n## PR feedback time\n\n")
	b.WriteString("| signature | base n | cand n | base p50 s | cand p50 s |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for _, row := range d.fbRows {
		fmt.Fprintf(&b, "| %s | %d | %d | %s | %s |\n",
			row.sig, row.baseN, row.candN,
			fmtSeconds(row.baseP50), fmtSeconds(row.candP50))
	}
	writeAggregate(&b, d.fbAgg, "feedback-minutes")
	for _, row := range d.fbRows {
		if row.excluded != "" {
			fmt.Fprintf(&b, "- excluded signature %s: %s\n", row.sig, row.excluded)
		}
	}
	b.WriteString("\nWeekly observations demonstrate operational improvement, not\n")
	b.WriteString("randomized causal proof. A stratum is comparable with at least\n")
	fmt.Fprintf(&b, "%d distinct successful first-attempt run IDs per window; a PR\n", minRunSamples)
	fmt.Fprintf(&b, "feedback signature needs %d measured revisions per window.\n", minRevisions)
	return b.String()
}

func pct(v *float64) string {
	if v == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.1f", *v*100)
}
