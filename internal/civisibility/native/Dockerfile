# Stage 1: Build the static library
FROM golang:1-alpine AS builder

# Install dependencies
RUN apk add --no-cache gcc musl-dev

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
RUN go build -tags civisibility_native -buildmode=c-archive -ldflags '-s -w -extldflags "-static"' -o ./output/static/libcivisibility.a *.go
RUN go build -tags civisibility_native -buildmode=c-shared -ldflags '-s -w' -o ./output/dynamic/libcivisibility.so *.go

# Stage 2: Extract the library
FROM alpine:latest

# Build arguments for the final archive names; you can override these during build
ARG FILE_NAME=libcivisibility

# Install p7zip for compression
RUN apk add --no-cache p7zip

# Create the output folder
RUN mkdir -p /output

# Copy the static library and header from the builder stage
COPY --from=builder /app/internal/civisibility/native/output/static/libcivisibility.a /output/static/libcivisibility.a
COPY --from=builder /app/internal/civisibility/native/output/static/libcivisibility.h /output/static/libcivisibility.h
COPY --from=builder /app/internal/civisibility/native/output/dynamic/libcivisibility.so /output/dynamic/libcivisibility.so
COPY --from=builder /app/internal/civisibility/native/output/dynamic/libcivisibility.h /output/dynamic/libcivisibility.h

# Compress both files into a 7z archive
RUN 7z a /output/${FILE_NAME}-static.7z /output/static/libcivisibility.a /output/static/libcivisibility.h
RUN 7z a /output/${FILE_NAME}-dynamic.7z /output/dynamic/libcivisibility.so /output/dynamic/libcivisibility.h
# Create a SHA256 checksum file for the archive
RUN sha256sum /output/${FILE_NAME}-static.7z > /output/${FILE_NAME}-static.7z.sha256sum
RUN sha256sum /output/${FILE_NAME}-dynamic.7z > /output/${FILE_NAME}-dynamic.7z.sha256sum

RUN rm -r /output/static /output/dynamic

# Command to run when the container starts
ENTRYPOINT ["sh", "-c", "ls /output && cp /output/*.* /libcivisibility && echo 'Static library copied.'"]

# ARM64 library
#docker build --platform linux/arm64 --build-arg GOARCH=arm64 --build-arg FILE_NAME=linux-arm64-libcivisibility -t libcivisibility-builder:arm64 -f ./Dockerfile ../../..
#docker run --rm -v .:/libcivisibility libcivisibility-builder:arm64

# AMD64 library
#docker build --platform linux/amd64 --build-arg GOARCH=amd64 --build-arg FILE_NAME=linux-amd64-libcivisibility -t libcivisibility-builder:amd64 -f ./Dockerfile ../../..
#docker run --rm -v .:/libcivisibility libcivisibility-builder:amd64
