// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DataDog/datadog-agent/pkg/obfuscate"
	pb "github.com/DataDog/datadog-agent/pkg/proto/pbgo/trace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tinylib/msgp/msgp"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// statsObfuscationQueries are chosen so that every SQL config exercised below
// obfuscates at least one of them differently from the default config.
var statsObfuscationQueries = []string{
	"SELECT * FROM users123 AS u WHERE u.id = 1 AND name = 'bob' AND active = TRUE AND x IS NULL /* c */ ;",
	"SELECT count(*), coalesce(a, 'x') FROM t WHERE data->>'name' = 'x' LIMIT 10",
	`SELECT $func$INSERT INTO t VALUES ('a')$func$ FROM "Users" WHERE id IN ( 1 , 2 ) AND p = $1 AND q = @P1`,
}

// agentSQLSettings is the SQL obfuscation configuration of a trace-agent.
type agentSQLSettings struct {
	featureFlags []string
	mode         obfuscate.ObfuscationMode // DD_APM_SQL_OBFUSCATION_MODE
}

// effectiveSQLConfig is the config the agent obfuscates stats with, as in
// (*AgentConfig).EffectiveSQLConfig in datadog-agent/pkg/trace/config.
func (s agentSQLSettings) effectiveSQLConfig() obfuscate.SQLConfig {
	has := func(f string) bool { return slices.Contains(s.featureFlags, f) }
	mode := s.mode
	if mode == "" && has("sqllexer") {
		mode = obfuscate.ObfuscateOnly
	}
	return obfuscate.SQLConfig{
		TableNames:       has("table_names"),
		ReplaceDigits:    has("quantize_sql_tables") || has("replace_sql_digits"),
		KeepSQLAlias:     has("keep_sql_alias"),
		DollarQuotedFunc: has("dollar_quoted_func"),
		ObfuscationMode:  mode,
	}
}

// infoJSON renders the /info response the agent serves for s, as in
// makeInfoHandler in datadog-agent/pkg/trace/api. withNestedSQL reports
// whether config.obfuscation.sql is included; agents that predate it only
// report sql_obfuscation_mode.
func (s agentSQLSettings) infoJSON(t *testing.T, withNestedSQL bool) string {
	cfg := s.effectiveSQLConfig()
	version := 1
	noTables := cfg
	noTables.TableNames = false
	if noTables != (obfuscate.SQLConfig{}) {
		version = 2
	}
	obf := map[string]any{"sql_obfuscation_mode": cfg.ObfuscationMode}
	if withNestedSQL {
		obf["sql"] = map[string]any{
			"replace_digits":     cfg.ReplaceDigits,
			"keep_sql_alias":     cfg.KeepSQLAlias,
			"dollar_quoted_func": cfg.DollarQuotedFunc,
			"obfuscation_mode":   cfg.ObfuscationMode,
		}
	}
	b, err := json.Marshal(map[string]any{
		"endpoints":           []string{"/v0.4/traces", "/v0.6/stats"},
		"client_drop_p0s":     true,
		"feature_flags":       s.featureFlags,
		"obfuscation_version": version,
		"config":              map[string]any{"obfuscation": obf},
	})
	require.NoError(t, err)
	return string(b)
}

// fetchTestAgentFeatures parses body through the tracer's real /info client.
func fetchTestAgentFeatures(t *testing.T, body string) agentFeatures {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	af, err := fetchAgentFeatures(t.Context(), u, srv.Client())
	require.NoError(t, err)
	return af
}

// agentObfuscatedResource obfuscates resource the way the agent's
// obfuscateStatsGroup does. Stats groups never carry a DB type.
func agentObfuscatedResource(t *testing.T, cfg obfuscate.SQLConfig, resource string) string {
	oq, err := obfuscate.NewObfuscator(obfuscate.Config{SQL: cfg}).ObfuscateSQLStringForDBMS(resource, "")
	require.NoError(t, err)
	return oq.Query
}

func TestStatsSQLObfuscationParity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		settings agentSQLSettings
	}{
		{name: "default"},
		{name: "table_names", settings: agentSQLSettings{featureFlags: []string{"table_names"}}},
		{name: "quantize_sql_tables", settings: agentSQLSettings{featureFlags: []string{"quantize_sql_tables"}}},
		{name: "replace_sql_digits", settings: agentSQLSettings{featureFlags: []string{"replace_sql_digits"}}},
		{name: "keep_sql_alias", settings: agentSQLSettings{featureFlags: []string{"keep_sql_alias"}}},
		{name: "dollar_quoted_func", settings: agentSQLSettings{featureFlags: []string{"dollar_quoted_func"}}},
		{name: "sqllexer", settings: agentSQLSettings{featureFlags: []string{"sqllexer"}}},
		{name: "obfuscate_only", settings: agentSQLSettings{mode: obfuscate.ObfuscateOnly}},
		{name: "normalize_only", settings: agentSQLSettings{mode: obfuscate.NormalizeOnly}},
		{name: "obfuscate_and_normalize", settings: agentSQLSettings{mode: obfuscate.ObfuscateAndNormalize}},
		{name: "mode and flags", settings: agentSQLSettings{
			featureFlags: []string{"replace_sql_digits", "keep_sql_alias", "dollar_quoted_func"},
			mode:         obfuscate.ObfuscateAndNormalize,
		}},
	} {
		agentCfg := tc.settings.effectiveSQLConfig()
		for _, nested := range []bool{true, false} {
			name := tc.name + "/nested_sql"
			if !nested {
				name = tc.name + "/mode_only"
			}
			t.Run(name, func(t *testing.T) {
				af := fetchTestAgentFeatures(t, tc.settings.infoJSON(t, nested))
				require.LessOrEqual(t, af.obfuscationVersion, tracerObfuscationVersion)
				require.False(t, af.statsSQLObfuscationUnsupported)

				o := (&statsObfuscator{}).forConfig(af.statsSQLObfuscation)
				differsFromDefault := false
				for _, q := range statsObfuscationQueries {
					want := agentObfuscatedResource(t, agentCfg, q)
					assert.Equal(t, want, obfuscatedResource(o, "sql", q), "query %q", q)
					if want != agentObfuscatedResource(t, obfuscate.SQLConfig{}, q) {
						differsFromDefault = true
					}
				}
				noTables := agentCfg
				noTables.TableNames = false
				if noTables != (obfuscate.SQLConfig{}) {
					assert.True(t, differsFromDefault, "no query distinguishes this config from the default one")
				}
			})
		}
	}
}

func TestStatsSQLObfuscationNestedOptions(t *testing.T) {
	af := fetchTestAgentFeatures(t, `{
		"obfuscation_version": 2,
		"config": {"obfuscation": {
			"sql_obfuscation_mode": "obfuscate_and_normalize",
			"sql": {
				"obfuscation_mode": "obfuscate_and_normalize",
				"keep_boolean": true,
				"keep_null": true,
				"keep_trailing_semicolon": true
			}
		}}
	}`)
	q := statsObfuscationQueries[0]
	got := obfuscatedResource((&statsObfuscator{}).forConfig(af.statsSQLObfuscation), "sql", q)
	want := agentObfuscatedResource(t, obfuscate.SQLConfig{
		ObfuscationMode:       obfuscate.ObfuscateAndNormalize,
		KeepBoolean:           true,
		KeepNull:              true,
		KeepTrailingSemicolon: true,
	}, q)
	assert.Equal(t, want, got)
	assert.NotEqual(t, agentObfuscatedResource(t, obfuscate.SQLConfig{ObfuscationMode: obfuscate.ObfuscateAndNormalize}, q), got)
}

func TestStatsSQLObfuscationUnsupportedMode(t *testing.T) {
	cfg := newTestConfigWithTransportAndEnv(t, newDummyTransport(), "someEnv")
	cfg.agent.store(fetchTestAgentFeatures(t, `{"obfuscation_version":2,"config":{"obfuscation":{"sql_obfuscation_mode":"some_future_mode"}}}`))
	s := &ddStatsSender{cfg: cfg}
	af := cfg.agent.load()
	assert.Zero(t, s.obfuscationVersion(&af))
}

func TestStatsSQLObfuscationVersion1IgnoresMode(t *testing.T) {
	// Agents that predate obfuscation_version 2 report version 1 even with a
	// custom mode; the tracer keeps obfuscating with the flag-derived config.
	af := fetchTestAgentFeatures(t, `{
		"obfuscation_version": 1,
		"feature_flags": ["keep_sql_alias"],
		"config": {"obfuscation": {"sql_obfuscation_mode": "obfuscate_only", "sql": {"obfuscation_mode": "obfuscate_only"}}}
	}`)
	assert.Equal(t, obfuscate.SQLConfig{KeepSQLAlias: true}, af.statsSQLObfuscation)
	assert.False(t, af.statsSQLObfuscationUnsupported)
}

func TestObfuscatedResourceErrorKeepsSQLOutOfLogs(t *testing.T) {
	rl := new(log.RecordLogger)
	defer log.UseLogger(rl)()
	const query = "SELECT * FROM users WHERE password = 'hunter2"
	for _, mode := range []obfuscate.ObfuscationMode{"", obfuscate.ObfuscateAndNormalize} {
		o := (&statsObfuscator{}).forConfig(obfuscate.SQLConfig{ObfuscationMode: mode})
		_, err := o.ObfuscateSQLString(query)
		if err == nil {
			continue
		}
		assert.Equal(t, textNonParsable, obfuscatedResource(o, "sql", query))
	}
	log.Flush()
	logs := strings.Join(rl.Logs(), "\n")
	require.Contains(t, logs, "Error obfuscating stats group resource")
	assert.NotContains(t, logs, "hunter2")
	assert.NotContains(t, logs, "password")
}

func TestStatsObfuscatorRebuildsOnConfigChange(t *testing.T) {
	var s statsObfuscator
	def := s.forConfig(obfuscate.SQLConfig{})
	assert.Same(t, def, s.forConfig(obfuscate.SQLConfig{}))
	alias := s.forConfig(obfuscate.SQLConfig{KeepSQLAlias: true})
	assert.NotSame(t, def, alias)
	assert.Equal(t, "SELECT * FROM t AS x", obfuscatedResource(alias, "sql", "SELECT * FROM t AS x"))
	assert.Nil(t, (*statsObfuscator)(nil).forConfig(obfuscate.SQLConfig{}))
}

// statsPost is one client stats payload received by statsAgent.
type statsPost struct {
	obfuscationVersion string
	resources          []string
}

// statsAgent is a minimal trace-agent that serves /info and records the
// client stats it receives.
type statsAgent struct {
	mu        sync.Mutex
	info      string
	infoCalls int
	posts     []statsPost
}

func (a *statsAgent) setInfo(body string) {
	a.mu.Lock()
	a.info = body
	a.mu.Unlock()
}

func (a *statsAgent) snapshot() (infoCalls int, posts []statsPost) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.infoCalls, slices.Clone(a.posts)
}

func (a *statsAgent) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/info", func(w http.ResponseWriter, _ *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.infoCalls++
		w.Write([]byte(a.info))
	})
	mux.HandleFunc("/v0.4/traces", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"rate_by_service":{}}`))
	})
	mux.HandleFunc("/v0.6/stats", func(_ http.ResponseWriter, r *http.Request) {
		var p pb.ClientStatsPayload
		if err := msgp.Decode(r.Body, &p); err != nil {
			t.Errorf("decoding stats payload: %v", err)
			return
		}
		post := statsPost{obfuscationVersion: r.Header.Get(obfuscationVersionHeader)}
		for _, b := range p.Stats {
			for _, g := range b.Stats {
				post.resources = append(post.resources, g.Resource)
			}
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		a.posts = append(a.posts, post)
	})
	return mux
}

// startStatsTracer starts a tracer computing client stats against agent.
func startStatsTracer(t *testing.T, agent *statsAgent, opts ...StartOption) *tracer {
	srv := httptest.NewServer(agent.handler(t))
	t.Cleanup(srv.Close)
	tr, err := newTracer(append([]StartOption{
		WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")),
		WithHTTPClient(internal.DefaultHTTPClient(defaultHTTPTimeout, true)),
		WithStatsComputation(true),
	}, opts...)...)
	require.NoError(t, err)
	setGlobalTracer(tr)
	t.Cleanup(func() { setGlobalTracer(&NoopTracer{}) })
	return tr
}

func finishSQLSpan(tr *tracer, query string) {
	tr.StartSpan("db.query", SpanType(ext.SpanTypeSQL), ResourceName(query)).Finish()
}

// TestStatsObfuscationEndToEnd runs a tracer against a mocked agent and checks
// the client stats it sends: resources obfuscated like the agent would, and
// the obfuscation version header the agent relies on to skip obfuscation.
func TestStatsObfuscationEndToEnd(t *testing.T) {
	query := statsObfuscationQueries[1]
	for _, tc := range []struct {
		name        string
		settings    agentSQLSettings
		wantVersion string
	}{
		{name: "version 1", wantVersion: "1"},
		{name: "version 2", settings: agentSQLSettings{mode: obfuscate.ObfuscateAndNormalize}, wantVersion: "2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agent := &statsAgent{info: tc.settings.infoJSON(t, true)}
			tr := startStatsTracer(t, agent)
			finishSQLSpan(tr, query)
			tr.Stop()

			_, posts := agent.snapshot()
			assert.Equal(t, []statsPost{{
				obfuscationVersion: tc.wantVersion,
				resources:          []string{agentObfuscatedResource(t, tc.settings.effectiveSQLConfig(), query)},
			}}, posts)
		})
	}
}

// TestStatsObfuscationFollowsAgentRestart checks that a running tracer picks
// up a new agent SQL config from /info, instead of keeping the config it saw
// at startup, and never mixes resources obfuscated under both configs in one
// payload.
func TestStatsObfuscationFollowsAgentRestart(t *testing.T) {
	query := statsObfuscationQueries[1]
	startup := agentSQLSettings{mode: obfuscate.ObfuscateOnly}
	agent := &statsAgent{info: startup.infoJSON(t, true)}
	tr := startStatsTracer(t, agent)

	// Obfuscate one span under the startup config, so a tracer that froze its
	// obfuscator at that point would keep using it.
	finishSQLSpan(tr, query)
	restarted := agentSQLSettings{mode: obfuscate.NormalizeOnly}
	agent.setInfo(restarted.infoJSON(t, true))
	tr.refreshAgentFeatures()
	finishSQLSpan(tr, query)
	tr.Stop()

	_, posts := agent.snapshot()
	assert.Equal(t, []statsPost{
		{obfuscationVersion: "2", resources: []string{agentObfuscatedResource(t, startup.effectiveSQLConfig(), query)}},
		{obfuscationVersion: "2", resources: []string{agentObfuscatedResource(t, restarted.effectiveSQLConfig(), query)}},
	}, posts)
}

// TestStatsObfuscationRawGroupAcrossRefresh checks that a stats group the
// tracer left raw, for the agent to obfuscate, is not sent under an
// obfuscation version header after /info starts allowing client-side
// obfuscation: the agent would then skip obfuscating it.
func TestStatsObfuscationRawGroupAcrossRefresh(t *testing.T) {
	query := statsObfuscationQueries[1]
	// A version newer than the tracer supports: the agent obfuscates.
	agent := &statsAgent{info: `{"endpoints":["/v0.4/traces","/v0.6/stats"],"client_drop_p0s":true,"obfuscation_version":3}`}
	tr := startStatsTracer(t, agent)

	finishSQLSpan(tr, query)
	v2 := agentSQLSettings{mode: obfuscate.ObfuscateAndNormalize}
	agent.setInfo(v2.infoJSON(t, true))
	tr.refreshAgentFeatures()
	finishSQLSpan(tr, query)
	tr.Stop()

	_, posts := agent.snapshot()
	assert.Equal(t, []statsPost{
		{obfuscationVersion: "", resources: []string{query}},
		{obfuscationVersion: "2", resources: []string{agentObfuscatedResource(t, v2.effectiveSQLConfig(), query)}},
	}, posts)
}

// TestStatsObfuscationRawGroupFlushedAfterRefresh checks that a raw stats
// group still buffered when /info starts allowing client-side obfuscation is
// flushed without an obfuscation version header, even with no newer span to
// separate it from.
func TestStatsObfuscationRawGroupFlushedAfterRefresh(t *testing.T) {
	query := statsObfuscationQueries[1]
	agent := &statsAgent{info: `{"endpoints":["/v0.4/traces","/v0.6/stats"],"client_drop_p0s":true,"obfuscation_version":3}`}
	tr := startStatsTracer(t, agent)

	finishSQLSpan(tr, query)
	agent.setInfo(agentSQLSettings{mode: obfuscate.ObfuscateAndNormalize}.infoJSON(t, true))
	tr.refreshAgentFeatures()
	tr.Stop()

	_, posts := agent.snapshot()
	assert.Equal(t, []statsPost{{obfuscationVersion: "", resources: []string{query}}}, posts)
}

// TestStatsObfuscationVersion1FlagsFrozen checks that, as before obfuscation
// version 2 support, a version 1 agent's SQL feature flags apply as they were
// at startup: a poll reporting other flags doesn't change stats obfuscation.
func TestStatsObfuscationVersion1FlagsFrozen(t *testing.T) {
	query := "SELECT * FROM users123 AS u WHERE u.id = 1"
	info := func(flags ...string) string {
		b, err := json.Marshal(map[string]any{
			"endpoints":           []string{"/v0.4/traces", "/v0.6/stats"},
			"client_drop_p0s":     true,
			"feature_flags":       flags,
			"obfuscation_version": 1,
		})
		require.NoError(t, err)
		return string(b)
	}
	agent := &statsAgent{info: info("keep_sql_alias")}
	tr := startStatsTracer(t, agent)

	agent.setInfo(info("replace_sql_digits"))
	tr.refreshAgentFeatures()
	assert.Equal(t, obfuscate.SQLConfig{KeepSQLAlias: true}, tr.config.agent.load().statsSQLObfuscation)
	finishSQLSpan(tr, query)
	tr.Stop()

	_, posts := agent.snapshot()
	assert.Equal(t, []statsPost{{
		obfuscationVersion: "1",
		resources:          []string{agentObfuscatedResource(t, obfuscate.SQLConfig{KeepSQLAlias: true}, query)},
	}}, posts)
}

// TestStatsObfuscationTimedPoll drives an agent SQL config transition through
// the tracer's real /info poll ticks, and checks the stats payloads sent
// before and after it.
func TestStatsObfuscationTimedPoll(t *testing.T) {
	const pollInterval = 20 * time.Millisecond
	query := statsObfuscationQueries[1]
	agent := &statsAgent{info: agentSQLSettings{}.infoJSON(t, true)}
	tr := startStatsTracer(t, agent, withAgentInfoPollInterval(pollInterval))
	require.Equal(t, 1, tr.config.agent.load().obfuscationVersion)

	finishSQLSpan(tr, query)
	infoCallsBefore, _ := agent.snapshot()
	v2 := agentSQLSettings{mode: obfuscate.NormalizeOnly}
	agent.setInfo(v2.infoJSON(t, true))
	start := time.Now()
	require.Eventually(t, func() bool {
		return tr.config.agent.load().obfuscationVersion == 2
	}, 50*pollInterval, pollInterval/4, "the poll never picked up the new /info")
	infoCallsAfter, _ := agent.snapshot()
	t.Logf("poll interval %s: /info picked up after %s, %d poll(s) since the change", pollInterval, time.Since(start).Round(time.Millisecond), infoCallsAfter-infoCallsBefore)
	finishSQLSpan(tr, query)
	tr.Stop()

	_, posts := agent.snapshot()
	assert.Equal(t, []statsPost{
		{obfuscationVersion: "1", resources: []string{agentObfuscatedResource(t, obfuscate.SQLConfig{}, query)}},
		{obfuscationVersion: "2", resources: []string{agentObfuscatedResource(t, v2.effectiveSQLConfig(), query)}},
	}, posts)
}
