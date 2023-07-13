package validationtest

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	memcachetest "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/contrib/bradfitz/gomemcache/memcache"
	redigotest "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/contrib/garyburd/redigo"
	mgotest "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/contrib/globalsign/mgo"
	pgtest "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/contrib/go-pg/pg.v10"
	redistest "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/contrib/go-redis/redis"
	redisV7test "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/contrib/go-redis/redis.v7"
	redisV8test "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/contrib/go-redis/redis.v8"
	mongotest "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/contrib/go.mongodb.org/mongo-driver/mongo"
	gocqltrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/contrib/gocql/gocql"
	gomodule_redigotest "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/contrib/gomodule/redigo"

	gormv1test "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/contrib/gopkg.in/jinzhu/gorm.v1"
	dnstest "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/contrib/miekg/dns"
	redisV9test "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/contrib/redis/go-redis.v9"
	leveldbtest "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/contrib/syndtr/goleveldb/leveldb"
	buntdbtest "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/contrib/tidwall/buntdb"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Integration is an interface that should be implemented by integrations (packages under the contrib/ folder) in
// order to be tested.
type Integration interface {
	// Name returns name of the integration (usually the import path starting from /contrib).
	Name() string

	// Init initializes the integration (start a server in the background, initialize the client, etc.).
	// It should return a cleanup function that will be executed after the test finishes.
	// It should also call t.Helper() before making any assertions.
	Init(t *testing.T) func()

	// GenSpans performs any operation(s) from the integration that generate spans.
	// It should call t.Helper() before making any assertions.
	GenSpans(t *testing.T)

	// NumSpans returns the number of spans that should have been generated during the test.
	NumSpans() int
}

// tracerEnv gets the current tracer configuration variables needed for Test Agent testing and places
// these env variables in a comma separated string of key=value pairs.
func tracerEnv() string {
	schemaVersionStr := os.Getenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA")
	peerServiceDefaultsEnabled := false
	// Use a default schema version of V1 and only update if the env variable is a valid schema
	schemaVersion := namingschema.SchemaV1
	if v, ok := namingschema.ParseVersion(schemaVersionStr); ok {
		schemaVersion = v
		if int(v) == int(namingschema.SchemaV0) {
			peerServiceDefaultsEnabled = internal.BoolEnv("DD_TRACE_PEER_SERVICE_DEFAULTS_ENABLED", false)
		}
	}
	serviceName := os.Getenv("DD_SERVICE")
	// override DD_SERVICE if a global service name is set in order to replicate real tracer service name resolution
	if v := globalconfig.ServiceName(); v != "" {
		serviceName = v
	}
	ddEnvVars := map[string]string{
		"DD_SERVICE":                             serviceName,
		"DD_TRACE_SPAN_ATTRIBUTE_SCHEMA":         fmt.Sprintf("v%d", schemaVersion),
		"DD_TRACE_PEER_SERVICE_DEFAULTS_ENABLED": strconv.FormatBool(peerServiceDefaultsEnabled),
	}
	values := make([]string, 0, len(ddEnvVars))
	for k, v := range ddEnvVars {
		values = append(values, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(values, ",")
}

type testAgentTransport struct {
	*http.Transport
}

// RoundTrip adds the DD Tracer configuration environment and test session token to the trace request headers
func (t *testAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Add("X-Datadog-Trace-Env-Variables", currentTracerEnv)
	req.Header.Add("X-Datadog-Test-Session-Token", sessionToken)
	return http.DefaultTransport.RoundTrip(req)
}

var defaultDialer = &net.Dialer{
	Timeout:   30 * time.Second,
	KeepAlive: 30 * time.Second,
	DualStack: true,
}
var testAgentClient = &http.Client{
	// We copy the transport to avoid using the default one, as it might be
	// augmented with tracing and we don't want these calls to be recorded.
	// See https://golang.org/pkg/net/http/#DefaultTransport .
	Transport: &testAgentTransport{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           defaultDialer.DialContext,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}, Timeout: 2 * time.Second,
}

func testAgentDetails() string {
	testAgentHost, exists := os.LookupEnv("DD_TEST_AGENT_HOST")
	if !exists {
		testAgentHost = "localhost"
	}

	testAgentPort, exists := os.LookupEnv("DD_TEST_AGENT_PORT")
	if !exists {
		testAgentPort = "9126"
	}
	return fmt.Sprintf("%s:%s", testAgentHost, testAgentPort)
}

var (
	testAgentConnection = testAgentDetails()
	sessionToken        = "default"
	currentTracerEnv    = tracerEnv()
	testCases           = GetValidationTestCases()
)

func TestIntegrations(t *testing.T) {
	if _, ok := os.LookupEnv("INTEGRATION"); !ok {
		t.Skip("to enable integration test, set the INTEGRATION environment variable")
	}
	integrations := []Integration{
		memcachetest.New(),
		dnstest.New(),
		redigotest.New(),
		pgtest.New(),
		redistest.New(),
		redisV7test.New(),
		redisV8test.New(),
		mongotest.New(),
		gocqltrace.New(),
		gomodule_redigotest.New(),
		redisV9test.New(),
		leveldbtest.New(),
		buntdbtest.New(),
		gormv1test.New(),
		mgotest.New(),
	}
	for _, ig := range integrations {
		name := ig.Name()
		sessionToken = fmt.Sprintf("%s-%d", name, time.Now().Unix())
		for _, testCase := range testCases {
			t.Run(name, func(t *testing.T) {
				t.Setenv("CI_TEST_AGENT_SESSION_TOKEN", sessionToken)
				t.Setenv("DD_SERVICE", "Datadog-Test-Agent-Trace-Checks")
				// loop through all our environment for the testCase and set each variable
				for k, v := range testCase.EnvVars {
					t.Setenv(k, v)
				}

				// also include the testCase start options within the tracer config
				tracer.Start(append(testCase.StartOptions, tracer.WithAgentAddr(testAgentConnection), tracer.WithHTTPClient(testAgentClient))...)

				// get the current Tracer Environment after it has been started with configuration
				currentTracerEnv = tracerEnv()

				defer tracer.Stop()

				cleanup := ig.Init(t)
				tracer.Flush()
				defer cleanup()

				ig.GenSpans(t)

				tracer.Flush()

				assertNumSpans(t, sessionToken, ig.NumSpans())
				checkFailures(t, sessionToken)
			})
		}
	}
}

// assertNumSpans makes an http request to the Test Agent for all traces produced with the included
// sessionToken and asserts that the correct number of spans was returned
func assertNumSpans(t *testing.T, sessionToken string, wantSpans int) {
	t.Helper()
	run := func() bool {
		req, err := http.NewRequest("GET", fmt.Sprintf("http://%s/test/session/traces", testAgentConnection), nil)
		require.NoError(t, err)
		req.Header.Set("X-Datadog-Test-Session-Token", sessionToken)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)

		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var traces [][]map[string]interface{}
		require.NoError(t, json.Unmarshal(body, &traces))

		receivedSpans := 0
		for _, traceSpans := range traces {
			receivedSpans += len(traceSpans)
		}
		if receivedSpans > wantSpans {
			t.Fatalf("received more spans than expected (wantSpans: %d, receivedSpans: %d)", wantSpans, receivedSpans)
		}
		return receivedSpans == wantSpans
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	timeoutChan := time.After(5 * time.Second)

	for {
		if done := run(); done {
			return
		}
		select {
		case <-ticker.C:
			continue

		case <-timeoutChan:
			t.Fatal("timeout waiting for spans")
		}
	}
}

// checkFailures makes an HTTP request to the Test Agent for any Trace Check failures and passes or fails the test
// depending on if failures exist.
func checkFailures(t *testing.T, sessionToken string) {
	t.Helper()
	req, err := http.NewRequest("GET", fmt.Sprintf("http://%s/test/trace_check/failures", testAgentConnection), nil)
	require.NoError(t, err)
	req.Header.Set("X-Datadog-Test-Session-Token", sessionToken)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	// the Test Agent returns a 200 if no trace-failures occurred and 400 otherwise
	if resp.StatusCode == http.StatusOK {
		return
	}
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Fail(t, "APM Test Agent detected failures: \n", string(body))
}
