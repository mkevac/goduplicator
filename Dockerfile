FROM golang:alpine AS build
RUN apk add git && go get github.com/mkevac/goduplicator

FROM alpine
COPY --from=build /go/bin/goduplicator /usr/local/bin/goduplicator
ENTRYPOINT ["/usr/local/bin/goduplicator"]
