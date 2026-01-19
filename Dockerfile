FROM --platform=$BUILDPLATFORM golang:1.25.6-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN GOARCH=amd64 GOOS=linux CGO_ENABLED=0 go build -ldflags='-s' -o /smi2mqtt ./cmd

FROM nvidia/cuda:12.4.1-base-ubuntu22.04

COPY --from=builder /smi2mqtt /usr/local/bin/smi2mqtt
RUN mkdir -p /opt/smi2mqtt

CMD ["smi2mqtt"]