package logrus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/DataDog/dd-trace-go/internal/orchestrion/_integration/internal/net"
	"github.com/DataDog/dd-trace-go/internal/orchestrion/_integration/internal/trace"
)

type TestCaseEchoMiddleware struct {
	srv    *echo.Echo
	logger *CustomLogger
	addr   string
}

func (tc *TestCaseEchoMiddleware) Setup(_ context.Context, t *testing.T) {
	tc.srv = echo.New()
	tc.logger = GetLogger("my-app")

	// This is not actually necessary, left here just for testing purposes
	tracer.Start()
	t.Cleanup(tracer.Stop)

	// Middleware
	tc.srv.Use(initMiddlewareLogger("my-app", tc.logger))
	tc.srv.Use(middleware.Recover())
	tc.srv.GET("/ping", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{"message": "pong"})
	})

	tc.addr = fmt.Sprintf("127.0.0.1:%d", net.FreePort(t))

	go func() { assert.ErrorIs(t, tc.srv.Start(tc.addr), http.ErrServerClosed) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NoError(t, tc.srv.Shutdown(ctx))
	})
}

func (tc *TestCaseEchoMiddleware) Run(_ context.Context, t *testing.T) {
	for i := 0; i < 2; i++ {
		resp, err := http.Get("http://" + tc.addr + "/ping")
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
	}
	raw := tc.logger.out.String()
	t.Logf("got logs: %s", raw)
	
	lines := strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
	require.Len(t, lines, 2)

	for i := 0; i < 2; i++ {
		var entry map[string]interface{}
		err := json.Unmarshal([]byte(lines[i]), &entry)
		require.NoError(t, err)

		assert.Equal(t, "handling GET request on /ping", entry["msg"], "wrong log message")
		assert.NotEmpty(t, entry["dd.span_id"], "no span ID")
		assert.NotEmpty(t, entry["dd.trace_id"], "no trace ID")
	}
}

func (tc *TestCaseEchoMiddleware) ExpectedTraces() trace.Traces {
	reqTrace := &trace.Trace{
		Tags: map[string]any{
			"name": "http.request",
		},
		Meta: map[string]string{
			"component": "net/http",
			"span.kind": "client",
		},
		Children: trace.Traces{
			{
				Tags: map[string]any{
					"name": "http.request",
				},
				Meta: map[string]string{
					"component": "net/http",
					"span.kind": "server",
				},
				Children: trace.Traces{
					{
						Tags: map[string]any{
							"name": "http.request",
						},
						Meta: map[string]string{
							"component": "labstack/echo.v4",
							"span.kind": "server",
						},
					},
				},
			},
		},
	}
	return trace.Traces{
		reqTrace,
		reqTrace,
	}
}

// -- Logger Setup -- //

var loggers = make(map[string]*CustomLogger, 0)

type CustomLogger struct {
	*logrus.Logger
	out *bytes.Buffer
}

// GetLogger gets a configured logger.
func GetLogger(name string) *CustomLogger {
	var ok bool
	var logger *CustomLogger

	if logger, ok = loggers[name]; !ok {
		logger = newCustomLogger()
		loggers[name] = logger
	}
	return logger
}

func initMiddlewareLogger(app string, logger *CustomLogger) echo.MiddlewareFunc {
	return middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogURI:    true,
		LogStatus: true,
		LogMethod: true,
		LogValuesFunc: func(c echo.Context, values middleware.RequestLoggerValues) error {
			contextLog := logger.WithContext(c.Request().Context())
			contextLog.WithFields(logrus.Fields{
				"app":    app,
				"uri":    values.URI,
				"status": values.Status,
				"method": values.Method,
			}).Infof("handling %s request on %s", values.Method, values.URI)

			return nil
		},
	})
}

func newCustomLogger() *CustomLogger {
	baseLogger := logrus.New()
	out := new(bytes.Buffer)
	logger := &CustomLogger{Logger: baseLogger, out: out}
	logger.Formatter = &logrus.JSONFormatter{TimestampFormat: time.RFC3339}
	logger.SetOutput(out)
	logger.SetLevel(logrus.InfoLevel)
	logger.SetReportCaller(true)
	return logger
}
