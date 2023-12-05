package apps

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/profiler"
)

type App struct {
	Name string
}

func (a *App) RunHTTP(handlerFn func() http.Handler) {
	// Parse flags
	var (
		httpF    = flag.String("http", "localhost:8080", "HTTP addr to listen on.")
		serviceF = flag.String("service", "dd-trace-go/"+a.Name, "Datadog service name")
		versionF = flag.String("version", "v1", "Datadog service version")
		periodF  = flag.Duration("period", 60*time.Second, "Profiling period.")
	)
	flag.Parse()

	// Setup context that gets canceled on receiving SIGINT
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Start tracer
	tracer.Start(
		tracer.WithService(*serviceF),
		tracer.WithServiceVersion(*versionF),
		tracer.WithRuntimeMetrics(),
	)
	defer tracer.Stop()

	// Start profiler
	if err := profiler.Start(
		profiler.WithService(*serviceF),
		profiler.WithVersion(*versionF),
		profiler.WithPeriod(*periodF),
		profiler.WithProfileTypes(
			profiler.CPUProfile,
			profiler.HeapProfile,
			profiler.BlockProfile,
			profiler.MutexProfile,
			profiler.GoroutineProfile,
		),
	); err != nil {
		log.Fatalf("failed to start profiler: %s", err)
	}
	defer profiler.Stop()

	// Start http server
	l, err := net.Listen("tcp", *httpF)
	if err != nil {
		log.Fatalf("failed to listen: %s", err)
	}
	defer l.Close()
	log.Printf("Listening on: http://%s", *httpF)
	// Note: handlerFn is a function that returns a http.Handler because if we
	// create httptrace.NewServeMux() before the tracer is started, we end up
	// with the wrong service name (http.router) :/.
	server := http.Server{Handler: handlerFn()}
	go server.Serve(l)

	// Wait until SIGINT is received, then shut down
	<-ctx.Done()
	log.Printf("Received interrupt, shutting down")
}
