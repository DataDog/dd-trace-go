# Build stage
FROM golang:1.27.1-alpine@sha256:4cb7ac979db5fcc41cae44b2227ba5ab8a51e8807f40d9ba4dee20a0ad960b5b AS builder
ENV CGO_ENABLED=1

WORKDIR /app
COPY . .

RUN apk add --no-cache --update git build-base openssl

# Generate SSL self-signed localhost certificate
RUN openssl genrsa -out localhost.key 3072
RUN openssl req -new \
  -key localhost.key \
  -subj "/C=US/ST=New York/O=Datadog/OU=gRPC/CN=localhost" \
  -out request.csr
RUN openssl x509 -req -days 3660 \
  -in request.csr \
  -signkey localhost.key \
  -out localhost.crt

# Build the serviceextensions binary
RUN go build -tags=appsec -o ./contrib/envoyproxy/go-control-plane/cmd/serviceextensions/serviceextensions ./contrib/envoyproxy/go-control-plane/cmd/serviceextensions

# Runtime stage
FROM alpine:3.24.2@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6

# Set opencontainers labels for Github container registry
LABEL org.opencontainers.image.source=https://github.com/DataDog/dd-trace-go/tree/main/contrib/envoyproxy/go-control-plane/cmd/serviceextensions
LABEL org.opencontainers.image.description="An Envoy External Processor service with Datadog App & API Protection support"
LABEL org.opencontainers.image.licenses=Apache-2.0

ARG COMMIT_SHA=""
LABEL org.opencontainers.image.revision=${COMMIT_SHA}

RUN apk --no-cache add ca-certificates tzdata libc6-compat libgcc libstdc++
WORKDIR /app

ARG DD_VERSION=""
ENV DD_VERSION=${DD_VERSION}

COPY --from=builder /app/contrib/envoyproxy/go-control-plane/cmd/serviceextensions/serviceextensions /app/serviceextensions
COPY --from=builder /app/localhost.crt /app/localhost.crt
COPY --from=builder /app/localhost.key /app/localhost.key

EXPOSE 80
EXPOSE 443

CMD ["./serviceextensions"]
