# Core dd-trace-go Implementations

## Organization

ddtrace/                  Core tracing interfaces, types, and span/context contracts
├── tracer/               Native Datadog tracer implementation (start spans, set tags, propagate)                                              
├── mocktracer/           In-memory tracer for use in unit tests                                                                               
├── opentelemetry/        OTel bridge — run OTel-instrumented code against the DD tracer                                                       
├── ext/                  Tag name/value constants for Datadog APM (span types, errors, etc.)                                                  
└── baggage/              W3C baggage propagation API  

