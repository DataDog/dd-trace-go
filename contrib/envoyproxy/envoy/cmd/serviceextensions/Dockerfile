# Build stage
FROM golang:1.22-alpine AS builder
ENV CGO_ENABLED=1
WORKDIR /app
COPY . .
RUN apk add --no-cache --update git build-base
RUN go build -o ./contrib/envoyproxy/envoy/cmd/serviceextensions/serviceextensions ./contrib/envoyproxy/envoy/cmd/serviceextensions

# Runtime stage
FROM alpine:3.20.3
RUN apk --no-cache add ca-certificates tzdata libc6-compat libgcc libstdc++
WORKDIR /app
COPY --from=builder /app/contrib/envoyproxy/envoy/cmd/serviceextensions/serviceextensions /app/serviceextensions
COPY ./contrib/envoyproxy/envoy/cmd/serviceextensions/localhost.crt /app/localhost.crt
COPY ./contrib/envoyproxy/envoy/cmd/serviceextensions/localhost.key /app/localhost.key

EXPOSE 80
EXPOSE 443

CMD ["./serviceextensions"]
