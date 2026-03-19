FROM golang:1.26-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -o /relay ./cmd/relay/

FROM alpine:3.21

RUN apk add --no-cache ca-certificates
COPY --from=builder /relay /usr/local/bin/relay

RUN mkdir -p /var/lib/akemon-relay

EXPOSE 8080

ENTRYPOINT ["relay"]
CMD ["-addr", ":8080", "-db", "/var/lib/akemon-relay/relay.db"]
