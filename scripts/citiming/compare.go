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
type stratum struct {
	workflow string
	workload string
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
func perfValues(records []jobObservation) (values map[stratum][]float64, failures map[stratum][]string) {
	values = map[stratum][]float64{}
	failures = map[stratum][]string{}
	for _, rec := range records {
		if rec.Run.Attempt != 1 || !rec.Run.LatestAttempt {
			continue
		}
		key := stratify(rec)
		switch {
		case rec.Job.Conclusion == "success" && rec.Job.Seconds != nil:
			values[key] = append(values[key], *rec.Job.Seconds)
		case rec.Job.Conclusion != "skipped" && rec.Job.Conclusion != "":
			failures[key] = append(failures[key], rec.Job.Conclusion)
		}
	}
	return values, failures
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

	baseVals, baseFail := perfValues(baseline.jobs)
	candVals, candFail := perfValues(candidate.jobs)

	var weightedBase, weightedCand []weightedValue
	rows := make([]comparisonRow, 0, len(baseVals))
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
			baselineP50: percentile(b, 0.50),
			baseP95:     percentile(b, 0.95),
			candP50:     percentile(c, 0.50),
			candP95:     percentile(c, 0.95),
		})
	}

	baseWMed := weightedMedian(weightedBase)
	candWMed := weightedMedian(weightedCand)
	improvement := improvementOf(baseWMed, candWMed)

	baseFB := feedbackBySignature(baseline.feedback)
	candFB := feedbackBySignature(candidate.feedback)
	var fbBase, fbCand []weightedValue
	fbRows := make([]feedbackRow, 0, len(baseFB))
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
	}
	fbBaseWMed := weightedMedian(fbBase)
	fbCandWMed := weightedMedian(fbCand)
	fbImprovement := improvementOf(fbBaseWMed, fbCandWMed)

	if err := os.MkdirAll(*outputDir, 0o755); err != nil {
		return err
	}
	if err := writeComparisonCSV(*outputDir, rows, fbRows); err != nil {
		return err
	}
	report := renderReport(rows, baseVals, candVals, baseWMed, candWMed, improvement,
		baseFail, candFail, fbRows, fbBaseWMed, fbCandWMed, fbImprovement)
	if err := os.WriteFile(*outputDir+"/comparison.md", []byte(report), 0o644); err != nil {
		return err
	}
	fmt.Fprint(os.Stdout, report)
	fmt.Fprintf(os.Stdout, "\nWrote %s/comparison.md and %s/comparison.csv\n", *outputDir, *outputDir)
	if improvement == nil {
		return errors.New("no comparable candidate data; comparison not computable")
	}
	return nil
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

func writeComparisonCSV(dir string, rows []comparisonRow, fbRows []feedbackRow) error {
	f, err := os.Create(dir + "/comparison.csv")
	if err != nil {
		return err
	}
	defer f.Close()
	w := newCSVWriter(f)
	w.write("workflow_file", "workload", "os", "arch", "go_version",
		"baseline_n", "candidate_n", "baseline_p50_s", "baseline_p95_s",
		"candidate_p50_s", "candidate_p95_s")
	for _, row := range rows {
		w.write(row.stratum.workflow, row.stratum.workload, row.stratum.os,
			row.stratum.arch, row.stratum.goVer,
			strconv.Itoa(row.baselineN), strconv.Itoa(row.candN),
			fmtSeconds(row.baselineP50), fmtSeconds(row.baseP95),
			fmtSeconds(row.candP50), fmtSeconds(row.candP95))
	}
	w.write("")
	w.write("pr_signature", "baseline_n", "candidate_n", "baseline_p50_s", "candidate_p50_s")
	for _, row := range fbRows {
		w.write(row.sig, strconv.Itoa(row.baseN), strconv.Itoa(row.candN),
			fmtSeconds(row.baseP50), fmtSeconds(row.candP50))
	}
	return w.err()
}

func renderReport(rows []comparisonRow, baseVals, candVals map[stratum][]float64,
	baseWMed, candWMed, improvement *float64, baseFail, candFail map[stratum][]string,
	fbRows []feedbackRow, fbBaseWMed, fbCandWMed, fbImprovement *float64) string {

	var b strings.Builder
	b.WriteString("# CI timing comparison\n\n")
	b.WriteString("Strata and weights are frozen from the baseline window. Only\n")
	b.WriteString("successful first attempts feed the performance table; failures and\n")
	b.WriteString("retries are reported separately below.\n\n")
	b.WriteString("## Job duration\n\n")
	b.WriteString("| workflow | workload | os | go | base n | cand n | base p50 | cand p50 | base p95 | cand p95 |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|\n")
	for _, row := range rows {
		s := row.stratum
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %d | %d | %s | %s | %s | %s |\n",
			s.workflow, s.workload, s.os, orNA(s.goVer),
			row.baselineN, row.candN,
			fmtSeconds(row.baselineP50), fmtSeconds(row.candP50),
			fmtSeconds(row.baseP95), fmtSeconds(row.candP95))
	}
	fmt.Fprintf(&b, "\n- baseline weighted median: %s s\n", fmtSeconds(baseWMed))
	fmt.Fprintf(&b, "- candidate weighted median: %s s\n", fmtSeconds(candWMed))
	fmt.Fprintf(&b, "- **improvement: %s%%**\n", pct(improvement))

	var missing []stratum
	for _, key := range sortedKeys(baseVals) {
		if len(candVals[key]) == 0 {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		b.WriteString("\n## Missing coverage\n\n")
		b.WriteString("Baseline strata with no candidate observations:\n")
		for _, key := range missing {
			fmt.Fprintf(&b, "- %s / %s / %s / %s\n", key.workflow, key.workload, key.os, key.goVer)
		}
	}

	if len(baseFail) > 0 || len(candFail) > 0 {
		b.WriteString("\n## Non-success outcomes (first attempts)\n\n")
		for _, key := range sortedKeys(baseVals) {
			fmt.Fprintf(&b, "- %s / %s: baseline %d, candidate %d\n",
				key.workflow, key.workload,
				len(baseFail[key]), len(candFail[key]))
		}
	}

	b.WriteString("\n## PR feedback time\n\n")
	b.WriteString("| signature | base n | cand n | base p50 s | cand p50 s |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for _, row := range fbRows {
		fmt.Fprintf(&b, "| %s | %d | %d | %s | %s |\n",
			row.sig, row.baseN, row.candN,
			fmtSeconds(row.baseP50), fmtSeconds(row.candP50))
	}
	fmt.Fprintf(&b, "\n- baseline weighted median: %s s\n", fmtSeconds(fbBaseWMed))
	fmt.Fprintf(&b, "- candidate weighted median: %s s\n", fmtSeconds(fbCandWMed))
	fmt.Fprintf(&b, "- **improvement: %s%%**\n", pct(fbImprovement))
	b.WriteString("\nWeekly observations demonstrate operational improvement, not\n")
	b.WriteString("randomized causal proof. A workload comparison needs at least five\n")
	b.WriteString("independent successful first-attempt run IDs per stratum per window;\n")
	b.WriteString("PR feedback needs five comparable PR revisions per window.\n")
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
