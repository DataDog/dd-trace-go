# Build stage
FROM golang:1.27.0-alpine@sha256:4c9fe60190a2a3350ddc51de80d0224b8a6698d12bdfc999fee45ea9d6c46dbc AS builder
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
FROM alpine:3.24.1@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b

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
