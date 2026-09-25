# Build stage
FROM golang:1.27.1-alpine@sha256:4cb7ac979db5fcc41cae44b2227ba5ab8a51e8807f40d9ba4dee20a0ad960b5b AS builder
ENV CGO_ENABLED=1

WORKDIR /app
COPY . .

RUN apk add --no-cache --update git build-base

# Build the apim-callout binary
RUN go build -tags=appsec -o ./contrib/azure/apim-callout/cmd/apim-callout/apim-callout ./contrib/azure/apim-callout/cmd/apim-callout

# Base runtime stage (shared between production and e2e)
FROM alpine:3.24.2@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6 AS runtime-base

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
