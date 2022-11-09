FROM golang as go-format
ARG version
ENV VERSION=$version
ADD https://raw.githubusercontent.com/DataDog/appsec-event-rules/$version/build/recommended.json /home/rules.json
COPY escaper/ /home/
WORKDIR /home/
RUN go run escaper.go ${VERSION} > rule.go

FROM scratch
ARG version=1.2.5
COPY --from=go-format /home/rule.go rule.go
COPY --from=go-format /home/rules.json rules.json
