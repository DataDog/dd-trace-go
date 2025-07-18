# Build stage
FROM golang:1.24-alpine AS builder
ENV CGO_ENABLED=1

RUN apk add --no-cache --update git build-base
RUN --mount=type=bind,src=.,dst=/build go -C /build build -tags=appsec -o /request-mirror /build/contrib/k8s.io/gateway-api/cmd/request-mirror

# Runtime stage
FROM alpine:3.20.3

# Set opencontainers labels for Github container registry
LABEL org.opencontainers.image.source=https://github.com/DataDog/dd-trace-go/tree/main/contrib/k8s.io/gateway-api/
LABEL org.opencontainers.image.description="A request mirror for the Kubernetes Gateway API with Datadog App & API Protection support"
LABEL org.opencontainers.image.licenses=Apache-2.0

RUN apk --no-cache add ca-certificates tzdata libc6-compat libgcc libstdc++
WORKDIR /app

COPY --from=builder /request-mirror /app/request-mirror

ENV DD_APPSEC_ENABLED=true \
    DD_APPSEC_WAF_TIMEOUT=10ms \
    _DD_APPSEC_BLOCKING_UNAVAILABLE=true \
    DD_SERVICE=request-mirror \
    DD_APM_TRACING_ENABLED=false \
    DD_TRACE_GIT_METADATA_ENABLED=false

EXPOSE 8080

# Healthcheck port
EXPOSE 8081

CMD ["/app/request-mirror"]
