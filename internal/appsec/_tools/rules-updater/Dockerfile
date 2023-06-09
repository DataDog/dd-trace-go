FROM golang:1.20 as go-format
ARG version
ENV VERSION=$version
ADD https://raw.githubusercontent.com/DataDog/appsec-event-rules/$version/build/recommended.json /home/rules.json
COPY writer/ /home/
WORKDIR /home/
RUN go run writer.go ${VERSION} > rules.go

FROM scratch
COPY --from=go-format /home/rules.go rules.go
COPY --from=go-format /home/rules.json rules.json
