# Build stage
FROM golang:1.23-alpine AS builder
ENV CGO_ENABLED=1
WORKDIR /app
COPY . .
RUN apk add --no-cache --update git build-base
RUN go build -tags=appsec -o ./contrib/envoyproxy/go-control-plane/cmd/serviceextensions/serviceextensions ./contrib/envoyproxy/go-control-plane/cmd/serviceextensions

# Runtime stage
FROM alpine:3.20.3
RUN apk --no-cache add ca-certificates tzdata libc6-compat libgcc libstdc++
WORKDIR /app
COPY --from=builder /app/contrib/envoyproxy/go-control-plane/cmd/serviceextensions/serviceextensions /app/serviceextensions
COPY ./contrib/envoyproxy/go-control-plane/cmd/serviceextensions/localhost.crt /app/localhost.crt
COPY ./contrib/envoyproxy/go-control-plane/cmd/serviceextensions/localhost.key /app/localhost.key

EXPOSE 80
EXPOSE 443

CMD ["./serviceextensions"]
