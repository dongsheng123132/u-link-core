FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/remote-agent-server ./cmd/server

FROM alpine:3.20
RUN addgroup -S ulink && adduser -S -G ulink ulink
COPY --from=build /out/remote-agent-server /usr/local/bin/remote-agent-server
USER ulink
EXPOSE 8900
ENTRYPOINT ["/usr/local/bin/remote-agent-server"]
