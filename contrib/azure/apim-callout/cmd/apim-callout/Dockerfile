# Build stage
FROM golang:1.25-alpine AS builder
ENV CGO_ENABLED=1

WORKDIR /app
COPY . .

RUN apk add --no-cache --update git build-base

# Build the apim-callout binary
RUN go build -tags=appsec -o ./contrib/azure/apim-callout/cmd/apim-callout/apim-callout ./contrib/azure/apim-callout/cmd/apim-callout

# Base runtime stage (shared between production and e2e)
FROM alpine:3.20.3 AS runtime-base

RUN apk --no-cache add ca-certificates tzdata libc6-compat libgcc libstdc++
WORKDIR /app

COPY --from=builder "/app/contrib/azure/apim-callout/cmd/apim-callout/apim-callout" "/app/apim-callout"

EXPOSE 8080
EXPOSE 8081

CMD ["./apim-callout"]

# E2E test stage — includes custom WAF rules for integration testing.
# Build with: docker build --target e2e -t apim-callout-e2e .
FROM runtime-base AS e2e
COPY --from=builder /app/internal/appsec/testdata/user_rules.json /app/rules.json
ENV DD_APPSEC_RULES=/app/rules.json

# Production runtime stage (default target — no test rules).
# CI builds this by default since it is the last stage.
FROM runtime-base AS runtime

# Set opencontainers labels for Github container registry
LABEL org.opencontainers.image.source=https://github.com/DataDog/dd-trace-go/tree/main/contrib/azure/apim-callout/cmd/apim-callout
LABEL org.opencontainers.image.description="An Azure APIM / Boomi HTTP Callout service with Datadog App & API Protection support"
LABEL org.opencontainers.image.licenses=Apache-2.0

ARG COMMIT_SHA=""
LABEL org.opencontainers.image.revision=${COMMIT_SHA}

ARG DD_VERSION=""
ENV DD_VERSION=${DD_VERSION}
