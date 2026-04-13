# Project overview

## Read CONTRIBUTING.md First

**BEFORE making ANY code changes**, you MUST read [CONTRIBUTING.md](./CONTRIBUTING.md) for information about:

* Code cleanliness and style
* Testing mechanisms
* Important Go conventions

Furthermore, be sure to follow [Effective Go guidelines](https://go.dev/doc/effective_go) when writing Go code.

## Project Structure & Module Organization

dd-trace-go is a multi-module Go repository with several main subdirectories:

dd-trace-go                                                                                                                                        
├── ddtrace/                  Core tracing interfaces, types, and span/context contracts
│   ├── tracer/               Native Datadog tracer implementation (start spans, set tags, propagate)                                              
│   ├── mocktracer/           In-memory tracer for use in unit tests                                                                               
│   ├── opentelemetry/        OTel bridge — run OTel-instrumented code against the DD tracer                                                       
│   ├── ext/                  Tag name/value constants for Datadog APM (span types, errors, etc.)                                                  
│   └── baggage/              W3C baggage propagation API                                                                                          
│                                                                                                                                                  
├── instrumentation/          Shared helpers used across contrib integrations (HTTP, GraphQL, etc.)                                                
│                                                                                                                                                  
├── contrib/                  Drop-in integrations for popular Go libraries (~50+ submodules)
│   ├── net/http/             Standard library HTTP client & server tracing                                                                        
│   ├── database/sql/         database/sql wrapper with automatic query tracing                                                                    
│   ├── aws/aws-sdk-go-v2/    AWS SDK v2 middleware for tracing AWS calls                                                                          
│   ├── gin-gonic/gin/        Gin web framework middleware                                                                                         
│   ├── go-redis/redis/       Redis client tracing                                                                                                 
│   ├── google.golang.org/    gRPC client & server interceptors                                                                                    
│   ├── gorm.io/gorm/         GORM ORM query tracing                                                                                               
│   ├── elastic/go-elastic/   Elasticsearch client tracing                                                                                         
│   ├── confluentinc/         Kafka producer/consumer tracing                                                                                      
│   ├── modelcontextprotocol/ MCP server tracing                                                                                                   
│   └── … (40+ more)                                                                                   
│                                                                                                                                                  
├── appsec/                   Application Security Management (WAF, RASP) public API                                                               
├── internal/appsec/          WAF engine, rule evaluation, threat detection internals                                                              
│                                                                                                                                                  
├── profiler/                 Continuous profiling — CPU, heap, goroutine, mutex profiles                                                          
│                                                                                                                                                  
├── llmobs/                   LLM Observability — trace LLM calls, log datasets, eval metrics                                                    
│                                                                                                                                                  
├── datastreams/              Data Streams Monitoring — end-to-end latency for Kafka/queue pipelines                                             
│                                                                                                                                                  
├── civisibility/             CI Visibility — test result reporting and flaky-test detection                                                     
│                                                                                                                                                  
├── openfeature/              OpenFeature provider backed by Datadog Remote Config feature flags                                                 
│                                                                                                                                                  
├── orchestrion/              Auto-instrumentation tool (compile-time code injection via AST rewrite)                                            
│                                                                                                                                                  
├── internal/                 Private shared packages (not part of the public API)                                                               
│   ├── remoteconfig/         Remote Configuration client (polls agent for rule/config updates)                                                    
│   ├── telemetry/            SDK telemetry reporting back to Datadog                                                                              
│   ├── log/                  Internal structured logger                                                                                           
│   ├── … (many more)                                                                        
│                                                                                                                                                  
└── _tools/scripts/        Dev tooling, code generation, lint scripts, and CI helpers
│                                                                                                                                                  
└── .github/
│   ├── actions/                Go scripts that are shared between workflows
│   ├── workflows/              GitHub Actions workflows that run in CI
│                                                                                                                                                  
└── .gitlab                     GitLab workflows for running benchmarks in CI

Use the following AGENTS.md files when making specific changes:

* [contrib/AGENTS.md](./contrib/AGENTS.md) -- for updating contribs/integrations
* [ddtrace/AGENTS.md](./ddtrace/AGENTS.md) -- for updating core Datadog tracer implementations and features
* [internal/AGENTS.md](/internal/AGENTS.md) -- for updating features and implementations that are not customer facing

## General tips

### Upgrading a dependency

Since dd-trace-go is a multi-module project and dependencies should be synced across all submodules, ensure that any time a dependency is upgraded, it is set to the same version across all submodules.

Afterwards, run [./scripts/fix_modules.sh](./scripts/fix_modules.sh) and [./scripts/generate.sh](./scripts/generate.sh) to ensure Orchestrion files are also up to date. 

### Creating new files

For new files, ensure that it has the correct copyright header starting from the first line.

### Handling concurrency

To prevent deadlocks or data races, be cautious with mutexes and synchronous code. Suggest and use, with approval, the command [checklocks](./.claude/commands/checklocks.md) to analyze and propose improvements.

### Updating AGENTS.md

This file should be short. Only update this file if:

* A new subdirectory is added to the repository, so the repository graph must be updated.
* A new AGENTS.md file is added, so it must be added to the list with its purpose