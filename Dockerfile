FROM golang:1.22-bookworm AS build

WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/wgnh ./cmd/wgnh

FROM alpine:3.20

COPY --from=build /out/wgnh /usr/local/bin/wgnh
EXPOSE 9090
ENTRYPOINT ["/usr/local/bin/wgnh"]
