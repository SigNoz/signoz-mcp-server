FROM golang:1.26-alpine AS builder

WORKDIR /src

COPY main.go .

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /webhook-sink ./main.go

FROM scratch

COPY --from=builder /webhook-sink /webhook-sink

EXPOSE 8080

ENTRYPOINT ["/webhook-sink"]
