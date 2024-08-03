FROM golang:1.22 AS builder

WORKDIR /app
COPY go.mod ./
COPY go.sum ./
COPY main.go ./
RUN CGO_ENABLED=0 go build .

FROM debian:buster-slim
WORKDIR /root/

RUN apt update && apt install -y conntrack
COPY --from=builder /app/dns-blackhole-tester .

CMD ["./dns-blackhole-tester"]
