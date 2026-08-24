FROM golang:1.27.0@sha256:65b6f280bf050ec5af12716857e8ea8439d694dbba8f31ceeb7630670071f2bb
COPY . /dd-trace-go
WORKDIR /dd-trace-go/internal/apps
# -t will download all dependencies, including test dependencies
RUN go get -t ./...
ENTRYPOINT [ "./run-scenario.bash" ]