# Stage 1: Build the static library
FROM golang:1.24-bullseye AS builder

# Install dependencies
RUN apt-get update && apt-get install -y gcc

# Argument can be set during build time with --build-arg GOARCH=arm64
ARG GOARCH=amd64

# Configure environment variables for CGO, operating system, architecture, and compiler
ENV CGO_LDFLAGS_ALLOW=".*" \
    CGO_ENABLED=1 \
    GOOS=linux \
    GOARCH=$GOARCH \
    CC=gcc

WORKDIR /app

# Copy everything from the current directory to the PWD (Present Working Directory) inside the container
COPY . .

WORKDIR /app/internal/civisibility/native

# Build the library
RUN go build -tags civisibility_native -buildmode=c-archive -o ./output/static/libtestoptimization.a *.go
RUN go build -tags civisibility_native -buildmode=c-shared -o ./output/dynamic/libtestoptimization.so *.go

# Stage 2: Extract the library
FROM alpine:latest

# Build arguments for the final archive names; you can override these during build
ARG FILE_NAME=libtestoptimization

# Install p7zip for compression
RUN apk add --no-cache p7zip

# Create the output folder
RUN mkdir -p /output

# Copy the static library and header from the builder stage
COPY --from=builder /app/internal/civisibility/native/output/static/libtestoptimization.a /output/static/libtestoptimization.a
COPY --from=builder /app/internal/civisibility/native/output/static/libtestoptimization.h /output/static/libtestoptimization.h
COPY --from=builder /app/internal/civisibility/native/output/dynamic/libtestoptimization.so /output/dynamic/libtestoptimization.so
COPY --from=builder /app/internal/civisibility/native/output/dynamic/libtestoptimization.h /output/dynamic/libtestoptimization.h

# Compress both files into a 7z archive
RUN 7z a /output/${FILE_NAME}-static.7z /output/static/*.*
RUN 7z a /output/${FILE_NAME}-dynamic.7z /output/dynamic/*.*
# Create a SHA256 checksum file for the archive
RUN sha256sum /output/${FILE_NAME}-static.7z > /output/${FILE_NAME}-static.7z.sha256sum
RUN sha256sum /output/${FILE_NAME}-dynamic.7z > /output/${FILE_NAME}-dynamic.7z.sha256sum

RUN rm -r /output/static /output/dynamic

# Command to run when the container starts
ENTRYPOINT ["sh", "-c", "ls /output && cp /output/*.* /libtestoptimization && echo 'Static library copied.'"]
