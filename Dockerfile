FROM golang:alpine as builder
LABEL maintainer="tetra5"
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . . 
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /diabler ./cmd/diabler/main.go

FROM scratch
WORKDIR /
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /diabler /diabler
ENV TELEGRAM_TOKEN=""
ENTRYPOINT ["/diabler"]