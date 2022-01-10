FROM golang:1-alpine as builder

RUN apk add --no-cache musl-dev gcc

ADD . /src

WORKDIR /src

RUN cd cmd/gost && go env && go build -v

FROM alpine:latest

WORKDIR /bin/

COPY --from=builder /src/cmd/gost/gost .

ENTRYPOINT ["/bin/gost"]
