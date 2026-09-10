Override for `reviewers/security.md` (in the core skill folder) — read that file first, then this.

# Security — dd-trace-go specifics

This file starts with one confirmed pattern and should grow — add the next
one you learn from review. Do not treat it as exhaustive.

## Secrets must not become span tags or log lines

Never write any of the following into a span tag, a metric, or a log line:

- `DD_API_KEY` / `DD_APP_KEY` / `cfg.APIKey` / any similarly named config field
- A DSN or URL with an embedded username and password (`amqp://user:password@host:5672`)
- `Authorization`, `Cookie`, or other credential-bearing headers

`span.SetTag("dsn", cfg.DSN)` and `log.Printf("connecting to %s", cfg.DSN)` are the same finding: the secret leaves the process and becomes customer-visible. Treat it as **P0**.

A host-shaped tag key does not make a credential-bearing URL safe. `span.SetTag("peer.hostname", cfg.BrokerURL)` where `BrokerURL` is `amqp://user:password@host` is the same P0 — the tag name looks like OpenTelemetry peer metadata, the value still ships the password.

Safe alternatives: tag the host / port / scheme only, or a redacted DSN (`amqp://***@host:5672`). Do not log the raw value "at debug, it's fine" — debug logs ship. Do not "fix" it by renaming the tag (`peer.url`, `amqp.url`) while still storing the raw URL.

Look for this around contrib connect / authenticate / client-init paths (`contrib/**`) and anything that reads `internal/env` or a connection config struct.
