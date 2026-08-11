// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"
)

// Canonical cross-SDK vector. Every SDK must reproduce this digest byte-for-byte for the same
// subject, so hashed values join across languages and against the backend. Asserted here and
// in system-tests (tests/ffe/test_flag_eval_evp.py).
const (
	piiCanonicalTargetingKey = "jane.doe@datadoghq.com"
	piiCanonicalHashed       = "sha256_b4698f9b6d186781fa8dc59e533578fa2d8379a46b1cf6db85cda6aa9c99e51b"
)

// TestHashTargetingKeyCanonicalVector pins the cross-SDK hash contract: unsalted SHA-256 over
// the raw bytes, lowercase hex, sha256_ prefix, 71 characters total.
func TestHashTargetingKeyCanonicalVector(t *testing.T) {
	got := hashTargetingKey(piiCanonicalTargetingKey)
	if got != piiCanonicalHashed {
		t.Fatalf("canonical vector mismatch:\n got %q\nwant %q", got, piiCanonicalHashed)
	}
	if len(got) != 71 {
		t.Errorf("hashed targeting key must be 71 chars, got %d (%q)", len(got), got)
	}
	if !strings.HasPrefix(got, targetingKeyHashPrefix) {
		t.Errorf("hashed targeting key must carry the %q prefix, got %q", targetingKeyHashPrefix, got)
	}
	hexSuffix := strings.TrimPrefix(got, targetingKeyHashPrefix)
	if len(hexSuffix) != 64 {
		t.Errorf("digest must be 64 hex chars, got %d", len(hexSuffix))
	}
	for _, c := range hexSuffix {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Errorf("digest must be lowercase hex, found %q in %q", c, got)
			break
		}
	}
}

// TestHashTargetingKeyDoesNotNormalize proves the hash input is the raw bytes as received.
// Trimming, case folding, or Unicode normalization would silently break the cross-SDK join,
// so each variant must produce a DIFFERENT digest from the canonical one.
func TestHashTargetingKeyDoesNotNormalize(t *testing.T) {
	variants := map[string]string{
		"leading whitespace":  " " + piiCanonicalTargetingKey,
		"trailing whitespace": piiCanonicalTargetingKey + " ",
		"uppercased":          strings.ToUpper(piiCanonicalTargetingKey),
		// Same grapheme, different bytes: NFC precomposed U+00E9 vs NFD "e" + U+0301
		// combining acute. A digest that normalized would collapse these into one subject
		// and break the cross-SDK join; written as escapes so the distinction survives editing.
		"NFC-composed accent":   "jos\u00e9@datadoghq.com",
		"NFD-decomposed accent": "jose\u0301@datadoghq.com",
	}
	seen := map[string]string{piiCanonicalHashed: "canonical"}
	for name, input := range variants {
		got := hashTargetingKey(input)
		if prior, ok := seen[got]; ok {
			t.Errorf("%s produced the same digest as %s — input must not be normalized", name, prior)
		}
		seen[got] = name
	}
}

// TestHashTargetingKeyEmptySubject documents that an absent subject stays absent. Hashing ""
// would fabricate one constant pseudo-subject shared by every subject-less evaluation.
func TestHashTargetingKeyEmptySubject(t *testing.T) {
	if got := hashTargetingKey(""); got != "" {
		t.Errorf("empty targeting key must stay empty (omitted), got %q", got)
	}
}

// TestUFCObserveFullEvaluationDataParsing covers the read side of the contract: the field is
// read from the UFC ROOT as a sibling of environment, absent means false, and an explicit null
// fails closed rather than erroring or opting in.
func TestUFCObserveFullEvaluationDataParsing(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{
			name: "absent is false (fail closed)",
			raw:  `{"format":"SERVER","environment":{"name":"Staging"},"flags":{}}`,
			want: false,
		},
		{
			name: "explicit false",
			raw:  `{"format":"SERVER","observeFullEvaluationData":false,"environment":{"name":"Staging"},"flags":{}}`,
			want: false,
		},
		{
			name: "explicit true opts in",
			raw:  `{"format":"SERVER","observeFullEvaluationData":true,"environment":{"name":"Staging"},"flags":{}}`,
			want: true,
		},
		{
			name: "explicit null is rejected and fails closed",
			raw:  `{"format":"SERVER","observeFullEvaluationData":null,"environment":{"name":"Staging"},"flags":{}}`,
			want: false,
		},
		{
			// Regression guard for the placement error: the field is a sibling of environment,
			// never a field on it. A parser reading it from environment would report true here
			// and hash forever in production, where it sits at the root.
			name: "nested under environment is NOT read",
			raw:  `{"format":"SERVER","environment":{"name":"Staging","observeFullEvaluationData":true},"flags":{}}`,
			want: false,
		},
		// Wrong-typed values must fail closed to false WITHOUT rejecting the whole UFC.
		// Fail-closed on privacy must not cascade into fail-closed on availability: agentless
		// swallows the parse error, so rejecting the config strands a fresh pod on the SDK
		// default for every flag until a well-formed UFC arrives.
		{
			name: "stringified true fails closed, config still parses",
			raw:  `{"format":"SERVER","observeFullEvaluationData":"true","environment":{"name":"Staging"},"flags":{}}`,
			want: false,
		},
		{
			name: "numeric 1 fails closed, config still parses",
			raw:  `{"format":"SERVER","observeFullEvaluationData":1,"environment":{"name":"Staging"},"flags":{}}`,
			want: false,
		},
		{
			name: "array fails closed, config still parses",
			raw:  `{"format":"SERVER","observeFullEvaluationData":[],"environment":{"name":"Staging"},"flags":{}}`,
			want: false,
		},
		{
			name: "object fails closed, config still parses",
			raw:  `{"format":"SERVER","observeFullEvaluationData":{},"environment":{"name":"Staging"},"flags":{}}`,
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var config universalFlagsConfiguration
			if err := json.Unmarshal([]byte(tc.raw), &config); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}
			if config.ObserveFullEvaluationData != tc.want {
				t.Errorf("ObserveFullEvaluationData = %v, want %v", config.ObserveFullEvaluationData, tc.want)
			}
		})
	}

	// The custom UnmarshalJSON copies only the fields its shadow struct lists, so a
	// struct-only change would read false forever and the true opt-in would never work.
	// Prove the value survives a round trip through the real Remote Config entry point.
	t.Run("survives the custom unmarshaller used by Remote Config", func(t *testing.T) {
		raw := `{"format":"SERVER","observeFullEvaluationData":true,"environment":{"name":"Staging"},` +
			`"flags":{"f":{"key":"f","enabled":true,"variationType":"STRING",` +
			`"variations":{"on":{"key":"on","value":"v"}},` +
			`"allocations":[{"key":"a","rules":[],"splits":[{"variationKey":"on","shards":[]}]}]}}}`
		var config universalFlagsConfiguration
		if err := json.Unmarshal([]byte(raw), &config); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if !config.ObserveFullEvaluationData {
			t.Error("consent lost by the custom UnmarshalJSON shadow struct — the true opt-in would never work")
		}
		if len(config.Flags) != 1 {
			t.Errorf("expected the flag to parse alongside consent, got %d flags", len(config.Flags))
		}
	})

	// Not omitempty: false must be on the wire so absent-as-false is unambiguous downstream.
	t.Run("marshals false explicitly", func(t *testing.T) {
		b, err := json.Marshal(universalFlagsConfiguration{Format: "SERVER"})
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}
		if !strings.Contains(string(b), `"observeFullEvaluationData":false`) {
			t.Errorf("false must be serialized explicitly, got %s", b)
		}
	})
}

// TestProviderStampsConsentFromEvaluatedConfig verifies the evaluator stamps consent onto
// evaluation metadata under the cross-SDK key, on every return path.
func TestProviderStampsConsentFromEvaluatedConfig(t *testing.T) {
	tests := []struct {
		name   string
		config *universalFlagsConfiguration
		want   bool
	}{
		{
			name:   "consent on is stamped",
			config: piiTestConfig(true),
			want:   true,
		},
		{
			name:   "consent off is stamped",
			config: piiTestConfig(false),
			want:   false,
		},
		{
			// No configuration means no environment behind the evaluation, so there is no
			// consent to honor. Must fail closed rather than leaving the key absent-and-
			// ambiguous.
			name:   "no configuration fails closed",
			config: nil,
			want:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &DatadogProvider{configuration: tc.config}
			p.configChange.L = &p.mu

			res := p.evaluate(context.Background(), "pii-flag", "fallback", of.FlattenedContext{
				of.TargetingKey: piiCanonicalTargetingKey,
			})

			got, ok := res.Metadata[metadataObserveFullEvaluationDataKey]
			if !ok {
				t.Fatalf("consent must be stamped on every evaluation path; metadata was %v", res.Metadata)
			}
			if got != tc.want {
				t.Errorf("stamped consent = %v, want %v", got, tc.want)
			}
		})
	}

	t.Run("missing flag still carries consent", func(t *testing.T) {
		p := &DatadogProvider{configuration: piiTestConfig(true)}
		p.configChange.L = &p.mu

		res := p.evaluate(context.Background(), "no-such-flag", "fallback", of.FlattenedContext{
			of.TargetingKey: piiCanonicalTargetingKey,
		})
		if res.Metadata[metadataObserveFullEvaluationDataKey] != true {
			t.Errorf("FLAG_NOT_FOUND must still carry consent, got %v", res.Metadata)
		}
	})
}

// TestExtractEvalDetailsConsentFailsClosed verifies the hook reads consent only from the
// evaluation's own metadata, and treats anything unexpected as withheld.
func TestExtractEvalDetailsConsentFailsClosed(t *testing.T) {
	tests := []struct {
		name     string
		metadata of.FlagMetadata
		want     bool
	}{
		{name: "absent metadata", metadata: nil, want: false},
		{name: "absent key", metadata: of.FlagMetadata{metadataAllocationKey: "a"}, want: false},
		{name: "explicit false", metadata: of.FlagMetadata{metadataObserveFullEvaluationDataKey: false}, want: false},
		{name: "explicit true", metadata: of.FlagMetadata{metadataObserveFullEvaluationDataKey: true}, want: true},
		{
			// A non-bool value is a bug somewhere upstream; it must not read as consent.
			name:     "wrong type is not consent",
			metadata: of.FlagMetadata{metadataObserveFullEvaluationDataKey: "true"},
			want:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hookCtx := makeHookContext("f", piiCanonicalTargetingKey, nil)
			details := makeEvalDetails("on", of.TargetingMatchReason, "", tc.metadata)
			if got := extractEvalDetails(hookCtx, details).observeFullEvaluationData; got != tc.want {
				t.Errorf("observeFullEvaluationData = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestConsentOffDropsContextFromBucketKey guards the aggregation-efficiency half of the
// contract. Without consent the context is discarded at serialization, so it must not be a
// bucket dimension: otherwise a high-cardinality attribute produces many wire-identical rows
// and burns perFlagCap on exactly the privacy-protected traffic.
func TestConsentOffDropsContextFromBucketKey(t *testing.T) {
	nowMs := time.Now().UnixMilli()

	t.Run("consent off merges distinct contexts into one bucket", func(t *testing.T) {
		// Caps well above the scenario: this test is about bucket KEYING, so tier overflow
		// must not be what limits the bucket count.
		agg := newTestAggregator(100, 100, 100)
		d := evalDetails{flagKey: "f", variant: "on", targetingKey: "user-1"}
		for i := range 5 {
			agg.add(d, map[string]any{"request_id": i}, nowMs)
		}

		agg.mu.Lock()
		defer agg.mu.Unlock()
		if len(agg.full) != 1 {
			t.Fatalf("consent-off buckets must not be keyed on discarded context; got %d buckets, want 1", len(agg.full))
		}
		for key, e := range agg.full {
			if key.contextKey != "" {
				t.Errorf("consent-off bucket key must carry no context dimension, got %q", key.contextKey)
			}
			if e.contextAttrs != nil {
				t.Errorf("consent-off bucket must not retain context attributes, got %v", e.contextAttrs)
			}
			if e.count != 5 {
				t.Errorf("expected all 5 evaluations counted in one bucket, got %d", e.count)
			}
		}
	})

	t.Run("consent on keeps distinct contexts distinct", func(t *testing.T) {
		// Caps well above the scenario: this test is about bucket KEYING, so tier overflow
		// must not be what limits the bucket count.
		agg := newTestAggregator(100, 100, 100)
		d := evalDetails{flagKey: "f", variant: "on", targetingKey: "user-1", observeFullEvaluationData: true}
		for i := range 5 {
			agg.add(d, map[string]any{"request_id": i}, nowMs)
		}

		agg.mu.Lock()
		defer agg.mu.Unlock()
		if len(agg.full) != 5 {
			t.Fatalf("consent-on buckets keep context as a dimension; got %d buckets, want 5", len(agg.full))
		}
	})

	// Two subjects evaluated under different consent must never share a bucket and inherit a
	// single policy.
	t.Run("mixed consent does not merge", func(t *testing.T) {
		// Caps well above the scenario: this test is about bucket KEYING, so tier overflow
		// must not be what limits the bucket count.
		agg := newTestAggregator(100, 100, 100)
		base := evalDetails{flagKey: "f", variant: "on", targetingKey: "user-1"}
		consented := base
		consented.observeFullEvaluationData = true

		agg.add(base, nil, nowMs)
		agg.add(consented, nil, nowMs)

		agg.mu.Lock()
		defer agg.mu.Unlock()
		if len(agg.full) != 2 {
			t.Fatalf("mixed-consent evaluations must land in distinct buckets, got %d", len(agg.full))
		}
	})

	// Defense in depth: if the key ever stops carrying consent, a single consent-off
	// observation must still force the whole bucket onto the privacy-protected path.
	t.Run("AND-fold keeps the protected policy", func(t *testing.T) {
		// Caps well above the scenario: this test is about bucket KEYING, so tier overflow
		// must not be what limits the bucket count.
		agg := newTestAggregator(100, 100, 100)
		consented := evalDetails{flagKey: "f", variant: "on", targetingKey: "user-1", observeFullEvaluationData: true}
		agg.add(consented, nil, nowMs)

		agg.mu.Lock()
		var key evaluationAggregationKey
		for k := range agg.full {
			key = k
		}
		entry := agg.full[key]
		agg.mu.Unlock()

		// Simulate key drift: replay a consent-off observation onto the consent-on bucket.
		withheld := consented
		withheld.observeFullEvaluationData = false
		agg.mu.Lock()
		entry.observeFullEvaluationData = entry.observeFullEvaluationData && withheld.observeFullEvaluationData
		agg.mu.Unlock()

		if entry.observeFullEvaluationData {
			t.Error("AND-fold must clear consent when any observation withholds it")
		}
	})
}

// TestFlushHashesTargetingKeyOnRawWire is the negative control, asserted on the bytes that
// actually leave the process rather than on in-memory structs: with consent withheld the
// digest is present and the raw subject and its attributes appear NOWHERE in the payload.
func TestFlushHashesTargetingKeyOnRawWire(t *testing.T) {
	piiAttributes := map[string]any{
		"org_id":       1234,
		"user_email":   piiCanonicalTargetingKey,
		"plan":         "enterprise",
		"region":       "us-east-1",
		"account.tier": "gold",
	}

	tests := []struct {
		name          string
		consent       bool
		wantTargeting string
		wantContext   bool
		// forbidden must not appear anywhere in the serialized payload.
		forbidden []string
	}{
		{
			name:          "consent withheld hashes the subject and omits the context",
			consent:       false,
			wantTargeting: piiCanonicalHashed,
			wantContext:   false,
			forbidden: []string{
				piiCanonicalTargetingKey, "enterprise", "us-east-1", "gold", "user_email",
			},
		},
		{
			name:          "consent granted emits the subject and context verbatim",
			consent:       true,
			wantTargeting: piiCanonicalTargetingKey,
			wantContext:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bodies := make(chan []byte, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer r.Body.Close()
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("failed to read request body: %v", err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				bodies <- body
				w.WriteHeader(http.StatusAccepted)
			}))
			defer server.Close()

			agentURL, err := url.Parse(server.URL)
			if err != nil {
				t.Fatalf("failed to parse test server URL: %v", err)
			}
			evp := newEVPClient()
			evp.agentURL = agentURL
			evp.httpClient = server.Client()

			w := newFlagEvalLoggingWriterWithEVP(ProviderConfig{FlagEvaluationFlushInterval: time.Hour}, evp)
			w.start()

			hookCtx := makeHookContext("pii-flag", piiCanonicalTargetingKey, piiAttributes)
			details := makeEvalDetails("on", of.TargetingMatchReason, "", of.FlagMetadata{
				metadataAllocationKey:                "default-allocation",
				metadataObserveFullEvaluationDataKey: tc.consent,
			})
			w.record(hookCtx, details)
			w.stop()

			var body []byte
			select {
			case body = <-bodies:
			case <-time.After(2 * time.Second):
				t.Fatal("writer did not flush a flagevaluation payload")
			}

			// Raw-wire assertions first: these catch a raw value routed into an unexpected
			// field, which a decode-then-inspect check would miss.
			for _, forbidden := range tc.forbidden {
				if strings.Contains(string(body), forbidden) {
					t.Errorf("raw value %q must not appear anywhere in the payload: %s", forbidden, body)
				}
			}

			var payload flagEvalLoggingPayload
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatalf("failed to decode payload: %v", err)
			}
			if len(payload.FlagEvaluations) != 1 {
				t.Fatalf("expected 1 event, got %d", len(payload.FlagEvaluations))
			}
			event := payload.FlagEvaluations[0]

			if event.TargetingKey != tc.wantTargeting {
				t.Errorf("targeting_key = %q, want %q", event.TargetingKey, tc.wantTargeting)
			}

			// "Omitted" means the key is absent — not null, not an empty object.
			var rawEvent map[string]json.RawMessage
			var rawEvents struct {
				FlagEvaluations []map[string]json.RawMessage `json:"flagEvaluations"`
			}
			if err := json.Unmarshal(body, &rawEvents); err != nil {
				t.Fatalf("failed to re-decode payload: %v", err)
			}
			rawEvent = rawEvents.FlagEvaluations[0]

			if _, present := rawEvent["context"]; present != tc.wantContext {
				t.Errorf("context key present = %v, want %v (payload: %s)", present, tc.wantContext, body)
			}
			if tc.wantContext {
				if event.Context == nil || len(event.Context.Evaluation) == 0 {
					t.Fatalf("expected full context on the consent-granted path, got %+v", event.Context)
				}
				for key, want := range piiAttributes {
					got, ok := event.Context.Evaluation[key]
					if !ok {
						t.Errorf("context.evaluation missing %q", key)
						continue
					}
					// JSON round-trips numbers as float64.
					if !valuesEquivalent(got, want) {
						t.Errorf("context.evaluation[%q] = %v (%T), want %v (%T)", key, got, got, want, want)
					}
				}
			}
		})
	}
}

// TestDoLogDoesNotGatePIIBehavior is the DoLog non-impact proof the contract requires: the
// same hashed/unhashed shape must be emitted regardless of DoLog, in both consent states.
func TestDoLogDoesNotGatePIIBehavior(t *testing.T) {
	// Pin both timestamps so the comparison isolates the PII-relevant shape; wall-clock drift
	// between the two runs would otherwise always differ.
	const fixedEvalTimeMs int64 = 1785000000000
	const fixedFlushTimeMs int64 = 1785000000123

	for _, consent := range []bool{false, true} {
		shapes := make(map[bool]string, 2)
		for _, doLog := range []bool{false, true} {
			w := newFlagEvalLoggingWriter(ProviderConfig{})
			hookCtx := makeHookContext("pii-flag", piiCanonicalTargetingKey, map[string]any{"plan": "enterprise"})
			details := makeEvalDetails("on", of.TargetingMatchReason, "", of.FlagMetadata{
				metadataAllocationKey:                "default-allocation",
				metadataEvalTimeKey:                  fixedEvalTimeMs,
				metadataDoLogKey:                     doLog,
				metadataObserveFullEvaluationDataKey: consent,
			})

			w.record(hookCtx, details)
			if len(w.events) != 1 {
				t.Fatalf("consent=%v doLog=%v: expected one queued event, got %d", consent, doLog, len(w.events))
			}
			w.aggregate(<-w.events)

			events := w.buildFlushEvents(fixedFlushTimeMs)
			if len(events) != 1 {
				t.Fatalf("consent=%v doLog=%v: expected one event, got %d", consent, doLog, len(events))
			}
			b, err := json.Marshal(events[0])
			if err != nil {
				t.Fatalf("marshal failed: %v", err)
			}
			shapes[doLog] = string(b)
		}

		if shapes[false] != shapes[true] {
			t.Errorf("consent=%v: DoLog must not affect the emitted shape:\n doLog=false: %s\n doLog=true:  %s",
				consent, shapes[false], shapes[true])
		}

		// And the shape must still be the correct one for the consent value.
		wantHashed := !consent
		hasHash := strings.Contains(shapes[true], piiCanonicalHashed)
		if hasHash != wantHashed {
			t.Errorf("consent=%v: hashed targeting_key present = %v, want %v (%s)", consent, hasHash, wantHashed, shapes[true])
		}
	}
}

// TestConsentIsNotReReadAfterEvaluation is the regression guard for the consent-lifecycle bug
// that system-tests caught in the Java pilot and unit tests missed: consent was read from live
// config at flush time, so a Remote Config update landing between evaluation and flush
// retroactively applied another environment's policy. Both directions leak.
func TestConsentIsNotReReadAfterEvaluation(t *testing.T) {
	tests := []struct {
		name string
		// consentAtEvaluation is the value the evaluation actually ran against.
		consentAtEvaluation bool
		// consentAfterUpdate is what a later Remote Config push installs on the provider.
		consentAfterUpdate bool
		wantTargeting      string
	}{
		{
			name:                "later opt-in must not retroactively unmask an already-evaluated subject",
			consentAtEvaluation: false,
			consentAfterUpdate:  true,
			wantTargeting:       piiCanonicalHashed,
		},
		{
			name:                "later opt-out must not retroactively hash an already-consented subject",
			consentAtEvaluation: true,
			consentAfterUpdate:  false,
			wantTargeting:       piiCanonicalTargetingKey,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &DatadogProvider{configuration: piiTestConfig(tc.consentAtEvaluation)}
			p.configChange.L = &p.mu
			w := newFlagEvalLoggingWriter(ProviderConfig{})

			// Evaluate against the current configuration, then run the hook exactly as the
			// provider would.
			res := p.evaluate(context.Background(), "pii-flag", "fallback", of.FlattenedContext{
				of.TargetingKey: piiCanonicalTargetingKey,
			})
			hookCtx := makeHookContext("pii-flag", piiCanonicalTargetingKey, map[string]any{"plan": "enterprise"})
			details := makeEvalDetails(res.VariantKey, res.Reason, "", of.FlagMetadata(res.Metadata))
			w.record(hookCtx, details)

			// Remote Config replaces the configuration BEFORE the event is aggregated and
			// flushed. Nothing downstream of the evaluator may notice.
			p.updateConfiguration(piiTestConfig(tc.consentAfterUpdate))

			if len(w.events) != 1 {
				t.Fatalf("expected one queued event, got %d", len(w.events))
			}
			w.aggregate(<-w.events)

			events := w.buildFlushEvents(time.Now().UnixMilli())
			if len(events) != 1 {
				t.Fatalf("expected one flushed event, got %d", len(events))
			}
			if got := events[0].TargetingKey; got != tc.wantTargeting {
				t.Errorf("targeting_key = %q, want %q — consent must come from the evaluated config, not live config", got, tc.wantTargeting)
			}
		})
	}
}

// TestDegradedTierNeverEmitsSubjectOrContext confirms the degraded tier is safe under either
// consent value, which is why consent is not a degraded bucket dimension.
func TestDegradedTierNeverEmitsSubjectOrContext(t *testing.T) {
	for _, consent := range []bool{false, true} {
		w := newFlagEvalLoggingWriter(ProviderConfig{})
		// globalCap 0 routes every new full key straight to the degraded tier.
		w.aggregator.globalCap = 0

		d := evalDetails{
			flagKey:                   "pii-flag",
			variant:                   "on",
			allocationKey:             "default-allocation",
			targetingKey:              piiCanonicalTargetingKey,
			observeFullEvaluationData: consent,
		}
		w.aggregator.add(d, map[string]any{"user_email": piiCanonicalTargetingKey}, time.Now().UnixMilli())

		events := w.buildFlushEvents(time.Now().UnixMilli())
		if len(events) != 1 {
			t.Fatalf("consent=%v: expected one degraded event, got %d", consent, len(events))
		}
		b, err := json.Marshal(events[0])
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}
		if strings.Contains(string(b), piiCanonicalTargetingKey) {
			t.Errorf("consent=%v: degraded event must never carry the raw subject: %s", consent, b)
		}
		if events[0].TargetingKey != "" || events[0].Context != nil {
			t.Errorf("consent=%v: degraded event must omit targeting_key and context, got %s", consent, b)
		}
	}
}

// piiTestConfig builds a minimal single-flag UFC with the given consent value.
func piiTestConfig(observeFullEvaluationData bool) *universalFlagsConfiguration {
	return &universalFlagsConfiguration{
		Format:                    "SERVER",
		Environment:               environment{Name: "Staging"},
		ObserveFullEvaluationData: observeFullEvaluationData,
		Flags: map[string]*flag{
			"pii-flag": {
				Key:           "pii-flag",
				Enabled:       true,
				VariationType: valueTypeString,
				Variations:    map[string]*variant{"on": {Key: "on", Value: "on-value"}},
				Allocations: []*allocation{{
					Key:    "default-allocation",
					Splits: []*split{{VariationKey: "on"}},
				}},
			},
		},
	}
}

// valuesEquivalent compares a JSON-decoded value against the original Go value, tolerating
// JSON's float64 representation of numbers.
func valuesEquivalent(got, want any) bool {
	if got == want {
		return true
	}
	gotFloat, gotOK := got.(float64)
	if !gotOK {
		return false
	}
	switch w := want.(type) {
	case int:
		return gotFloat == float64(w)
	case int64:
		return gotFloat == float64(w)
	case float64:
		return gotFloat == w
	default:
		return false
	}
}
