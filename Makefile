.PHONY: build test lint tidy docker docker-up docker-down proto kitex help

# 二进制输出目录
BIN_DIR := bin
BINARY  := $(BIN_DIR)/id-generator
MODULE  := github.com/xiongwp/id-generator

## build: 编译服务二进制
build:
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -trimpath -o $(BINARY) ./cmd/server
	@echo "✓ build → $(BINARY)"

## test: 运行单元测试
test:
	go test -race -count=1 ./...

## lint: 静态检查（需要 golangci-lint）
lint:
	golangci-lint run ./...

## tidy: 整理依赖
tidy:
	go mod tidy
	go mod vendor

## docker: 构建 Docker 镜像
docker:
	docker build -t id-generator:latest .

## docker-up: 启动所有容器（后台）
docker-up:
	docker-compose up -d

## docker-down: 停止并移除容器（保留数据卷）
docker-down:
	docker-compose down

## proto: 从 proto 文件重新生成 gRPC 代码
## 前置：protoc + protoc-gen-go + protoc-gen-go-grpc
proto:
	protoc \
		--go_out=gen/pb \
		--go_opt=paths=source_relative \
		--go-grpc_out=gen/pb \
		--go-grpc_opt=paths=source_relative \
		proto/idgen/v1/id_generator.proto
	@echo "✓ proto → gen/pb/"

## kitex: 从 Thrift IDL 重新生成 Kitex 代码（类型安全 Thrift 二进制编解码）
## 前置：go install github.com/cloudwego/kitex/tool/cmd/kitex@latest
kitex:
	kitex \
		-type thrift \
		-module $(MODULE) \
		-out-dir gen/kitex_gen \
		idl/id_generator.thrift
	@echo "✓ kitex → gen/kitex_gen/"

## help: 打印所有可用 target
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'
