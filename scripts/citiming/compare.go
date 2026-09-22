// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"cmp"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
)

// stratum is one frozen comparison cell. Strata and weights come from the
// baseline window: each baseline stratum's frequency is its weight and
// observations within a stratum are equal. This stops a change in the job
// mix from manufacturing an improvement.
// minRunSamples and minRevisions are the acceptance minima from the
// measurement plan: a workload comparison needs at least five
// independent successful first-attempt run IDs per stratum per window,
// and PR feedback needs five comparable revisions per window.
const (
	minRunSamples = 5
	minRevisions  = 5
)

// stratum is one frozen comparison cell. The logical job name carries
// the matrix dimensions (e.g. "test-contrib-matrix (chunk 3/6)"), so
// matrix variants with the same workload family do not collapse into
// one stratum: a reshuffled chunk selection or a different build tag
// must not be able to manufacture an improvement by changing which
// work runs. Strata and weights come from the baseline window: each
// baseline stratum's frequency is its weight and observations within
// a stratum are equal.
type stratum struct {
	workflow string
	workload string
	logical  string
	os       string
	arch     string
	goVer    string
}

func stratify(rec jobObservation) stratum {
	family := rec.Workload.Family
	if family == "" {
		family = rec.Job.LogicalName
	}
	return stratum{
		workflow: rec.Run.WorkflowFile,
		workload: family,
		logical:  rec.Job.LogicalName,
		os:       rec.Job.RunnerOS,
		arch:     rec.Job.RunnerArch,
		goVer:    rec.Workload.ResolvedGoVersion,
	}
}

func sortStrata(values []stratum) {
	slices.SortFunc(values, func(a, b stratum) int {
		return cmp.Or(
			cmp.Compare(a.workflow, b.workflow),
			cmp.Compare(a.workload, b.workload),
			cmp.Compare(a.logical, b.logical),
			cmp.Compare(a.os, b.os),
			cmp.Compare(a.arch, b.arch),
			cmp.Compare(a.goVer, b.goVer),
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

type weightedValue struct {
	value  float64
	weight float64
}

// weightedMedian returns the value where half the total weight is reached.
func weightedMedian(values []weightedValue) *float64 {
	if len(values) == 0 {
		return nil
	}
	slices.SortStableFunc(values, func(a, b weightedValue) int {
		return cmp.Compare(a.value, b.value)
	})
	var total float64
	for _, v := range values {
		total += v.weight
	}
	if total <= 0 {
		return nil
	}
	var cumulative float64
	for _, v := range values {
		cumulative += v.weight
		if cumulative >= total/2 {
			return &v.value
		}
	}
	return &values[len(values)-1].value
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

// loadedRecords is one window of collected evidence.
type loadedRecords struct {
	jobs     []jobObservation
	feedback []prFeedback
}

func loadRecords(dir string) (loadedRecords, error) {
	data, err := os.ReadFile(dir + "/observations.jsonl")
	if err != nil {
		return loadedRecords{}, err
	}
	var recs loadedRecords
	for i, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var probe struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal([]byte(line), &probe); err != nil {
			return loadedRecords{}, fmt.Errorf("%s line %d: %w", dir, i+1, err)
		}
		switch probe.Kind {
		case "job_observation":
			var rec jobObservation
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				return loadedRecords{}, fmt.Errorf("%s line %d: %w", dir, i+1, err)
			}
			recs.jobs = append(recs.jobs, rec)
		case "pr_feedback":
			var rec prFeedback
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				return loadedRecords{}, fmt.Errorf("%s line %d: %w", dir, i+1, err)
			}
			recs.feedback = append(recs.feedback, rec)
		}
	}
	return recs, nil
}

// feedbackRow is one PR-feedback signature comparison row.
type feedbackRow struct {
	sig              string
	baseN, candN     int
	baseP50, candP50 *float64
}

// perfValues splits successful first attempts from everything else.
// Skipped jobs are absent work, not zero-duration successes; failures and
// retries are reported separately, never in the performance table.
// perfSplit splits successful first attempts from everything else
// and aggregates the cache evidence alongside the durations. Skipped
// jobs are absent work, not zero-duration successes; failures and
// retries are reported separately, never in the performance table.
type perfSplit struct {
	values   map[stratum][]float64
	runIDs   map[stratum]map[int64]bool
	cache    map[stratum]*cacheAgg
	failures map[stratum][]string
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
		values:   map[stratum][]float64{},
		runIDs:   map[stratum]map[int64]bool{},
		cache:    map[stratum]*cacheAgg{},
		failures: map[stratum][]string{},
	}
	for _, rec := range records {
		if rec.Run.Attempt != 1 || !rec.Run.LatestAttempt {
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
	}
	return split
}

// feedbackRevisionCounts counts stored revisions per selection
// signature, including records whose feedback time is not measurable
// yet; the sample minimum applies to revisions, not just durations.
func feedbackRevisionCounts(records []prFeedback) map[string]int {
	counts := map[string]int{}
	for _, rec := range records {
		counts[rec.SelectionSignature]++
	}
	return counts
}

// underSampleRow names a stratum that fell below the distinct-run
// minimum in one of the windows.
type underSampleRow struct {
	stratum  stratum
	baseRuns int
	candRuns int
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

type comparisonRow struct {
	stratum              stratum
	baselineN, candN     int
	baseRuns, candRuns   int
	baselineP50, baseP95 *float64
	candP50, candP95     *float64
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

	baseline, err := loadRecords(*baselineDir)
	if err != nil {
		return err
	}
	candidate, err := loadRecords(*candidateDir)
	if err != nil {
		return err
	}

	baseSplit := perfValues(baseline.jobs)
	candSplit := perfValues(candidate.jobs)
	baseVals, baseFail := baseSplit.values, baseSplit.failures
	candVals, candFail := candSplit.values, candSplit.failures

	var weightedBase, weightedCand []weightedValue
	rows := make([]comparisonRow, 0, len(baseVals))
	var missing []stratum
	var underSampled []underSampleRow
	for _, key := range sortedKeys(baseVals) {
		b := slices.Clone(baseVals[key])
		slices.Sort(b)
		c := slices.Clone(candVals[key])
		slices.Sort(c)
		weight := float64(len(b))
		for _, v := range b {
			weightedBase = append(weightedBase, weightedValue{v, 1})
		}
		if len(c) > 0 {
			per := weight / float64(len(c))
			for _, v := range c {
				weightedCand = append(weightedCand, weightedValue{v, per})
			}
		}
		rows = append(rows, comparisonRow{
			stratum:     key,
			baselineN:   len(b),
			candN:       len(c),
			baseRuns:    len(baseSplit.runIDs[key]),
			candRuns:    len(candSplit.runIDs[key]),
			baselineP50: percentile(b, 0.50),
			baseP95:     percentile(b, 0.95),
			candP50:     percentile(c, 0.50),
			candP95:     percentile(c, 0.95),
		})
		if len(c) == 0 {
			missing = append(missing, key)
			continue
		}
		// Matrix jobs from a single run must not stand in for the
		// five independent run IDs the acceptance criteria require.
		if len(baseSplit.runIDs[key]) < minRunSamples ||
			len(candSplit.runIDs[key]) < minRunSamples {
			underSampled = append(underSampled, underSampleRow{
				stratum:  key,
				baseRuns: len(baseSplit.runIDs[key]),
				candRuns: len(candSplit.runIDs[key]),
			})
		}
	}

	baseWMed := weightedMedian(weightedBase)
	candWMed := weightedMedian(weightedCand)
	improvement := improvementOf(baseWMed, candWMed)

	var reasons []string
	if len(baseVals) == 0 {
		reasons = append(reasons, "no baseline observations")
	}
	if len(missing) > 0 {
		reasons = append(reasons, fmt.Sprintf(
			"%d baseline strata have no candidate observations", len(missing)))
	}
	if len(underSampled) > 0 {
		reasons = append(reasons, fmt.Sprintf(
			"%d strata are below the minimum of %d distinct run IDs in a window",
			len(underSampled), minRunSamples))
	}
	// A missing or under-sampled stratum keeps its baseline weight, so
	// computing the aggregate anyway could report a false improvement;
	// refuse it and let the report name what is missing.
	if len(reasons) > 0 {
		improvement = nil
	}

	baseFB := feedbackBySignature(baseline.feedback)
	candFB := feedbackBySignature(candidate.feedback)
	baseFBRevs := feedbackRevisionCounts(baseline.feedback)
	candFBRevs := feedbackRevisionCounts(candidate.feedback)
	var fbBase, fbCand []weightedValue
	fbRows := make([]feedbackRow, 0, len(baseFB))
	var fbMissing, fbUnder []string
	for _, sig := range sortedSigKeys(baseFB) {
		b := slices.Clone(baseFB[sig])
		slices.Sort(b)
		c := slices.Clone(candFB[sig])
		slices.Sort(c)
		for _, v := range b {
			fbBase = append(fbBase, weightedValue{v, 1})
		}
		if len(c) > 0 {
			per := float64(len(b)) / float64(len(c))
			for _, v := range c {
				fbCand = append(fbCand, weightedValue{v, per})
			}
		}
		fbRows = append(fbRows, feedbackRow{
			sig:     sig,
			baseN:   len(b),
			candN:   len(c),
			baseP50: percentile(b, 0.50),
			candP50: percentile(c, 0.50),
		})
		if len(c) == 0 {
			fbMissing = append(fbMissing, sig)
			continue
		}
		if baseFBRevs[sig] < minRevisions || candFBRevs[sig] < minRevisions {
			fbUnder = append(fbUnder, fmt.Sprintf(
				"%s: %d baseline, %d candidate revisions (minimum %d)",
				sig, baseFBRevs[sig], candFBRevs[sig], minRevisions))
		}
	}
	fbBaseWMed := weightedMedian(fbBase)
	fbCandWMed := weightedMedian(fbCand)
	fbImprovement := improvementOf(fbBaseWMed, fbCandWMed)
	if len(fbMissing) > 0 || len(fbUnder) > 0 {
		fbImprovement = nil
	}

	cacheRows := cacheBehaviorRows(baseSplit, candSplit)

	if err := os.MkdirAll(*outputDir, 0o755); err != nil {
		return err
	}
	if err := writeComparisonCSV(*outputDir, rows, cacheRows, fbRows); err != nil {
		return err
	}
	data := reportData{
		rows:         rows,
		missing:      missing,
		underSampled: underSampled,
		cacheRows:    cacheRows,
		baseWMed:     baseWMed,
		candWMed:     candWMed,
		improvement:  improvement,
		reasons:      reasons,
		baseFail:     baseFail,
		candFail:     candFail,
		strataKeys: strataKeyUnion(strataKeySet(baseVals),
			strataKeySet(candVals), strataKeySet(baseFail),
			strataKeySet(candFail)),
		fbRows:        fbRows,
		fbMissing:     fbMissing,
		fbUnder:       fbUnder,
		fbBaseWMed:    fbBaseWMed,
		fbCandWMed:    fbCandWMed,
		fbImprovement: fbImprovement,
	}
	report := renderReport(data)
	if err := os.WriteFile(*outputDir+"/comparison.md", []byte(report), 0o644); err != nil {
		return err
	}
	fmt.Fprint(os.Stdout, report)
	fmt.Fprintf(os.Stdout, "\nWrote %s/comparison.md and %s/comparison.csv\n", *outputDir, *outputDir)
	if improvement == nil {
		msg := "comparison not computable"
		if len(reasons) > 0 {
			msg += ": " + strings.Join(reasons, "; ")
		}
		return errors.New(msg)
	}
	return nil
}

// strataKeyUnion returns every stratum that appears in any of the
// success or failure maps, so strata whose jobs all failed stay
// visible in the report instead of vanishing because they never
// produced a timing sample.
// strataKeySet extracts the key set of any stratum-keyed map.
func strataKeySet[V any](m map[stratum]V) map[stratum]bool {
	s := make(map[stratum]bool, len(m))
	for k := range m {
		s[k] = true
	}
	return s
}

func strataKeyUnion(sets ...map[stratum]bool) []stratum {
	seen := map[stratum]bool{}
	for _, s := range sets {
		for k := range s {
			seen[k] = true
		}
	}
	keys := make([]stratum, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sortStrata(keys)
	return keys
}

func improvementOf(base, cand *float64) *float64 {
	if base == nil || cand == nil || *base == 0 {
		return nil
	}
	value := 1 - *cand / *base
	return &value
}

func sortedKeys(values map[stratum][]float64) []stratum {
	keys := make([]stratum, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sortStrata(keys)
	return keys
}

func sortedSigKeys(values map[string][]float64) []string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// reportData bundles everything the rendered report needs.
type reportData struct {
	rows                   []comparisonRow
	missing                []stratum
	underSampled           []underSampleRow
	cacheRows              []cacheRow
	baseWMed, candWMed     *float64
	improvement            *float64
	reasons                []string
	baseFail, candFail     map[stratum][]string
	strataKeys             []stratum
	fbRows                 []feedbackRow
	fbMissing, fbUnder     []string
	fbBaseWMed, fbCandWMed *float64
	fbImprovement          *float64
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
	w.write("workflow_file", "workload", "job", "os", "arch", "go_version",
		"baseline_n", "candidate_n",
		"baseline_runs", "candidate_runs",
		"baseline_p50_s", "baseline_p95_s",
		"candidate_p50_s", "candidate_p95_s",
		"baseline_restore_non_exact", "baseline_restore_unknown",
		"baseline_save_errors", "baseline_post_p50_s",
		"candidate_restore_non_exact", "candidate_restore_unknown",
		"candidate_save_errors", "candidate_post_p50_s")
	for _, row := range rows {
		c := cacheByStratum[row.stratum]
		w.write(row.stratum.workflow, row.stratum.workload,
			row.stratum.logical, row.stratum.os,
			row.stratum.arch, row.stratum.goVer,
			strconv.Itoa(row.baselineN), strconv.Itoa(row.candN),
			strconv.Itoa(row.baseRuns), strconv.Itoa(row.candRuns),
			fmtSeconds(row.baselineP50), fmtSeconds(row.baseP95),
			fmtSeconds(row.candP50), fmtSeconds(row.candP95),
			strconv.Itoa(c.baseNonExact), strconv.Itoa(c.baseUnknown),
			strconv.Itoa(c.baseSaveErrs), fmtSeconds(c.basePostP50),
			strconv.Itoa(c.candNonExact), strconv.Itoa(c.candUnknown),
			strconv.Itoa(c.candSaveErrs), fmtSeconds(c.candPostP50))
	}
	w.write("")
	w.write("pr_signature", "baseline_n", "candidate_n",
		"baseline_p50_s", "candidate_p50_s")
	for _, row := range fbRows {
		w.write(row.sig, strconv.Itoa(row.baseN), strconv.Itoa(row.candN),
			fmtSeconds(row.baseP50), fmtSeconds(row.candP50))
	}
	return w.err()
}

func renderReport(d reportData) string {
	var b strings.Builder
	b.WriteString("# CI timing comparison\n\n")
	b.WriteString("Strata and weights are frozen from the baseline window. Only\n")
	b.WriteString("successful first attempts feed the performance table; failures and\n")
	b.WriteString("retries are reported separately below. The aggregate is refused\n")
	b.WriteString("while any baseline stratum lacks candidate data or sits below the\n")
	b.WriteString("sample minimum: extend the observation window instead of\n")
	b.WriteString("accepting an under-sampled result.\n\n")
	b.WriteString("## Job duration\n\n")
	b.WriteString("| workflow | workload | job | os | go | base n | cand n | base p50 | cand p50 | base p95 | cand p95 |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, row := range d.rows {
		s := row.stratum
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %d | %d | %s | %s | %s | %s |\n",
			s.workflow, s.workload, s.logical, s.os, orNA(s.goVer),
			row.baselineN, row.candN,
			fmtSeconds(row.baselineP50), fmtSeconds(row.candP50),
			fmtSeconds(row.baseP95), fmtSeconds(row.candP95))
	}
	fmt.Fprintf(&b, "\n- baseline weighted median: %s s\n", fmtSeconds(d.baseWMed))
	fmt.Fprintf(&b, "- candidate weighted median: %s s\n", fmtSeconds(d.candWMed))
	fmt.Fprintf(&b, "- **improvement: %s%%**\n", pct(d.improvement))
	if len(d.reasons) > 0 {
		b.WriteString("- aggregate refused: ")
		b.WriteString(strings.Join(d.reasons, "; "))
		b.WriteString("\n")
	}

	if len(d.missing) > 0 {
		b.WriteString("\n## Missing coverage\n\n")
		b.WriteString("Baseline strata with no candidate observations:\n")
		for _, key := range d.missing {
			fmt.Fprintf(&b, "- %s / %s / %s / %s / %s\n",
				key.workflow, key.workload, key.logical, key.os, key.goVer)
		}
	}

	if len(d.underSampled) > 0 {
		b.WriteString("\n## Under-sampled strata\n\n")
		fmt.Fprintf(&b, "Below the minimum of %d distinct run IDs in a window:\n",
			minRunSamples)
		for _, u := range d.underSampled {
			fmt.Fprintf(&b, "- %s / %s / %s: %d baseline, %d candidate runs\n",
				u.stratum.workflow, u.stratum.workload, u.stratum.logical,
				u.baseRuns, u.candRuns)
		}
	}

	if len(d.baseFail) > 0 || len(d.candFail) > 0 {
		b.WriteString("\n## Non-success outcomes (first attempts)\n\n")
		for _, key := range d.strataKeys {
			fmt.Fprintf(&b, "- %s / %s / %s: baseline %d, candidate %d\n",
				key.workflow, key.workload, key.logical,
				len(d.baseFail[key]), len(d.candFail[key]))
		}
	}

	b.WriteString("\n## Cache behavior\n\n")
	b.WriteString("Restores that were not exact hits and unknown\n")
	b.WriteString("classifications, save errors, and post-phase p50 from the\n")
	b.WriteString("successful first attempts of each stratum.\n\n")
	b.WriteString("| workload | job | os | restores b (non-exact/unknown) | restores c | save errors b/c | post p50 b/c |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	for _, row := range d.cacheRows {
		s := row.stratum
		fmt.Fprintf(&b, "| %s | %s | %s | %d (%d/%d) | %d (%d/%d) | %d/%d | %s/%s |\n",
			s.workload, s.logical, s.os,
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
	fmt.Fprintf(&b, "\n- baseline weighted median: %s s\n", fmtSeconds(d.fbBaseWMed))
	fmt.Fprintf(&b, "- candidate weighted median: %s s\n", fmtSeconds(d.fbCandWMed))
	fmt.Fprintf(&b, "- **improvement: %s%%**\n", pct(d.fbImprovement))
	if len(d.fbMissing) > 0 {
		fmt.Fprintf(&b, "- feedback refused: %d baseline signatures have no candidate revisions\n",
			len(d.fbMissing))
	}
	for _, note := range d.fbUnder {
		fmt.Fprintf(&b, "- feedback under-sampled: %s\n", note)
	}
	b.WriteString("\nWeekly observations demonstrate operational improvement, not\n")
	b.WriteString("randomized causal proof. A workload comparison needs at least\n")
	fmt.Fprintf(&b, "%d independent successful first-attempt run IDs per stratum per\n", minRunSamples)
	fmt.Fprintf(&b, "window; PR feedback needs %d comparable PR revisions per window.\n", minRevisions)
	return b.String()
}

func pct(v *float64) string {
	if v == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.1f", *v*100)
}

func orNA(value string) string {
	if value == "" {
		return "n/a"
	}
	return value
}
