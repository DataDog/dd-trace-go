# Build stage
FROM golang:1.27.1-alpine@sha256:4cb7ac979db5fcc41cae44b2227ba5ab8a51e8807f40d9ba4dee20a0ad960b5b AS builder
ENV CGO_ENABLED=1

WORKDIR /app
COPY . .

RUN apk add --no-cache --update git build-base openssl

# Build the spoa binary
RUN go build -tags=appsec -o ./contrib/haproxy/stream-processing-offload/cmd/spoa/spoa ./contrib/haproxy/stream-processing-offload/cmd/spoa

# Runtime stage
FROM alpine:3.24.2@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6

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
