// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

// Rules used by the AppSec tests. "type" is the tags.type of the rule: the WAF
// does not report a second rule of the same type that has no block action.
// Conditions are written for server.request.path_params (key_path = the name of
// the parameter), except for the header and mixed rules.
const (
	// path-block blocks when the path parameter "id" matches "attack".
	rulePathBlock = `{"id":"path-block","name":"path-block","tags":{"type":"path-test","category":"attack_attempt"},
		"conditions":[{"operator":"match_regex","parameters":{"inputs":[{"address":"server.request.path_params","key_path":["id"]}],"regex":"attack"}}],
		"on_match":["block"]}`
	// path-any blocks when any path parameter matches "attack".
	rulePathAny = `{"id":"path-any","name":"path-any","tags":{"type":"path-any-test","category":"attack_attempt"},
		"conditions":[{"operator":"match_regex","parameters":{"inputs":[{"address":"server.request.path_params"}],"regex":"attack"}}],
		"on_match":["block"]}`
	// wildcard-value reports (no action) a non-empty "*" parameter. Another type than
	// path-block, so that path-block cannot hide it.
	ruleWildcardValue = `{"id":"wildcard-value","name":"wildcard-value","tags":{"type":"wildcard-test","category":"attack_attempt"},
		"conditions":[{"operator":"match_regex","parameters":{"inputs":[{"address":"server.request.path_params","key_path":["*"]}],"regex":"."}}],
		"on_match":[]}`
	// path-redirect: the condition of path-block, with a redirect.
	rulePathRedirect = `{"id":"path-redirect","name":"path-redirect","tags":{"type":"path-redirect-test","category":"attack_attempt"},
		"conditions":[{"operator":"match_regex","parameters":{"inputs":[{"address":"server.request.path_params","key_path":["id"]}],"regex":"attack"}}],
		"on_match":["redirect"]}`
	// header-block blocks on the response header that every handler sets.
	ruleHeaderBlock = `{"id":"header-block","name":"header-block","tags":{"type":"header-test","category":"attack_attempt"},
		"conditions":[{"operator":"match_regex","parameters":{"inputs":[{"address":"server.response.headers.no_cookies","key_path":["x-response-stage"]}],"regex":"^ready$"}}],
		"on_match":["block"]}`
	// mixed-block needs the path parameter and the response header.
	ruleMixedBlock = `{"id":"mixed-block","name":"mixed-block","tags":{"type":"mixed-test","category":"attack_attempt"},
		"conditions":[
			{"operator":"match_regex","parameters":{"inputs":[{"address":"server.request.path_params","key_path":["id"]}],"regex":"^attack$"}},
			{"operator":"match_regex","parameters":{"inputs":[{"address":"server.response.headers.no_cookies","key_path":["x-response-stage"]}],"regex":"^ready$"}}],
		"on_match":["block"]}`
	// mixed-block-header-first has the conditions of mixed-block in the other order.
	// The WAF stops at the first condition that does not match: with the response
	// header first, the path parameter is not evaluated (and not kept) before the
	// response.
	ruleMixedBlockHeaderFirst = `{"id":"mixed-block","name":"mixed-block","tags":{"type":"mixed-test","category":"attack_attempt"},
		"conditions":[
			{"operator":"match_regex","parameters":{"inputs":[{"address":"server.response.headers.no_cookies","key_path":["x-response-stage"]}],"regex":"^ready$"}},
			{"operator":"match_regex","parameters":{"inputs":[{"address":"server.request.path_params","key_path":["id"]}],"regex":"^attack$"}}],
		"on_match":["block"]}`
	// mixed-redirect: the conditions of mixed-block, with a redirect.
	ruleMixedRedirect = `{"id":"mixed-redirect","name":"mixed-redirect","tags":{"type":"mixed-redirect-test","category":"attack_attempt"},
		"conditions":[
			{"operator":"match_regex","parameters":{"inputs":[{"address":"server.request.path_params","key_path":["id"]}],"regex":"^attack$"}},
			{"operator":"match_regex","parameters":{"inputs":[{"address":"server.response.headers.no_cookies","key_path":["x-response-stage"]}],"regex":"^ready$"}}],
		"on_match":["redirect"]}`
	// same-path and same-header have the same type and both block.
	ruleSamePath = `{"id":"same-path","name":"same-path","tags":{"type":"same-test","category":"attack_attempt"},
		"conditions":[{"operator":"match_regex","parameters":{"inputs":[{"address":"server.request.path_params","key_path":["id"]}],"regex":"attack"}}],
		"on_match":["block"]}`
	ruleSameHeader = `{"id":"same-header","name":"same-header","tags":{"type":"same-test","category":"attack_attempt"},
		"conditions":[{"operator":"match_regex","parameters":{"inputs":[{"address":"server.response.headers.no_cookies","key_path":["x-response-stage"]}],"regex":"^ready$"}}],
		"on_match":["block"]}`
)

const blockActions = `[
	{"id":"block","type":"block_request","parameters":{"status_code":403,"type":"auto"}},
	{"id":"redirect","type":"redirect_request","parameters":{"status_code":303,"location":"/blocked"}}]`

// rulesJSON builds a ruleset from rule definitions.
func rulesJSON(rules ...string) string {
	return fmt.Sprintf(`{"version":"2.2","metadata":{"rules_version":"1.0.0"},"rules":[%s],"actions":%s}`,
		strings.Join(rules, ","), blockActions)
}

// startAppSecRules starts AppSec with only the given rules, and the telemetry
// recorder (that must start first, so that it gets the AppSec metrics).
func startAppSecRules(t *testing.T, rules ...string) *metricsRecorder {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rules.json")
	require.NoError(t, os.WriteFile(path, []byte(rulesJSON(rules...)), 0o600))
	t.Setenv("DD_APPSEC_RULES", path)
	t.Setenv("DD_APPSEC_ENABLED", "true")
	t.Setenv("DD_API_SECURITY_ENABLED", "true")
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "1s")
	rec := testutils.StartTelemetryRecorder(t)
	testutils.StartAppSec(t)
	return &metricsRecorder{rec: rec}
}

// metricsRecorder reads the telemetry metrics that AppSec recorded.
type metricsRecorder struct {
	rec interface{}
}

// snapshot returns the value of each metric that has one, by "name{tags}".
func (m *metricsRecorder) snapshot() map[string]float64 {
	out := map[string]float64{}
	metrics := reflect.ValueOf(m.rec).Elem().FieldByName("Metrics")
	for _, key := range metrics.MapKeys() {
		handle := metrics.MapIndex(key).Interface().(interface{ Get() float64 })
		value := handle.Get()
		if value == 0 {
			continue
		}
		out[key.FieldByName("Name").String()+"{"+key.FieldByName("Tags").String()+"}"] = value
	}
	return out
}

// since returns the metrics that changed since the snapshot before.
func (m *metricsRecorder) since(before map[string]float64) map[string]float64 {
	out := map[string]float64{}
	for k, v := range m.snapshot() {
		if d := v - before[k]; d != 0 {
			out[k] = d
		}
	}
	return out
}

// count sums the metrics of the given name whose tags contain all of the wanted tags.
func count(metrics map[string]float64, name string, tags ...string) float64 {
	var total float64
	for k, v := range metrics {
		if !strings.HasPrefix(k, name+"{") {
			continue
		}
		ok := true
		for _, tag := range tags {
			ok = ok && strings.Contains(k, tag)
		}
		if ok {
			total += v
		}
	}
	return total
}

// opCapture records the HandlerOperation of each request. It uses a start
// listener on a probe operation (which is registered by a middleware before the
// tracer), so it also sees operations whose handler never runs (an early block).
type opCapture struct {
	probe dyngo.Operation
	ops   []*httpsec.HandlerOperation
	args  []httpsec.HandlerOperationArgs
}

// newOpCapture must be called after AppSec started.
func newOpCapture() *opCapture {
	c := &opCapture{probe: dyngo.NewOperation(nil)}
	dyngo.On(c.probe, func(op *httpsec.HandlerOperation, args httpsec.HandlerOperationArgs) {
		c.ops = append(c.ops, op)
		c.args = append(c.args, args)
	})
	return c
}

// middleware is the first middleware of a router.
func (c *opCapture) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(dyngo.RegisterOperation(r.Context(), c.probe)))
	})
}

func (c *opCapture) reset() { c.ops, c.args = nil, nil }

func (c *opCapture) routes() []string {
	var routes []string
	for _, op := range c.ops {
		routes = append(routes, op.Route())
	}
	return routes
}

// eventRules returns the sorted ids of the rules in the _dd.appsec.json tag.
func eventRules(tag any) []string {
	s, _ := tag.(string)
	var ids []string
	for _, rule := range []string{"path-block", "path-any", "wildcard-value", "path-redirect", "header-block",
		"mixed-block", "mixed-redirect", "same-path", "same-header", "mon-wild", "mon-id", "blk-id"} {
		if strings.Contains(s, `"id":"`+rule+`"`) {
			ids = append(ids, rule)
		}
	}
	sort.Strings(ids)
	return ids
}

// pathRule returns a rule on the path parameter key of the given tags.type. The
// action is "" (detection only) or "block".
func pathRule(id, typ, key, action string) string {
	onMatch := "[]"
	if action != "" {
		onMatch = `["` + action + `"]`
	}
	return fmt.Sprintf(`{"id":%q,"name":%q,"tags":{"type":%q,"category":"attack_attempt"},
		"conditions":[{"operator":"match_regex","parameters":{"inputs":[{"address":"server.request.path_params","key_path":[%q]}],"regex":"attack"}}],
		"on_match":%s}`, id, id, typ, key, onMatch)
}
