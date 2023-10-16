FROM golang:1.20 as builder
WORKDIR /app
COPY . .

RUN CGO_ENABLED=0 go build ./proxy.go

FROM alpine:latest

COPY --from=builder /app/proxy /app/proxy

ENV BACKEND_PORT=3306
ENV BACKEND_HOST=localhost
ENV PROXY_PORT=3307

CMD ["sh", "-c", "/app/proxy ${PROXY_PORT} ${BACKEND_HOST} ${BACKEND_PORT} /etc/server-cert.pem /etc/server-key.pem"]
