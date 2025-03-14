# Stage 1: Build the static library
FROM golang:1 AS builder

# Install musl-tools for musl-gcc
RUN apt-get update && apt-get install -y musl-tools

# Argument can be set during build time with --build-arg GOARCH=arm64
ARG GOARCH=amd64

# Configure environment variables for CGO, operating system, architecture, and compiler
ENV CGO_LDFLAGS_ALLOW=".*" \
    CGO_ENABLED=1 \
    GOOS=linux \
    GOARCH=$GOARCH \
    CC=musl-gcc

WORKDIR /app

# Copy everything from the current directory to the PWD (Present Working Directory) inside the container
COPY . .

WORKDIR /app/internal/civisibility/native

# Build the static library
RUN go build -tags civisibility_native -buildmode=c-archive -o ./output/libcivisibility.a *.go

# Stage 2: Extract the static library
FROM alpine:latest

# Build arguments for the final archive names; you can override these during build
ARG FILE_NAME=libcivisibility

# Install p7zip for compression
RUN apk add --no-cache p7zip

# Create the output folder
RUN mkdir -p /output

# Copy the static library and header from the builder stage
COPY --from=builder /app/internal/civisibility/native/output/libcivisibility.a /output/libcivisibility.a
COPY --from=builder /app/internal/civisibility/native/output/libcivisibility.h /output/libcivisibility.h

# Compress both files into a 7z archive
RUN 7z a /output/${FILE_NAME}.7z /output/libcivisibility.a /output/libcivisibility.h
# Create a SHA256 checksum file for the archive
RUN sha256sum /output/${FILE_NAME}.7z > /output/${FILE_NAME}.7z.sha256sum

RUN rm /output/libcivisibility.a /output/libcivisibility.h

# Command to run when the container starts
ENTRYPOINT ["sh", "-c", "ls /output &cp /output/libcivisibility.* /libcivisibility && echo 'Static library copied to /libcivisibility'"]

# ARM64 library
#docker build --platform linux/arm64 --build-arg GOARCH=arm64 --build-arg FILE_NAME=linux-arm64-libcivisibility -t libcivisibility-builder:arm64 -f ./Dockerfile ../../..
#docker run --rm -v .:/libcivisibility libcivisibility-builder:arm64

# AMD64 library
#docker build --platform linux/amd64 --build-arg GOARCH=amd64 --build-arg FILE_NAME=linux-AMD64-libcivisibility -t libcivisibility-builder:amd64 -f ./Dockerfile ../../..
#docker run --rm -v .:/libcivisibility libcivisibility-builder:amd64
