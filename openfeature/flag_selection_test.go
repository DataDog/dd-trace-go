// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	internalffe "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

// The expected log lines are spelled out in full so that any drift from the
// wording shared with the other Datadog SDKs fails a test.
const (
	wantDropMalformed         = "openfeature: ProviderConfig.FlagSelection was not sent (malformed_selection: ProviderConfig.FlagSelection must have optional Tags and KeyPatterns slices of valid UTF-8 strings). Loading the full flag configuration."
	wantDropTooManyValues     = "openfeature: ProviderConfig.FlagSelection was not sent (too_many_values: each list accepts at most 31 values). Loading the full flag configuration."
	wantDropInvalidKeyPattern = "openfeature: ProviderConfig.FlagSelection was not sent (invalid_key_pattern: each key pattern is at most 200 UTF-8 bytes). Loading the full flag configuration."
	wantDropInvalidTag        = "openfeature: ProviderConfig.FlagSelection was not sent (invalid_tag: each tag is at most 200 UTF-8 bytes). Loading the full flag configuration."
	wantDropURLTooLong        = "openfeature: ProviderConfig.FlagSelection was not sent (too_many_values: the request URL is at most 8192 bytes). Loading the full flag configuration."
	wantRemoteConfigWarning   = "openfeature: ProviderConfig.FlagSelection is not supported when flags are delivered through Remote Config. Loading the full flag configuration."
)

func wantIgnored(reason string) string {
	return "openfeature: ProviderConfig.FlagSelection was ignored by the server (" + reason + "). Loaded the full flag configuration."
}

const testManagedEndpoint = "https://ufc-server.ff-cdn.datadoghq.com/api/v2/feature-flagging/config/rules-based/server"

func resetFlagSelectionReports(t *testing.T) {
	t.Helper()
	flagSelectionReported.Clear()
	t.Cleanup(flagSelectionReported.Clear)
}

func flagSelectionReportedFor(metric, reason string) bool {
	_, ok := flagSelectionReported.Load(metric + ":" + reason)
	return ok
}

// numbered returns n distinct values with the given prefix.
func numbered(prefix string, n int) []string {
	values := make([]string, n)
	for i := range values {
		values[i] = fmt.Sprintf("%s%02d", prefix, i+1)
	}
	return values
}

type flagSelectionVectorFile struct {
	Cases []struct {
		Name     string              `json:"name"`
		Query    map[string][]string `json:"query"`
		RawQuery *string             `json:"rawQuery"`
		SDK      *struct {
			Selection *struct {
				Tags        []string `json:"tags"`
				KeyPatterns []string `json:"keyPatterns"`
			} `json:"selection"`
			Dropped string `json:"dropped"`
		} `json:"sdk"`
	} `json:"cases"`
}

func emptyIfNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func TestFlagSelectionVectors(t *testing.T) {
	resetFlagSelectionReports(t)
	withLogLevel(t, log.LevelWarn)

	data, err := os.ReadFile(filepath.Join("ffe-system-test-data", "flag-selection", "selection-vectors.json"))
	if err != nil {
		t.Fatalf("read flag selection vectors: %v (initialize them with `git submodule update --init --recursive`)", err)
	}
	var vectors flagSelectionVectorFile
	require.NoError(t, json.Unmarshal(data, &vectors))
	require.Len(t, vectors.Cases, 44)
	sdkCases := 0
	for _, c := range vectors.Cases {
		if c.SDK != nil {
			sdkCases++
		}
	}
	require.Equal(t, 38, sdkCases, "the other 6 cases are rawQuery cases")

	const endpoint = testManagedEndpoint + "?dd_env=prod"
	for _, c := range vectors.Cases {
		t.Run(c.Name, func(t *testing.T) {
			if c.SDK == nil {
				require.NotNil(t, c.RawQuery, "only rawQuery cases may lack an sdk expectation")
				require.Nil(t, c.Query)
				t.Skip("rawQuery cases cover how the server percent-decodes a raw query; the SDK never parses one")
			}
			require.NotNil(t, c.Query, "an sdk case must carry decoded query values")

			selection := &FlagSelection{
				Tags:        c.Query["selected_flag_tags"],
				KeyPatterns: c.Query["selected_flag_key_patterns"],
			}
			canonical, drop := canonicalizeFlagSelection(selection)
			requestURL, sent := requestURLWithFlagSelection(endpoint, selection)

			if c.SDK.Dropped != "" {
				require.Nil(t, c.SDK.Selection)
				assert.Equal(t, c.SDK.Dropped, drop.reason())
				assert.False(t, sent)
				assert.Equal(t, endpoint, requestURL, "a dropped selection must request the full configuration")
				return
			}

			require.NotNil(t, c.SDK.Selection)
			require.Equal(t, flagSelectionNotDropped, drop)
			assert.Equal(t, emptyIfNil(c.SDK.Selection.Tags), emptyIfNil(canonical.tags))
			assert.Equal(t, emptyIfNil(c.SDK.Selection.KeyPatterns), emptyIfNil(canonical.keyPatterns))

			u, err := url.Parse(requestURL)
			require.NoError(t, err)
			query := u.Query()
			assert.Equal(t, []string{"prod"}, query["dd_env"])
			assert.Equal(t, emptyIfNil(c.SDK.Selection.Tags), emptyIfNil(query["selected_flag_tags"]))
			assert.Equal(t, emptyIfNil(c.SDK.Selection.KeyPatterns), emptyIfNil(query["selected_flag_key_patterns"]))
			wantSent := len(c.SDK.Selection.Tags)+len(c.SDK.Selection.KeyPatterns) > 0
			assert.Equal(t, wantSent, sent)
			if !wantSent {
				assert.Equal(t, endpoint, requestURL, "an empty selection must leave the request unchanged")
			}
		})
	}
}

func TestRequestURLWithFlagSelection(t *testing.T) {
	resetFlagSelectionReports(t)
	for _, tc := range []struct {
		name      string
		endpoint  string
		selection FlagSelection
		want      string
	}{
		{
			name:      "RFC 3986 percent-encoding with uppercase hex",
			endpoint:  "https://example.test/rules",
			selection: FlagSelection{Tags: []string{"a b+c/&=?#%é*!'()~"}},
			want:      "https://example.test/rules?selected_flag_tags=a%20b%2Bc%2F%26%3D%3F%23%25%C3%A9%2A%21%27%28%29~",
		},
		{
			name:      "repeated parameters, tags before key patterns",
			endpoint:  "https://example.test/rules",
			selection: FlagSelection{Tags: []string{"area:b", "area:a"}, KeyPatterns: []string{"checkout.*"}},
			want:      "https://example.test/rules?selected_flag_tags=area%3Aa&selected_flag_tags=area%3Ab&selected_flag_key_patterns=checkout.%2A",
		},
		{
			name:      "managed endpoint keeps its dd_env encoding",
			endpoint:  testManagedEndpoint + "?dd_env=my+env",
			selection: FlagSelection{Tags: []string{"mobile"}},
			want:      testManagedEndpoint + "?dd_env=my+env&selected_flag_tags=mobile",
		},
		{
			name:      "custom endpoint query is kept byte for byte",
			endpoint:  "http://127.0.0.1:8080/custom/rules?dd_env=test&x=%2f",
			selection: FlagSelection{KeyPatterns: []string{"a*"}},
			want:      "http://127.0.0.1:8080/custom/rules?dd_env=test&x=%2f&selected_flag_key_patterns=a%2A",
		},
		{
			name:      "empty query",
			endpoint:  "https://example.test/rules?",
			selection: FlagSelection{Tags: []string{"t"}},
			want:      "https://example.test/rules?selected_flag_tags=t",
		},
		{
			name:      "userinfo and fragment are kept",
			endpoint:  "https://user:pass@example.test/rules?k=v#frag",
			selection: FlagSelection{Tags: []string{"t"}},
			want:      "https://user:pass@example.test/rules?k=v&selected_flag_tags=t#frag",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, sent := requestURLWithFlagSelection(tc.endpoint, &tc.selection)
			assert.True(t, sent)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestRequestURLWithFlagSelection_WholeURLLimit(t *testing.T) {
	resetFlagSelectionReports(t)
	logger := withLogLevel(t, log.LevelWarn)
	log.Flush()

	const base = "https://example.test/"
	const param = "?selected_flag_tags="
	endpoint := base + strings.Repeat("p", 8192-len(base)-len(param)-len("t"))

	got, sent := requestURLWithFlagSelection(endpoint, &FlagSelection{Tags: []string{"t"}})
	assert.True(t, sent)
	assert.Len(t, got, 8192)

	got, sent = requestURLWithFlagSelection(endpoint, &FlagSelection{Tags: []string{"tt"}})
	assert.False(t, sent)
	assert.Equal(t, endpoint, got)

	// The limit applies after percent-encoding: "é" is two bytes, sent as six.
	encodedEndpoint := base + strings.Repeat("p", 8192-len(base)-len(param)-len("%C3%A9"))
	got, sent = requestURLWithFlagSelection(encodedEndpoint, &FlagSelection{Tags: []string{"é"}})
	assert.True(t, sent)
	assert.Len(t, got, 8192)

	got, sent = requestURLWithFlagSelection(encodedEndpoint+"p", &FlagSelection{Tags: []string{"é"}})
	assert.False(t, sent)
	assert.Equal(t, encodedEndpoint+"p", got)

	log.Flush()
	assert.Equal(t, 1, logger.countContaining(wantDropURLTooLong+", 1 additional messages skipped"), "one error per dropped selection:\n%s", strings.Join(logger.all(), "\n"))
	assert.True(t, flagSelectionReportedFor(flagSelectionDroppedMetric, "too_many_values"))
}

func TestCanonicalizeFlagSelection_InvalidUTF8IsMalformed(t *testing.T) {
	for name, selection := range map[string]FlagSelection{
		"tag":                                       {Tags: []string{"mobile", "\xff"}},
		"key pattern":                               {KeyPatterns: []string{"checkout.\xc3"}},
		"surrogate code point":                      {Tags: []string{"\xed\xa0\x80"}},
		"surrounded by white space":                 {Tags: []string{" \xff "}},
		"takes precedence over too_many_values":     {Tags: numbered("t", 32), KeyPatterns: []string{"\xff"}},
		"takes precedence over invalid_tag":         {Tags: []string{strings.Repeat("a", 201), "\xff"}},
		"takes precedence over invalid_key_pattern": {KeyPatterns: []string{strings.Repeat("a", 201), "\xff"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, drop := canonicalizeFlagSelection(&selection)
			assert.Equal(t, flagSelectionMalformed, drop)
			assert.Equal(t, "malformed_selection", drop.reason())
		})
	}
}

func TestCanonicalizeFlagSelection_CopiesTheCallersLists(t *testing.T) {
	tags := []string{"b", "a", "b"}
	patterns := []string{"y*", "x*"}
	canonical, drop := canonicalizeFlagSelection(&FlagSelection{Tags: tags, KeyPatterns: patterns})
	require.Equal(t, flagSelectionNotDropped, drop)

	assert.Equal(t, []string{"b", "a", "b"}, tags, "the caller's list must not be reordered")
	tags[0], patterns[0] = "changed", "changed"
	assert.Equal(t, []string{"a", "b"}, canonical.tags)
	assert.Equal(t, []string{"x*", "y*"}, canonical.keyPatterns)
}

func TestCanonicalizeFlagSelection_NilAndEmpty(t *testing.T) {
	for name, selection := range map[string]*FlagSelection{
		"nil selection":    nil,
		"nil lists":        {},
		"empty lists":      {Tags: []string{}, KeyPatterns: []string{}},
		"only white space": {Tags: []string{" ", "　"}, KeyPatterns: []string{""}},
	} {
		t.Run(name, func(t *testing.T) {
			canonical, drop := canonicalizeFlagSelection(selection)
			assert.Equal(t, flagSelectionNotDropped, drop)
			assert.True(t, canonical.empty())
		})
	}
}

func TestSanitizeFlagSelectionIgnoredReason(t *testing.T) {
	for _, reason := range []string{"malformed_selection", "too_many_values", "invalid_key_pattern", "invalid_tag", "tag_load_failed"} {
		assert.Equal(t, reason, sanitizeFlagSelectionIgnoredReason(reason))
	}
	for _, value := range []string{
		strings.Repeat("a", 33),
		"tag_load_failed" + strings.Repeat("_", 18), // 33 bytes, starts with a known reason
		"Too_Many_Values",
		"too_many_values ",
		" invalid_tag",
		"invalid_tag\t",
		"reason_from_a_newer_server",
		"invalid_tag,too_many_values",
	} {
		assert.Equal(t, "unknown", sanitizeFlagSelectionIgnoredReason(value), "value %q", value)
	}
}

func TestReportFlagSelectionDropped_LogsEveryCallWithAFormatStringPerReason(t *testing.T) {
	resetFlagSelectionReports(t)
	logger := withLogLevel(t, log.LevelWarn)
	log.Flush()

	for _, drop := range []flagSelectionDrop{
		flagSelectionMalformed,
		flagSelectionTooManyValues,
		flagSelectionInvalidKeyPattern,
		flagSelectionInvalidTag,
		flagSelectionURLTooLong,
		flagSelectionTooManyValues,
	} {
		reportFlagSelectionDropped(drop)
	}
	log.Flush()

	for _, want := range []string{wantDropMalformed, wantDropInvalidKeyPattern, wantDropInvalidTag, wantDropURLTooLong} {
		assert.Equal(t, 1, logger.countContaining(want+" (occurred: "), "want exactly one %q in:\n%s", want, strings.Join(logger.all(), "\n"))
	}
	assert.Equal(t, 1, logger.countContaining(wantDropTooManyValues+", 1 additional messages skipped"), "one log.Error per call:\n%s", strings.Join(logger.all(), "\n"))
}

func TestReportFlagSelectionIgnored_LogsOncePerProcessPerReason(t *testing.T) {
	resetFlagSelectionReports(t)
	logger := withLogLevel(t, log.LevelWarn)
	log.Flush()

	for _, header := range []string{"invalid_tag", "invalid_tag", "tag_load_failed", "Invalid_Tag", "secret-value"} {
		reportFlagSelectionIgnored(header)
	}
	log.Flush()

	for _, reason := range []string{"invalid_tag", "tag_load_failed", "unknown"} {
		assert.Equal(t, 1, logger.countContaining(wantIgnored(reason)+" (occurred: "), "want exactly one line for %s in:\n%s", reason, strings.Join(logger.all(), "\n"))
	}
	assert.Zero(t, logger.countContaining("secret-value"))
	assert.Zero(t, logger.countContaining("Invalid_Tag"))
}

// capturedFlagSelectionCounts sums the flag_selection.* counts in every
// telemetry payload rt captured, keyed by "namespace metric tags".
func capturedFlagSelectionCounts(t *testing.T, rt *telemetrytest.CaptureRoundTripper) map[string]float64 {
	t.Helper()
	type series struct {
		Metric    string   `json:"metric"`
		Namespace string   `json:"namespace"`
		Type      string   `json:"type"`
		Tags      []string `json:"tags"`
		Points    [][2]any `json:"points"`
	}
	type metrics struct {
		Namespace string   `json:"namespace"`
		Series    []series `json:"series"`
	}
	type message struct {
		RequestType string          `json:"request_type"`
		Payload     json.RawMessage `json:"payload"`
	}
	counts := map[string]float64{}
	var collect func(message)
	collect = func(m message) {
		switch m.RequestType {
		case "message-batch":
			var batch []message
			require.NoError(t, json.Unmarshal(m.Payload, &batch))
			for _, inner := range batch {
				collect(inner)
			}
		case "generate-metrics":
			var payload metrics
			require.NoError(t, json.Unmarshal(m.Payload, &payload))
			for _, s := range payload.Series {
				if !strings.HasPrefix(s.Metric, "flag_selection.") {
					continue
				}
				namespace := s.Namespace
				if namespace == "" {
					namespace = payload.Namespace
				}
				assert.Equal(t, "count", s.Type)
				key := namespace + " " + s.Metric + " " + strings.Join(s.Tags, ",")
				for _, point := range s.Points {
					value, ok := point[1].(float64)
					require.True(t, ok)
					counts[key] += value
				}
			}
		}
	}
	for _, body := range rt.Bodies() {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		var m message
		require.NoError(t, json.Unmarshal(raw, &m))
		collect(m)
	}
	return counts
}

func TestFlagSelectionTelemetry_OncePerProcessPerReason(t *testing.T) {
	resetFlagSelectionReports(t)
	withLogLevel(t, log.LevelWarn)
	client, rt := telemetrytest.NewCapturingClient(t)
	defer telemetry.MockClient(client)()

	reportFlagSelectionDropped(flagSelectionTooManyValues)
	reportFlagSelectionDropped(flagSelectionURLTooLong)
	reportFlagSelectionDropped(flagSelectionTooManyValues)
	reportFlagSelectionDropped(flagSelectionInvalidTag)
	reportFlagSelectionIgnored("tag_load_failed")
	reportFlagSelectionIgnored("tag_load_failed")
	reportFlagSelectionIgnored("reason_from_a_newer_server")
	reportFlagSelectionIgnored("Too_Many_Values")
	client.Flush()

	assert.Equal(t, map[string]float64{
		"tracers flag_selection.dropped reason:too_many_values": 1,
		"tracers flag_selection.dropped reason:invalid_tag":     1,
		"tracers flag_selection.ignored reason:tag_load_failed": 1,
		"tracers flag_selection.ignored reason:unknown":         1,
	}, capturedFlagSelectionCounts(t, rt))
}

// selectionBackend is a scripted Agentless endpoint that records the query and
// If-None-Match of every request.
type selectionBackend struct {
	server *httptest.Server

	mu          sync.Mutex
	responses   []selectionResponse // the last one repeats
	rawQueries  []string
	paths       []string
	ifNoneMatch []string
}

type selectionResponse struct {
	status int
	// ignored, when set, is sent as DD-Flag-Selection-Ignored.
	ignored *string
	// ignoredFields, when set, are sent as repeated DD-Flag-Selection-Ignored fields.
	ignoredFields []string
}

const selectionBackendETag = `"ufc-v1-selection"`

func newSelectionBackend(t *testing.T, responses ...selectionResponse) *selectionBackend {
	t.Helper()
	b := &selectionBackend{responses: responses}
	b.server = httptest.NewServer(http.HandlerFunc(b.handle))
	t.Cleanup(b.server.Close)
	return b
}

func (b *selectionBackend) handle(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	b.rawQueries = append(b.rawQueries, r.URL.RawQuery)
	b.paths = append(b.paths, r.URL.Path)
	b.ifNoneMatch = append(b.ifNoneMatch, r.Header.Get("If-None-Match"))
	resp := b.responses[0]
	if len(b.responses) > 1 {
		b.responses = b.responses[1:]
	}
	b.mu.Unlock()

	if resp.ignored != nil {
		w.Header()[flagSelectionIgnoredHeader] = []string{*resp.ignored}
	}
	if resp.ignoredFields != nil {
		w.Header()[flagSelectionIgnoredHeader] = resp.ignoredFields
	}
	switch resp.status {
	case http.StatusOK:
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", selectionBackendETag)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(fakeUFCValidBody))
	case http.StatusNotModified:
		w.Header().Set("ETag", selectionBackendETag)
		w.WriteHeader(http.StatusNotModified)
	default:
		w.WriteHeader(resp.status)
	}
}

func (b *selectionBackend) requests() (rawQueries, paths, ifNoneMatch []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.rawQueries), slices.Clone(b.paths), slices.Clone(b.ifNoneMatch)
}

func newSelectionSource(t *testing.T, baseURL string, selection *FlagSelection, apply func(*universalFlagsConfiguration)) *agentlessSource {
	t.Helper()
	if apply == nil {
		apply = func(*universalFlagsConfiguration) {}
	}
	src, err := newAgentlessSource(internalffe.Settings{
		AgentlessBaseURL: baseURL,
		PollInterval:     time.Hour,
		RequestTimeout:   2 * time.Second,
	}, selection, apply)
	require.NoError(t, err)
	src.retryDelay = func(int) time.Duration { return 0 }
	return src
}

func TestAgentlessSource_SendsFlagSelectionAndRevalidatesWithItsETag(t *testing.T) {
	resetFlagSelectionReports(t)
	backend := newSelectionBackend(t, selectionResponse{status: http.StatusOK}, selectionResponse{status: http.StatusNotModified})
	src := newSelectionSource(t, backend.server.URL+"/custom/rules?dd_env=test", &FlagSelection{
		Tags:        []string{" area:b ", "area:a", "area:b"},
		KeyPatterns: []string{"checkout.*"},
	}, nil)

	outcome, _ := src.pollOnce(1)
	require.Equal(t, pollOutcomeSuccess, outcome)
	outcome, _ = src.pollOnce(1)
	require.Equal(t, pollOutcomeSuccess, outcome)

	rawQueries, paths, ifNoneMatch := backend.requests()
	const want = "dd_env=test&selected_flag_tags=area%3Aa&selected_flag_tags=area%3Ab&selected_flag_key_patterns=checkout.%2A"
	assert.Equal(t, []string{want, want}, rawQueries)
	assert.Equal(t, []string{"/custom/rules", "/custom/rules"}, paths)
	assert.Equal(t, []string{"", selectionBackendETag}, ifNoneMatch)
}

func TestAgentlessSource_NoFlagSelectionLeavesTheRequestUnchanged(t *testing.T) {
	for name, selection := range map[string]*FlagSelection{
		"nil":              nil,
		"empty":            {},
		"only white space": {Tags: []string{" ", ""}, KeyPatterns: []string{"\t"}},
	} {
		t.Run(name, func(t *testing.T) {
			resetFlagSelectionReports(t)
			logger := withLogLevel(t, log.LevelWarn)
			log.Flush()
			backend := newSelectionBackend(t, selectionResponse{status: http.StatusOK, ignored: new("invalid_tag")})
			plain, err := buildAgentlessEndpoint(backend.server.URL+"/rules?dd_env=test", "", "", "")
			require.NoError(t, err)

			src := newSelectionSource(t, backend.server.URL+"/rules?dd_env=test", selection, nil)
			assert.Equal(t, plain.url, src.endpoint)
			outcome, _ := src.pollOnce(1)
			require.Equal(t, pollOutcomeSuccess, outcome)

			rawQueries, _, _ := backend.requests()
			assert.Equal(t, []string{"dd_env=test"}, rawQueries)
			log.Flush()
			assert.Zero(t, logger.countContaining("FlagSelection"), "logs:\n%s", strings.Join(logger.all(), "\n"))
			assert.False(t, flagSelectionReportedFor(flagSelectionIgnoredMetric, "invalid_tag"), "the header must not be read without a selection")
		})
	}
}

func TestAgentlessSource_DroppedFlagSelectionRequestsTheFullConfiguration(t *testing.T) {
	resetFlagSelectionReports(t)
	logger := withLogLevel(t, log.LevelWarn)
	log.Flush()
	backend := newSelectionBackend(t, selectionResponse{status: http.StatusOK, ignored: new("too_many_values")})
	selection := &FlagSelection{Tags: numbered("t", 32)}

	var applied int
	src := newSelectionSource(t, backend.server.URL, selection, func(*universalFlagsConfiguration) { applied++ })
	newSelectionSource(t, backend.server.URL, selection, nil)
	outcome, _ := src.pollOnce(1)
	require.Equal(t, pollOutcomeSuccess, outcome)

	rawQueries, _, _ := backend.requests()
	assert.Equal(t, []string{""}, rawQueries)
	assert.Equal(t, 1, applied)
	log.Flush()
	assert.Equal(t, 1, logger.countContaining(wantDropTooManyValues+", 1 additional messages skipped"), "one error per provider construction:\n%s", strings.Join(logger.all(), "\n"))
	assert.Zero(t, logger.countContaining("t01"))
	assert.False(t, flagSelectionReportedFor(flagSelectionIgnoredMetric, "too_many_values"), "a selection that was not sent cannot be ignored")
}

func TestAgentlessSource_FlagSelectionIgnoredHeader(t *testing.T) {
	selection := &FlagSelection{Tags: []string{"mobile"}}

	t.Run("reported after the configuration is applied", func(t *testing.T) {
		resetFlagSelectionReports(t)
		logger := withLogLevel(t, log.LevelWarn)
		log.Flush()
		backend := newSelectionBackend(t, selectionResponse{status: http.StatusOK, ignored: new("tag_load_failed")})
		var reportedBeforeApply, applied bool
		src := newSelectionSource(t, backend.server.URL, selection, func(*universalFlagsConfiguration) {
			applied = true
			reportedBeforeApply = flagSelectionReportedFor(flagSelectionIgnoredMetric, "tag_load_failed")
		})

		outcome, _ := src.pollOnce(1)
		require.Equal(t, pollOutcomeSuccess, outcome)
		require.True(t, applied)
		assert.False(t, reportedBeforeApply)
		assert.True(t, flagSelectionReportedFor(flagSelectionIgnoredMetric, "tag_load_failed"))
		log.Flush()
		assert.Equal(t, 1, logger.countContaining(wantIgnored("tag_load_failed")))
	})

	t.Run("an unrecognized value is reported as unknown", func(t *testing.T) {
		resetFlagSelectionReports(t)
		logger := withLogLevel(t, log.LevelWarn)
		log.Flush()
		backend := newSelectionBackend(t, selectionResponse{status: http.StatusOK, ignored: new("Too_Many_Values")})
		src := newSelectionSource(t, backend.server.URL, selection, nil)

		outcome, _ := src.pollOnce(1)
		require.Equal(t, pollOutcomeSuccess, outcome)
		assert.True(t, flagSelectionReportedFor(flagSelectionIgnoredMetric, "unknown"))
		log.Flush()
		assert.Equal(t, 1, logger.countContaining(wantIgnored("unknown")))
		assert.Zero(t, logger.countContaining("Too_Many_Values"))
	})

	t.Run("reported once per process", func(t *testing.T) {
		resetFlagSelectionReports(t)
		logger := withLogLevel(t, log.LevelWarn)
		log.Flush()
		backend := newSelectionBackend(t, selectionResponse{status: http.StatusOK, ignored: new("invalid_tag")})
		first := newSelectionSource(t, backend.server.URL, selection, nil)
		second := newSelectionSource(t, backend.server.URL, selection, nil)

		for _, src := range []*agentlessSource{first, first, second} {
			outcome, _ := src.pollOnce(1)
			require.Equal(t, pollOutcomeSuccess, outcome)
		}
		log.Flush()
		assert.Equal(t, 1, logger.countContaining(wantIgnored("invalid_tag")+" (occurred: "), "logs:\n%s", strings.Join(logger.all(), "\n"))
	})

	for name, responses := range map[string][]selectionResponse{
		"not read on a 304": {
			{status: http.StatusOK},
			{status: http.StatusNotModified, ignored: new("invalid_tag")},
		},
		"not read on an error": {
			{status: http.StatusInternalServerError, ignored: new("invalid_tag")},
		},
		"an empty value counts as absent": {
			{status: http.StatusOK, ignored: new("")},
		},
		"not read on a 401": {
			{status: http.StatusUnauthorized, ignored: new("invalid_tag")},
		},
		"not read on a 403": {
			{status: http.StatusForbidden, ignored: new("invalid_tag")},
		},
		"not read on a 404": {
			{status: http.StatusNotFound, ignored: new("invalid_tag")},
		},
		"not read on a 429": {
			{status: http.StatusTooManyRequests, ignored: new("invalid_tag")},
		},
	} {
		t.Run(name, func(t *testing.T) {
			resetFlagSelectionReports(t)
			logger := withLogLevel(t, log.LevelWarn)
			log.Flush()
			backend := newSelectionBackend(t, responses...)
			src := newSelectionSource(t, backend.server.URL, selection, nil)

			for range responses {
				src.pollOnce(1)
			}
			rawQueries, _, _ := backend.requests()
			require.Len(t, rawQueries, len(responses))
			for _, q := range rawQueries {
				assert.Equal(t, "selected_flag_tags=mobile", q)
			}
			log.Flush()
			assert.Zero(t, logger.countContaining("was ignored by the server"))
			flagSelectionReported.Range(func(key, _ any) bool {
				assert.NotContains(t, key, flagSelectionIgnoredMetric)
				return true
			})
		})
	}

	for name, fields := range map[string][]string{
		"distinct repeated fields":         {"invalid_tag", "tag_load_failed"},
		"identical repeated fields":        {"invalid_tag", "invalid_tag"},
		"fields with an empty first value": {"", "invalid_tag"},
		"repeated empty fields":            {"", ""},
	} {
		t.Run(name+" sanitize to unknown", func(t *testing.T) {
			resetFlagSelectionReports(t)
			logger := withLogLevel(t, log.LevelWarn)
			log.Flush()
			backend := newSelectionBackend(t, selectionResponse{status: http.StatusOK, ignoredFields: fields})
			src := newSelectionSource(t, backend.server.URL, selection, nil)

			outcome, _ := src.pollOnce(1)
			require.Equal(t, pollOutcomeSuccess, outcome)
			assert.Equal(t, selectionBackendETag, src.currentETag())
			assert.True(t, flagSelectionReportedFor(flagSelectionIgnoredMetric, "unknown"))
			for _, reason := range []string{"invalid_tag", "tag_load_failed"} {
				assert.False(t, flagSelectionReportedFor(flagSelectionIgnoredMetric, reason))
			}
			log.Flush()
			assert.Equal(t, 1, logger.countContaining(wantIgnored("unknown")))
		})
	}
}

// log.Error only aggregates and prints later from the log package, so this
// exercises a panicking logger on the synchronous paths: the Remote Config
// warning and the poll itself.
func TestAgentlessSource_FlagSelectionDiagnosticsWithAPanickingLogger(t *testing.T) {
	resetFlagSelectionReports(t)
	withLogLevel(t, log.LevelWarn)
	log.Flush()
	// Runs after the panicking logger is removed, so the errors aggregated
	// below are not left for another test to print.
	t.Cleanup(log.Flush)
	backend := newSelectionBackend(t, selectionResponse{status: http.StatusOK, ignored: new("tag_load_failed")})
	t.Cleanup(log.UseLogger(panickingLogger{}))

	var applied bool
	dropped := newSelectionSource(t, backend.server.URL, &FlagSelection{Tags: []string{strings.Repeat("a", 201)}}, nil)
	sent := newSelectionSource(t, backend.server.URL, &FlagSelection{Tags: []string{"mobile"}}, func(*universalFlagsConfiguration) { applied = true })
	assert.NotPanics(t, func() {
		outcome, _ := dropped.pollOnce(1)
		assert.Equal(t, pollOutcomeSuccess, outcome)
		outcome, _ = sent.pollOnce(1)
		assert.Equal(t, pollOutcomeSuccess, outcome)
	})
	assert.True(t, applied)
	assert.True(t, flagSelectionReportedFor(flagSelectionIgnoredMetric, "tag_load_failed"))
	assert.NotPanics(t, func() { warnFlagSelectionWithRemoteConfig(&FlagSelection{Tags: []string{"mobile"}}) })
}

func TestStartWithAgentless_EachProviderSendsItsOwnFlagSelection(t *testing.T) {
	resetFlagSelectionReports(t)
	backend := newSelectionBackend(t, selectionResponse{status: http.StatusOK})
	settings := internalffe.Settings{
		AgentlessBaseURL: backend.server.URL,
		PollInterval:     time.Hour,
		RequestTimeout:   2 * time.Second,
	}
	checkout := &FlagSelection{KeyPatterns: []string{"checkout.*"}}
	providers := make([]*DatadogProvider, 0, 3)
	for _, selection := range []*FlagSelection{checkout, {Tags: []string{"mobile"}}, nil} {
		p, err := startWithAgentless(ProviderConfig{FlagSelection: selection}, settings)
		require.NoError(t, err)
		providers = append(providers, p)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			require.NoError(t, p.ShutdownWithContext(ctx))
		})
	}
	// The provider copied the selection at construction.
	checkout.KeyPatterns[0] = "changed.*"

	require.Eventually(t, func() bool {
		rawQueries, _, _ := backend.requests()
		return len(rawQueries) == 3
	}, 5*time.Second, time.Millisecond)
	rawQueries, _, _ := backend.requests()
	assert.ElementsMatch(t, []string{"selected_flag_key_patterns=checkout.%2A", "selected_flag_tags=mobile", ""}, rawQueries)
	// The backend serves a configuration with no flags, as for a selection that
	// matches none: the provider applies it like any other.
	for _, p := range providers {
		require.Eventually(t, func() bool { return p.getConfiguration() != nil }, 5*time.Second, time.Millisecond)
		assert.Empty(t, p.getConfiguration().Flags)
	}
}

func TestNewDatadogProvider_FlagSelectionWithRemoteConfig(t *testing.T) {
	internalconfig.SetUseFreshConfig(true)
	t.Cleanup(func() { internalconfig.SetUseFreshConfig(false) })

	newRemoteConfigProvider := func(t *testing.T, selection *FlagSelection) {
		t.Helper()
		internalffe.ResetForTest()
		internalffe.SetSubscribedForTest(true)
		t.Cleanup(internalffe.ResetForTest)
		p, err := NewDatadogProvider(ProviderConfig{FlagSelection: selection})
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, p.(*DatadogProvider).ShutdownWithContext(ctx))
	}

	t.Run("warns once per process when a selection is set", func(t *testing.T) {
		resetFlagSelectionReports(t)
		logger := withLogLevel(t, log.LevelWarn)
		log.Flush()
		t.Setenv("DD_FEATURE_FLAGS_CONFIGURATION_SOURCE", "remote_config")

		newRemoteConfigProvider(t, &FlagSelection{Tags: []string{"mobile"}})
		newRemoteConfigProvider(t, &FlagSelection{KeyPatterns: []string{"checkout.*"}})
		log.Flush()

		assert.Equal(t, 1, logger.countContaining(wantRemoteConfigWarning), "logs:\n%s", strings.Join(logger.all(), "\n"))
		assert.Zero(t, logger.countContaining("was not sent"))
	})

	t.Run("a selection that fails validation is set", func(t *testing.T) {
		resetFlagSelectionReports(t)
		logger := withLogLevel(t, log.LevelWarn)
		log.Flush()
		t.Setenv("DD_FEATURE_FLAGS_CONFIGURATION_SOURCE", "remote_config")

		newRemoteConfigProvider(t, &FlagSelection{Tags: numbered("t", 32)})
		log.Flush()

		assert.Equal(t, 1, logger.countContaining(wantRemoteConfigWarning))
		assert.Zero(t, logger.countContaining("was not sent"))
		assert.False(t, flagSelectionReportedFor(flagSelectionDroppedMetric, "too_many_values"), "Remote Config mode sends no metric")
	})

	t.Run("the legacy experimental setting maps to Remote Config", func(t *testing.T) {
		resetFlagSelectionReports(t)
		logger := withLogLevel(t, log.LevelWarn)
		t.Setenv("DD_EXPERIMENTAL_FLAGGING_PROVIDER_ENABLED", "true")

		newRemoteConfigProvider(t, &FlagSelection{Tags: []string{"mobile"}})

		assert.Equal(t, 1, logger.countContaining(wantRemoteConfigWarning))
	})

	for name, selection := range map[string]*FlagSelection{
		"no warning without a selection":                   nil,
		"no warning for a selection that is empty":         {Tags: []string{" "}},
		"no warning for a selection with only empty lists": {Tags: []string{}, KeyPatterns: []string{}},
	} {
		t.Run(name, func(t *testing.T) {
			resetFlagSelectionReports(t)
			logger := withLogLevel(t, log.LevelWarn)
			t.Setenv("DD_FEATURE_FLAGS_CONFIGURATION_SOURCE", "remote_config")

			newRemoteConfigProvider(t, selection)

			assert.Zero(t, logger.countContaining("FlagSelection"))
		})
	}

	t.Run("no warning when flagging is disabled", func(t *testing.T) {
		resetFlagSelectionReports(t)
		logger := withLogLevel(t, log.LevelWarn)
		t.Setenv("DD_FEATURE_FLAGS_CONFIGURATION_SOURCE", "offline")

		_, err := NewDatadogProvider(ProviderConfig{FlagSelection: &FlagSelection{Tags: []string{"mobile"}}})
		require.NoError(t, err)

		assert.Zero(t, logger.countContaining("FlagSelection"))
	})
}

func TestReportFlagSelectionIgnored_EachKnownReasonVerbatim(t *testing.T) {
	resetFlagSelectionReports(t)
	logger := withLogLevel(t, log.LevelWarn)
	log.Flush()

	reasons := []string{"malformed_selection", "too_many_values", "invalid_key_pattern", "invalid_tag", "tag_load_failed"}
	for _, reason := range reasons {
		reportFlagSelectionIgnored(reason)
	}
	log.Flush()

	for _, reason := range reasons {
		assert.Equal(t, 1, logger.countContaining(wantIgnored(reason)+" (occurred: "), "want exactly one line for %s in:\n%s", reason, strings.Join(logger.all(), "\n"))
	}
	assert.Zero(t, logger.countContaining(wantIgnored("unknown")))
}

// With DD_LOGGING_RATE=0, log.Error prints synchronously, so this is the only
// configuration in which containLoggerPanic guards the error-level reports.
// The log package reads DD_LOGGING_RATE once at init, hence the subprocess.
func TestFlagSelectionDiagnostics_PanickingLoggerWithImmediateErrors(t *testing.T) {
	if os.Getenv("DD_TEST_SUBPROCESS") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestFlagSelectionDiagnostics_PanickingLoggerWithImmediateErrors$", "-test.count=1")
		cmd.Env = append(os.Environ(), "DD_TEST_SUBPROCESS=1", "DD_LOGGING_RATE=0")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)
		return
	}
	resetFlagSelectionReports(t)
	withLogLevel(t, log.LevelWarn)
	backend := newSelectionBackend(t, selectionResponse{status: http.StatusOK, ignored: new("tag_load_failed")})
	t.Cleanup(log.UseLogger(panickingLogger{}))

	var applied bool
	assert.NotPanics(t, func() {
		newSelectionSource(t, backend.server.URL, &FlagSelection{Tags: numbered("t", 32)}, nil)
		sent := newSelectionSource(t, backend.server.URL, &FlagSelection{Tags: []string{"mobile"}}, func(*universalFlagsConfiguration) { applied = true })
		outcome, _ := sent.pollOnce(1)
		assert.Equal(t, pollOutcomeSuccess, outcome)
	})
	assert.True(t, applied)
}

func TestNewAgentlessSource_EndpointErrorSkipsFlagSelectionDiagnostics(t *testing.T) {
	resetFlagSelectionReports(t)
	logger := withLogLevel(t, log.LevelWarn)
	log.Flush()

	_, err := newAgentlessSource(internalffe.Settings{}, &FlagSelection{Tags: numbered("t", 32)}, func(*universalFlagsConfiguration) {})
	require.ErrorIs(t, err, errAgentlessNoAPIKey)
	log.Flush()
	assert.Zero(t, logger.countContaining("FlagSelection"))
	assert.False(t, flagSelectionReportedFor(flagSelectionDroppedMetric, "too_many_values"))
}
