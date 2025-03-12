FROM --platform=${BUILDPLATFORM:-linux/amd64} golang:1.24-alpine as builder

ARG TARGETPLATFORM
ARG BUILDPLATFORM
ARG TARGETOS
ARG TARGETARCH

ENV CGO_ENABLED=0
ENV GOOS=${TARGETOS}
ENV GOARCH=${TARGETARCH}

WORKDIR /src
COPY go.mod go.sum /src/
RUN go mod download

COPY . /src
RUN go build -ldflags='-s -w' -trimpath -o /proxy

FROM alpine AS compressor
RUN apk add --no-cache upx
COPY --from=builder /proxy /proxy
RUN upx -9 /proxy -o /proxy-compressed

FROM --platform=${TARGETPLATFORM:-linux/amd64} scratch AS base
EXPOSE 80 443 443/udp
ENV CONFIG_FILE=/config/config.yml
ENV PUID=1000
ENV PGID=1000
ENTRYPOINT [ "/proxy" ]

FROM --platform=${TARGETPLATFORM:-linux/amd64} base AS compressed
COPY --from=builder /proxy-compressed /proxy

FROM --platform=${TARGETPLATFORM:-linux/amd64} base AS uncompressed
COPY --from=builder /proxy /proxy
