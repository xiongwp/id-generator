package grpc

import (
	"context"
	"fmt"
	"net"

	"github.com/xiongwp/id-generator/internal/service"
	pb "github.com/xiongwp/id-generator/gen/pb/idgen/v1"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// Server gRPC 服务器（兼容标准 gRPC 客户端）
type Server struct {
	pb.UnimplementedIdGenServiceServer
	segmentSvc   service.SegmentService
	snowflakeSvc service.SnowflakeService
	grpcServer   *grpc.Server
	port         int
	logger       *zap.Logger
}

// NewServer 创建 gRPC 服务器
func NewServer(
	segmentSvc service.SegmentService,
	snowflakeSvc service.SnowflakeService,
	port int,
	logger *zap.Logger,
) *Server {
	s := &Server{
		segmentSvc:   segmentSvc,
		snowflakeSvc: snowflakeSvc,
		port:         port,
		logger:       logger,
	}
	s.grpcServer = grpc.NewServer()
	pb.RegisterIdGenServiceServer(s.grpcServer, s)
	reflection.Register(s.grpcServer)
	return s
}

// Start 启动监听（阻塞）
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.port)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("grpc listen on %s: %w", addr, err)
	}
	s.logger.Info("gRPC server starting", zap.String("addr", addr))
	return s.grpcServer.Serve(lis)
}

// Stop 优雅关闭
func (s *Server) Stop() {
	s.logger.Info("gRPC server stopping")
	s.grpcServer.GracefulStop()
}

// ─── RPC 实现 ──────────────────────────────────────────────────────────────────

// GetSegmentId 获取单个号段 ID
func (s *Server) GetSegmentId(ctx context.Context, req *pb.GetSegmentIdRequest) (*pb.GetSegmentIdResponse, error) {
	id, err := s.segmentSvc.NextID(ctx, req.BizTag)
	if err != nil {
		s.logger.Warn("GetSegmentId failed",
			zap.String("bizTag", req.BizTag),
			zap.Error(err),
		)
		return &pb.GetSegmentIdResponse{
			Code:    codeOf(err),
			Message: err.Error(),
		}, nil
	}
	return &pb.GetSegmentIdResponse{Id: id, Code: 0}, nil
}

// BatchGetSegmentId 批量获取号段 ID
func (s *Server) BatchGetSegmentId(ctx context.Context, req *pb.BatchGetSegmentIdRequest) (*pb.BatchGetSegmentIdResponse, error) {
	ids, err := s.segmentSvc.BatchNextID(ctx, req.BizTag, int(req.Count))
	if err != nil {
		return &pb.BatchGetSegmentIdResponse{
			Code:    codeOf(err),
			Message: err.Error(),
		}, nil
	}
	return &pb.BatchGetSegmentIdResponse{Ids: ids, Code: 0}, nil
}

// GetSnowflakeId 获取单个雪花 ID
func (s *Server) GetSnowflakeId(_ context.Context, _ *pb.GetSnowflakeIdRequest) (*pb.GetSnowflakeIdResponse, error) {
	id, err := s.snowflakeSvc.NextID()
	if err != nil {
		return &pb.GetSnowflakeIdResponse{Code: 1, Message: err.Error()}, nil
	}
	return &pb.GetSnowflakeIdResponse{Id: id, Code: 0}, nil
}

// BatchGetSnowflakeId 批量获取雪花 ID
func (s *Server) BatchGetSnowflakeId(_ context.Context, req *pb.BatchGetSnowflakeIdRequest) (*pb.BatchGetSnowflakeIdResponse, error) {
	ids, err := s.snowflakeSvc.BatchNextID(int(req.Count))
	if err != nil {
		return &pb.BatchGetSnowflakeIdResponse{Code: 1, Message: err.Error()}, nil
	}
	return &pb.BatchGetSnowflakeIdResponse{Ids: ids, Code: 0}, nil
}

// RegisterBizTag 注册新业务标签
func (s *Server) RegisterBizTag(ctx context.Context, req *pb.RegisterBizTagRequest) (*pb.RegisterBizTagResponse, error) {
	err := s.segmentSvc.RegisterBizTag(ctx, req.BizTag, req.InitId, int(req.Step), req.Description)
	if err != nil {
		return &pb.RegisterBizTagResponse{Code: 1, Message: err.Error()}, nil
	}
	return &pb.RegisterBizTagResponse{Code: 0}, nil
}

// codeOf 将常见错误映射为业务错误码
func codeOf(err error) int32 {
	switch err {
	case service.ErrBizTagNotFound:
		return 1001
	case service.ErrSegmentExhausted:
		return 1002
	default:
		return 500
	}
}
