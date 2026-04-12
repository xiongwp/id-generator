package kitex

import (
	"context"
	"fmt"
	"net"

	"github.com/cloudwego/kitex/pkg/generic"
	"github.com/cloudwego/kitex/server"
	"github.com/cloudwego/kitex/server/genericserver"
	"github.com/xiongwp/id-generator/gen/kitex_gen/idgen/v1/idgenservice"
	khandler "github.com/xiongwp/id-generator/internal/kitex/handler"
	"github.com/xiongwp/id-generator/internal/service"
	"go.uber.org/zap"
)

// Server Kitex 泛型 JSON/Thrift 服务器（兼容标准 Kitex 客户端）。
//
// 运行机制：
//   - 解析 Thrift IDL 获取服务描述符，使用 Kitex JSONThriftGeneric 编解码；
//   - 所有 RPC 方法通过 genericDispatcher 路由到内部 handler；
//   - 客户端以 JSON 格式传入请求字段（字段名与 IDL 一致），接收 JSON 响应。
//
// 注：生产环境也可运行 kitex 工具生成类型安全的 Thrift 二进制代码：
//
//	kitex -type thrift -module github.com/xiongwp/id-generator \
//	      -out-dir gen/kitex_gen idl/id_generator.thrift
type Server struct {
	svr    server.Server
	port   int
	logger *zap.Logger
}

// NewServer 创建 Kitex 服务器。
// idlPath 为 Thrift IDL 文件路径（相对于进程工作目录），例如 "idl/id_generator.thrift"。
func NewServer(
	segmentSvc service.SegmentService,
	snowflakeSvc service.SnowflakeService,
	port int,
	idlPath string,
	logger *zap.Logger,
) (*Server, error) {
	h := khandler.NewIdGenServiceHandler(segmentSvc, snowflakeSvc, logger)

	p, err := generic.NewThriftFileProvider(idlPath)
	if err != nil {
		return nil, fmt.Errorf("kitex: load IDL %q: %w", idlPath, err)
	}
	g, err := generic.JSONThriftGeneric(p)
	if err != nil {
		return nil, fmt.Errorf("kitex: create JSON/Thrift generic: %w", err)
	}

	addr, _ := net.ResolveTCPAddr("tcp", fmt.Sprintf(":%d", port))
	svr := genericserver.NewServer(
		&genericDispatcher{h: h},
		g,
		server.WithServiceAddr(addr),
	)

	return &Server{svr: svr, port: port, logger: logger}, nil
}

// Start 启动 Kitex 服务器（阻塞，直到 Stop 调用或发生错误）。
func (s *Server) Start() error {
	s.logger.Info("Kitex server starting", zap.Int("port", s.port))
	return s.svr.Run()
}

// Stop 优雅关闭 Kitex 服务器。
func (s *Server) Stop() {
	s.logger.Info("Kitex server stopping")
	_ = s.svr.Stop()
}

// ─── genericDispatcher ────────────────────────────────────────────────────────

// genericDispatcher 实现 generic.Service 接口，将 Kitex 泛型 JSON 请求
// 路由到对应的类型安全 handler 方法。
//
// 请求字段名与 Thrift IDL struct 字段名一致（下划线命名）。
// 返回值为 map[string]interface{}，由 Kitex 按 IDL 序列化为 JSON 响应。
type genericDispatcher struct {
	h *khandler.IdGenServiceHandler
}

// GenericCall 实现 generic.Service 接口。
// request 类型为 map[string]interface{}（来自 JSON 解码）。
func (d *genericDispatcher) GenericCall(ctx context.Context, method string, request interface{}) (interface{}, error) {
	m, _ := request.(map[string]interface{})

	switch method {
	case "GetSegmentId":
		bizTag, _ := m["biz_tag"].(string)
		resp, err := d.h.GetSegmentId(ctx, &idgenservice.GetSegmentIdRequest{BizTag: bizTag})
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"id":      resp.ID,
			"code":    resp.Code,
			"message": resp.Message,
		}, nil

	case "BatchGetSegmentId":
		bizTag, _ := m["biz_tag"].(string)
		count, _ := m["count"].(float64) // JSON numbers decoded as float64
		resp, err := d.h.BatchGetSegmentId(ctx, &idgenservice.BatchGetSegmentIdRequest{
			BizTag: bizTag,
			Count:  int32(count),
		})
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"ids":     resp.IDs,
			"code":    resp.Code,
			"message": resp.Message,
		}, nil

	case "GetSnowflakeId":
		resp, err := d.h.GetSnowflakeId(ctx, &idgenservice.GetSnowflakeIdRequest{})
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"id":      resp.ID,
			"code":    resp.Code,
			"message": resp.Message,
		}, nil

	case "BatchGetSnowflakeId":
		count, _ := m["count"].(float64)
		resp, err := d.h.BatchGetSnowflakeId(ctx, &idgenservice.BatchGetSnowflakeIdRequest{
			Count: int32(count),
		})
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"ids":     resp.IDs,
			"code":    resp.Code,
			"message": resp.Message,
		}, nil

	case "RegisterBizTag":
		bizTag, _ := m["biz_tag"].(string)
		initID, _ := m["init_id"].(float64)
		step, _ := m["step"].(float64)
		description, _ := m["description"].(string)
		resp, err := d.h.RegisterBizTag(ctx, &idgenservice.RegisterBizTagRequest{
			BizTag:      bizTag,
			InitID:      int64(initID),
			Step:        int32(step),
			Description: description,
		})
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"code":    resp.Code,
			"message": resp.Message,
		}, nil

	default:
		return nil, fmt.Errorf("kitex: unknown method %q", method)
	}
}
