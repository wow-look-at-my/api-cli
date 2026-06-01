FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /api-cli .

FROM alpine:3.21
RUN apk add --no-cache curl jq
COPY --from=builder /api-cli /usr/local/bin/api-cli
COPY github.example.json /github.example.json
ENTRYPOINT ["api-cli", "--config", "/github.example.json", "--mcp"]
CMD ["stdio"]
