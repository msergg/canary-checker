FROM node:12 as node
WORKDIR /app
COPY ./ ./
RUN make vue-dist

FROM golang:1.16 as builder
WORKDIR /app
COPY ./ ./
ARG NAME
ARG VERSION
COPY --from=node /app/statuspage/dist /app/statuspage/dist
WORKDIR /app/statuspage/dist
WORKDIR /app
RUN go version
RUN GOOS=linux GOARCH=amd64 go build -o canary-checker -ldflags "-X \"main.version=$VERSION\""  main.go

FROM ubuntu:bionic
WORKDIR /app
# install CA certificates
RUN apt-get update && \
  apt-get install -y ca-certificates && \
  rm -Rf /var/lib/apt/lists/*  && \
  rm -Rf /usr/share/doc && rm -Rf /usr/share/man  && \
  apt-get clean
COPY --from=builder /app/canary-checker /app
ENTRYPOINT ["/app/canary-checker"]
