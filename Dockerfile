# ─── Build stage ──────────────────────────────────────────────────────────────
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git

WORKDIR /src

# 下载依赖（利用 Docker layer 缓存）
COPY go.mod go.sum ./
RUN go mod download

# 编译
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /app/id-generator ./cmd/server

# ─── Runtime stage ────────────────────────────────────────────────────────────
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /app/id-generator .

# 配置文件（docker 启动时使用 config.yaml，由 docker-compose 挂载或覆盖）
COPY config/config.docker.yaml ./config/config.yaml

# Thrift IDL（Kitex 泛型服务器在运行时解析 IDL）
COPY idl/ ./idl/

EXPOSE 9090 9091

CMD ["./id-generator"]
