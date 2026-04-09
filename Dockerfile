# 构建阶段
FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY . .

RUN go mod tidy
RUN go build -o id-server ./cmd/server

# 运行阶段（更小更安全）
FROM alpine:latest

WORKDIR /app
COPY --from=builder /app/id-server .

EXPOSE 9090

CMD ["./id-server"]