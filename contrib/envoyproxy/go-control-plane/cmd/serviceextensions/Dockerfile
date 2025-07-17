# Build stage
FROM golang:1.23-alpine AS builder
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
FROM alpine:3.20.3
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
