FROM golang:1.21-alpine AS builder
WORKDIR /src/app/
RUN apk add git
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o=/usr/local/bin/firewall ./cmd/firewall

FROM gcr.io/distroless/static
WORKDIR /
COPY --from=builder /usr/local/bin/firewall .
ENTRYPOINT ["/firewall"]