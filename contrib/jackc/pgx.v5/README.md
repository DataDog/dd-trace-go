# pgx.v5 Integration

This integration provides Datadog tracing for [jackc/pgx](https://github.com/jackc/pgx)
v5 connections and pools. When AppSec and RASP are enabled, it also monitors SQL
for SQL injection.

## AppSec SQL injection monitoring

This integration emits monitoring-only SQL operations
for `Query`, `QueryRow`, `Exec`, and each queued query in `SendBatch`. A queued
query is one evaluation: in simple-protocol mode it can contain several SQL
statements, and it can also be a prepared statement name. Pools and
transactions use the same hooks. Monitoring uses the incoming request's security
context and remains enabled when query/batch APM spans are disabled, provided
AppSec and RASP are enabled.

These operations retain WAF events, stack traces, and evaluation metrics, but
suppress blocking and redirect actions: pgx tracing hooks cannot abort execution.
A suppressed blocking action counts as `block:failure` in `rasp.rule.match`, not
as a blocked request. A suppressed redirect action counts as `block:irrelevant`,
the same as on the normal path. A match without either action also counts as
`block:irrelevant`.

Transaction control statements also pass through `Exec` and are evaluated.
`Begin` plus `Commit` or `Rollback` adds two evaluations when given the request
context, even with no user SQL statements. The hooks cannot distinguish generated
transaction commands from SQL supplied by the caller; no SQL text is excluded.

Monitoring does not change protection in other integrations. The instrumented
`database/sql` execution path marks the context passed to its driver after its
own security check, so the nested pgx hook skips duplicate evaluation. It adds the
marker only when the context has a parent security operation. It also marks
other drivers, since registered driver names can be aliases. The marker does not
affect subsequent calls using the original request context, or pgx used through
uninstrumented `database/sql`.

Prepared statements through `database/sql` do not run that blocking check. With
the pgx hooks installed, their SQL text is monitored at execution but is not
blocked. Thus, the same injected SQL can be blocked by a direct
`ExecContext`/`QueryContext` call and only reported by a prepared statement.
Blocking on the prepared-statement path is not supported.

Monitoring examines SQL supplied at query/batch start, before pgx rewrites queries
or resolves prepared statement names. It does not interpolate bound parameters,
and does not provide complete coverage for custom query rewriters or execution by
prepared statement name. Direct `pgconn` calls, including calls through
`Conn.PgConn()`, bypass these pgx hooks.
