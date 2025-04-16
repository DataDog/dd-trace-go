# Build stage
FROM golang:1.24-alpine AS builder
ENV CGO_ENABLED=1

WORKDIR /app
COPY . .

RUN apk add --no-cache --update git build-base
RUN go build -tags=appsec -o ./request-mirror ./contrib/net/http/cmd/request-mirror

# Runtime stage
FROM alpine:3.20.3
RUN apk --no-cache add ca-certificates tzdata libc6-compat libgcc libstdc++
WORKDIR /app

COPY --from=builder /app/request-mirror /app/request-mirror

 # Traffic mirror port
EXPOSE 8080

# Healthcheck port
EXPOSE 8081

CMD ["/app/request-mirror"]
