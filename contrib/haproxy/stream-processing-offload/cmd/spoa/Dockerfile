# Build stage
FROM golang:1.24-alpine AS builder
ENV CGO_ENABLED=1

WORKDIR /app
COPY . .

RUN apk add --no-cache --update git build-base openssl

# Build the spoa binary
RUN go build -tags=appsec -o ./contrib/haproxy/stream-processing-offload/cmd/spoa/spoa ./contrib/haproxy/stream-processing-offload/cmd/spoa

# Runtime stage
FROM alpine:3.20.3

# Set opencontainers labels for Github container registry
LABEL org.opencontainers.image.source=https://github.com/DataDog/dd-trace-go/tree/main/contrib/haproxy/stream-processing-offload/cmd/spoa
LABEL org.opencontainers.image.description="An HAProxy Stream Processing Offload Agent service with Datadog App & API Protection support"
LABEL org.opencontainers.image.licenses=Apache-2.0

ARG COMMIT_SHA=""
LABEL org.opencontainers.image.revision=${COMMIT_SHA}

RUN apk --no-cache add ca-certificates tzdata libc6-compat libgcc libstdc++
WORKDIR /app

ARG DD_VERSION=""
ENV DD_VERSION=${DD_VERSION}

COPY --from=builder /app/contrib/haproxy/stream-processing-offload/cmd/spoa/spoa /app/spoa

EXPOSE 3000
EXPOSE 3080

CMD ["./spoa"]
