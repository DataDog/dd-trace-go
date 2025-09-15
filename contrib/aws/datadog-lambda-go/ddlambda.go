// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package ddlambda

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/aws/aws-lambda-go/lambda"

	"github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2/internal/extension"
	"github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2/internal/logger"
	"github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2/internal/metrics"
	"github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2/internal/trace"
	"github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2/internal/wrapper"
)

type (
	// Config gives options for how ddlambda should behave
	Config struct {
		// APIKey is your Datadog API key. This is used for sending metrics.
		APIKey string
		// KMSAPIKey is your Datadog API key, encrypted using the AWS KMS service. This is used for sending metrics.
		KMSAPIKey string
		// ShouldRetryOnFailure is used to turn on retry logic when sending metrics via the API. This can negatively effect the performance of your lambda,
		// and should only be turned on if you can't afford to lose metrics data under poor network conditions.
		ShouldRetryOnFailure bool
		// ShouldUseLogForwarder enabled the log forwarding method for sending metrics to Datadog. This approach requires the user to set up a custom lambda
		// function that forwards metrics from cloudwatch to the Datadog api. This approach doesn't have any impact on the performance of your lambda function.
		ShouldUseLogForwarder bool
		// BatchInterval is the period of time which metrics are grouped together for processing to be sent to the API or written to logs.
		// Any pending metrics are flushed at the end of the lambda.
		BatchInterval time.Duration
		// Site is the host to send metrics to. If empty, this value is read from the 'DD_SITE' environment variable, or if that is empty
		// will default to 'datadoghq.com'.
		Site string
		// DebugLogging will turn on extended debug logging.
		DebugLogging bool
		// EnhancedMetrics enables the reporting of enhanced metrics under `aws.lambda.enhanced*` and adds enhanced metric tags
		EnhancedMetrics bool
		// DDTraceEnabled enables the Datadog tracer.
		DDTraceEnabled bool
		// MergeXrayTraces will cause Datadog traces to be merged with traces from AWS X-Ray.
		MergeXrayTraces bool
		// HTTPClientTimeout specifies a time limit for requests to the API. It defaults to 5s.
		HTTPClientTimeout time.Duration
		// CircuitBreakerInterval is the cyclic period of the closed state
		// for the CircuitBreaker to clear the internal Counts.
		// default: 30s
		CircuitBreakerInterval time.Duration
		// CircuitBreakerTimeout is the period of the open state,
		// after which the state of the CircuitBreaker becomes half-open.
		// default: 60s
		CircuitBreakerTimeout time.Duration
		// CircuitBreakerTotalFailures after this amount of times
		// of a request failing in the closed state, the state will become open.
		// the counter will get totally reset after CircuitBreakerInterval
		// default: 4
		CircuitBreakerTotalFailures uint32
		// FIPSMode enables FIPS mode. Defaults to true in GovCloud regions and false elsewhere.
		FIPSMode *bool
		// TraceContextExtractor is the function that extracts a root/parent trace context from the Lambda event body.
		// See trace.DefaultTraceExtractor for an example.
		TraceContextExtractor trace.ContextExtractor
		// TracerOptions are additional options passed to the tracer.
		TracerOptions []tracer.StartOption
	}
)

const (
	// DatadogAPIKeyEnvVar is the environment variable that will be used to set the API key.
	DatadogAPIKeyEnvVar = "DD_API_KEY"
	// DatadogKMSAPIKeyEnvVar is the environment variable that will be sent to KMS for decryption, then used as an API key.
	DatadogKMSAPIKeyEnvVar = "DD_KMS_API_KEY"
	// DatadogSiteEnvVar is the environment variable that will be used as the API host.
	DatadogSiteEnvVar = "DD_SITE"
	// LogLevelEnvVar is the environment variable that will be used to set the log level.
	LogLevelEnvVar = "DD_LOG_LEVEL"
	// ShouldUseLogForwarderEnvVar is the environment variable that enables log forwarding of metrics.
	ShouldUseLogForwarderEnvVar = "DD_FLUSH_TO_LOG"
	// DatadogTraceEnabledEnvVar is the environment variable that enables Datadog tracing.
	DatadogTraceEnabledEnvVar = "DD_TRACE_ENABLED"
	// MergeXrayTracesEnvVar is the environment variable that enables the merging of X-Ray and Datadog traces.
	MergeXrayTracesEnvVar = "DD_MERGE_XRAY_TRACES"
	// UniversalInstrumentation is the environment variable that enables universal instrumentation with the DD Extension
	UniversalInstrumentation = "DD_UNIVERSAL_INSTRUMENTATION"
	// Initialize otel tracer provider if enabled
	OtelTracerEnabled = "DD_TRACE_OTEL_ENABLED"
	// FIPSModeEnvVar is the environment variable that determines whether to enable FIPS mode.
	// Defaults to true in GovCloud regions and false otherwise.
	FIPSModeEnvVar = "DD_LAMBDA_FIPS_MODE"

	// DefaultSite to send API messages to.
	DefaultSite = "datadoghq.com"
	// DefaultEnhancedMetrics enables enhanced metrics by default.
	DefaultEnhancedMetrics = true

	// serverlessAppSecEnabledEnvVar is the environment variable used to activate Serverless ASM through the use of an
	// AWS Lambda runtime API proxy.
	serverlessAppSecEnabledEnvVar = "DD_SERVERLESS_APPSEC_ENABLED"
	// awsLambdaRuntimeApiEnvVar is the environment variable used to redirect AWS Lambda runtime API calls to the proxy.
	awsLambdaRuntimeApiEnvVar = "AWS_LAMBDA_RUNTIME_API"
	// datadogAgentUrl is the URL of the agent and proxy started by the Datadog lambda extension.
	datadogAgentUrl = "127.0.0.1:9000"
	// ddExtensionFilePath is the path on disk of the datadog lambda extension.
	ddExtensionFilePath = "/opt/extensions/datadog-agent"

	// awsLambdaServerPortEnvVar is the environment variable set by the go1.x Lambda Runtime to indicate which port the
	// RCP server should listen on. This is used as a sign that a warning should be printed if customers want to enable
	// ASM support, but did not enable the lambda.norpc build taf.
	awsLambdaServerPortEnvVar = "_LAMBDA_SERVER_PORT"
)

// WrapLambdaHandlerInterface is used to instrument your lambda functions.
// It returns a modified handler that can be passed directly to the lambda.StartHandler function from aws-lambda-go.
func WrapLambdaHandlerInterface(handler lambda.Handler, cfg *Config) lambda.Handler {
	setupAppSec()
	listeners := initializeListeners(cfg)
	return wrapper.WrapHandlerInterfaceWithListeners(handler, listeners...)
}

// WrapFunction is used to instrument your lambda functions.
// It returns a modified handler that can be passed directly to the lambda.Start function from aws-lambda-go.
func WrapFunction(handler interface{}, cfg *Config) interface{} {
	setupAppSec()
	listeners := initializeListeners(cfg)
	return wrapper.WrapHandlerWithListeners(handler, listeners...)
}

// WrapHandler is used to instrument your lambda functions.
// It returns a modified handler that can be passed directly to the lambda.Start function from aws-lambda-go.
// Deprecated: use WrapFunction instead
func WrapHandler(handler interface{}, cfg *Config) interface{} {
	return WrapFunction(handler, cfg)
}

// GetTraceHeaders returns a map containing Datadog trace headers that reflect the
// current X-Ray subsegment.
// Deprecated: use native Datadog tracing instead.
func GetTraceHeaders(ctx context.Context) map[string]string {
	result := trace.ConvertCurrentXrayTraceContext(ctx)
	return result
}

// AddTraceHeaders adds Datadog trace headers to a HTTP Request reflecting the current X-Ray
// subsegment.
// Deprecated: use native Datadog tracing instead.
func AddTraceHeaders(ctx context.Context, req *http.Request) {
	headers := trace.ConvertCurrentXrayTraceContext(ctx)
	for key, value := range headers {
		req.Header.Add(key, value)
	}
}

// GetContext retrieves the last created lambda context.
// Only use this if you aren't manually passing context through your call hierarchy.
func GetContext() context.Context {
	return wrapper.CurrentContext
}

// Distribution sends a distribution metric to Datadog
// Deprecated: Use Metric method instead
func Distribution(metric string, value float64, tags ...string) {
	Metric(metric, value, tags...)
}

// Metric sends a distribution metric to DataDog
func Metric(metric string, value float64, tags ...string) {
	MetricWithTimestamp(metric, value, time.Now(), tags...)
}

// MetricWithTimestamp sends a distribution metric to DataDog with a custom timestamp
func MetricWithTimestamp(metric string, value float64, timestamp time.Time, tags ...string) {
	ctx := GetContext()

	if ctx == nil {
		logger.Debug("no context available, did you wrap your handler?")
		return
	}

	listener := metrics.GetListener(ctx)

	if listener == nil {
		logger.Error(fmt.Errorf("couldn't get metrics listener from current context"))
		return
	}
	listener.AddDistributionMetric(metric, value, timestamp, false, tags...)
}

// InvokeDryRun is a utility to easily run your lambda for testing
func InvokeDryRun(callback func(ctx context.Context), cfg *Config) (interface{}, error) {
	wrapped := WrapHandler(callback, cfg)
	// Convert the wrapped handler to it's underlying raw handler type
	handler, ok := wrapped.(func(ctx context.Context, msg json.RawMessage) (interface{}, error))
	if !ok {
		logger.Debug("Could not unwrap lambda during dry run")
	}
	return handler(context.Background(), json.RawMessage("{}"))
}

func (cfg *Config) toTraceConfig() trace.Config {
	traceConfig := trace.Config{
		DDTraceEnabled:           true,
		MergeXrayTraces:          false,
		UniversalInstrumentation: true,
		OtelTracerEnabled:        false,
	}

	if cfg != nil {
		traceConfig.DDTraceEnabled = cfg.DDTraceEnabled
		traceConfig.MergeXrayTraces = cfg.MergeXrayTraces
		traceConfig.TraceContextExtractor = cfg.TraceContextExtractor
		traceConfig.TracerOptions = cfg.TracerOptions
	}

	if traceConfig.TraceContextExtractor == nil {
		traceConfig.TraceContextExtractor = trace.DefaultTraceExtractor
	}

	if tracingEnabled, err := strconv.ParseBool(os.Getenv(DatadogTraceEnabledEnvVar)); err == nil {
		traceConfig.DDTraceEnabled = tracingEnabled
		// Only read the OTEL env var if DD tracing is enabled
		if tracingEnabled {
			if otelTracerEnabled, err := strconv.ParseBool(os.Getenv(OtelTracerEnabled)); err == nil {
				traceConfig.OtelTracerEnabled = otelTracerEnabled
			}
		}
	}

	if !traceConfig.MergeXrayTraces {
		traceConfig.MergeXrayTraces, _ = strconv.ParseBool(os.Getenv(MergeXrayTracesEnvVar))
	}

	if universalInstrumentation, err := strconv.ParseBool(os.Getenv(UniversalInstrumentation)); err == nil {
		traceConfig.UniversalInstrumentation = universalInstrumentation
	}

	return traceConfig
}

func initializeListeners(cfg *Config) []wrapper.HandlerListener {
	logLevel := os.Getenv(LogLevelEnvVar)
	if strings.EqualFold(logLevel, "debug") || (cfg != nil && cfg.DebugLogging) {
		logger.SetLogLevel(logger.LevelDebug)
	}
	traceConfig := cfg.toTraceConfig()
	extensionManager := extension.BuildExtensionManager(traceConfig.UniversalInstrumentation)
	isExtensionRunning := extensionManager.IsExtensionRunning()
	metricsConfig := cfg.toMetricsConfig(isExtensionRunning)

	// Wrap the handler with listeners that add instrumentation for traces and metrics.
	tl := trace.MakeListener(traceConfig, extensionManager)
	ml := metrics.MakeListener(metricsConfig, extensionManager)
	return []wrapper.HandlerListener{
		&tl, &ml,
	}
}

func (cfg *Config) toMetricsConfig(isExtensionRunning bool) metrics.Config {

	mc := metrics.Config{
		ShouldRetryOnFailure: false,
		FIPSMode:             cfg.calculateFipsMode(),
	}

	if cfg != nil {
		mc.BatchInterval = cfg.BatchInterval
		mc.ShouldRetryOnFailure = cfg.ShouldRetryOnFailure
		mc.APIKey = cfg.APIKey
		mc.KMSAPIKey = cfg.KMSAPIKey
		mc.Site = cfg.Site
		mc.ShouldUseLogForwarder = cfg.ShouldUseLogForwarder
		mc.HTTPClientTimeout = cfg.HTTPClientTimeout
	}

	if mc.Site == "" {
		mc.Site = os.Getenv(DatadogSiteEnvVar)
	}
	if mc.Site == "" {
		mc.Site = DefaultSite
	}
	if strings.HasPrefix(mc.Site, "https://") || strings.HasPrefix(mc.Site, "http://") {
		mc.Site = fmt.Sprintf("%s/api/v1", mc.Site)
	} else {
		mc.Site = fmt.Sprintf("https://api.%s/api/v1", mc.Site)
	}

	if !mc.ShouldUseLogForwarder {
		shouldUseLogForwarder := os.Getenv(ShouldUseLogForwarderEnvVar)
		mc.ShouldUseLogForwarder = strings.EqualFold(shouldUseLogForwarder, "true")
	}

	if mc.APIKey == "" {
		mc.APIKey = os.Getenv(DatadogAPIKeyEnvVar)

	}
	if mc.KMSAPIKey == "" {
		mc.KMSAPIKey = os.Getenv(DatadogKMSAPIKeyEnvVar)
	}
	if !isExtensionRunning && mc.APIKey == "" && mc.KMSAPIKey == "" && !mc.ShouldUseLogForwarder {
		logger.Error(fmt.Errorf(
			"couldn't read %s or %s from environment", DatadogAPIKeyEnvVar, DatadogKMSAPIKeyEnvVar,
		))
	}

	enhancedMetrics := os.Getenv("DD_ENHANCED_METRICS")
	if enhancedMetrics == "" {
		mc.EnhancedMetrics = DefaultEnhancedMetrics
	}
	if !mc.EnhancedMetrics {
		mc.EnhancedMetrics = strings.EqualFold(enhancedMetrics, "true")
	}

	if localTest := os.Getenv("DD_LOCAL_TEST"); localTest == "1" || strings.ToLower(localTest) == "true" {
		mc.LocalTest = true
	}

	return mc
}

func (cfg *Config) calculateFipsMode() bool {
	if cfg != nil && cfg.FIPSMode != nil {
		return *cfg.FIPSMode
	}

	region := os.Getenv("AWS_REGION")
	isGovCloud := strings.HasPrefix(region, "us-gov-")

	fipsMode := isGovCloud

	fipsModeEnv := os.Getenv(FIPSModeEnvVar)
	if fipsModeEnv != "" {
		if parsedFipsMode, err := strconv.ParseBool(fipsModeEnv); err == nil {
			fipsMode = parsedFipsMode
		} else {
			logger.Debug(fmt.Sprintf("could not parse %s: %s", fipsModeEnv, err))
		}
	}

	if fipsMode || isGovCloud {
		if fipsMode {
			logger.Debug("Go Lambda Layer FIPS mode enabled")
		} else {
			logger.Debug("Go Lambda Layer FIPS mode disabled")
		}
	}

	return fipsMode
}

// setupAppSec checks if DD_SERVERLESS_APPSEC_ENABLED is set (to true) and when that
// is the case, redirects `AWS_LAMBDA_RUNTIME_API` to the agent extension, and turns
// on universal instrumentation unless it was already configured by the customer, so
// that the HTTP context (invocation details span tags) is available on AppSec traces.
func setupAppSec() {
	enabled := false
	if env := os.Getenv(serverlessAppSecEnabledEnvVar); env != "" {
		if on, err := strconv.ParseBool(env); err == nil {
			enabled = on
		}
	}

	if !enabled {
		return
	}

	if _, err := os.Stat(ddExtensionFilePath); os.IsNotExist(err) {
		logger.Debug(fmt.Sprintf("%s is enabled, but the Datadog extension was not found at %s", serverlessAppSecEnabledEnvVar, ddExtensionFilePath))
		return
	}

	if awsLambdaRpcSupport {
		if port := os.Getenv(awsLambdaServerPortEnvVar); port != "" {
			logger.Warn(fmt.Sprintf("%s activation with the go1.x AWS Lambda runtime requires setting the `lambda.norpc` go build tag", serverlessAppSecEnabledEnvVar))
		}
	}

	if err := os.Setenv(awsLambdaRuntimeApiEnvVar, datadogAgentUrl); err != nil {
		logger.Debug(fmt.Sprintf("failed to set %s=%s: %v", awsLambdaRuntimeApiEnvVar, datadogAgentUrl, err))
	} else {
		logger.Debug(fmt.Sprintf("successfully set %s=%s", awsLambdaRuntimeApiEnvVar, datadogAgentUrl))
	}

	if val := os.Getenv(UniversalInstrumentation); val == "" {
		if err := os.Setenv(UniversalInstrumentation, "1"); err != nil {
			logger.Debug(fmt.Sprintf("failed to set %s=%d: %v", UniversalInstrumentation, 1, err))
		} else {
			logger.Debug(fmt.Sprintf("successfully set %s=%d", UniversalInstrumentation, 1))
		}
	}
}
