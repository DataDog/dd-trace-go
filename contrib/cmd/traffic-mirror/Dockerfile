# Build stage
FROM golang:1.23-alpine AS builder
ENV CGO_ENABLED=1

WORKDIR /app
COPY . .

RUN apk add --no-cache --update git build-base

# Build the serviceextensions binary
RUN go build -tags=appsec -o ./traffic-mirror ./contrib/net/http/cmd/traffic-mirror

# Runtime stage
FROM alpine:3.20.3
RUN apk --no-cache add ca-certificates tzdata libc6-compat libgcc libstdc++
WORKDIR /app

COPY --from=builder /app/traffic-mirror /app/traffic-mirror

 # Traffic mirror port
EXPOSE 8080

# Healthcheck port
EXPOSE 8081

CMD ["/app/traffic-mirror"]
