# syntax=docker/dockerfile:1
# Go version: go.mod declares go 1.25.11; the toolchain image is 1.25.x >= that.
FROM golang:1.25.14-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/wallet ./cmd/wallet

FROM alpine:3.24.2
RUN apk add --no-cache ca-certificates && adduser -D -H -u 10001 wallet
COPY --from=build /out/wallet /usr/local/bin/wallet
USER wallet
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/wallet"]
