package tracer

/*

Package tracer provides Datadog's Go tracing client.

The core idea o


If you need finer grained control, create and use your own tracer:

	t := tracer.NewTracer()
	span := t.NewSpan("http.request", "datadog-web", "/user/home")
	defer span.Finish()


*/
