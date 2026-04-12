package handler

import (
	"context"

	"github.com/xiongwp/id-generator/gen/kitex_gen/idgen/v1/idgenservice"
	"github.com/xiongwp/id-generator/internal/service"
	"go.uber.org/zap"
)

// IdGenServiceHandler Kitex 业务处理器，实现 idgenservice.IdGenServiceHandler 接口
type IdGenServiceHandler struct {
	segmentSvc   service.SegmentService
	snowflakeSvc service.SnowflakeService
	logger       *zap.Logger
}

// NewIdGenServiceHandler 创建 Kitex 处理器
func NewIdGenServiceHandler(
	segmentSvc service.SegmentService,
	snowflakeSvc service.SnowflakeService,
	logger *zap.Logger,
) *IdGenServiceHandler {
	return &IdGenServiceHandler{
		segmentSvc:   segmentSvc,
		snowflakeSvc: snowflakeSvc,
		logger:       logger,
	}
}

func (h *IdGenServiceHandler) GetSegmentId(ctx context.Context, req *idgenservice.GetSegmentIdRequest) (*idgenservice.GetSegmentIdResponse, error) {
	id, err := h.segmentSvc.NextID(ctx, req.BizTag)
	if err != nil {
		return &idgenservice.GetSegmentIdResponse{Code: codeOf(err), Message: err.Error()}, nil
	}
	return &idgenservice.GetSegmentIdResponse{ID: id, Code: 0}, nil
}

func (h *IdGenServiceHandler) BatchGetSegmentId(ctx context.Context, req *idgenservice.BatchGetSegmentIdRequest) (*idgenservice.BatchGetSegmentIdResponse, error) {
	ids, err := h.segmentSvc.BatchNextID(ctx, req.BizTag, int(req.Count))
	if err != nil {
		return &idgenservice.BatchGetSegmentIdResponse{Code: codeOf(err), Message: err.Error()}, nil
	}
	return &idgenservice.BatchGetSegmentIdResponse{IDs: ids, Code: 0}, nil
}

func (h *IdGenServiceHandler) GetSnowflakeId(_ context.Context, _ *idgenservice.GetSnowflakeIdRequest) (*idgenservice.GetSnowflakeIdResponse, error) {
	id, err := h.snowflakeSvc.NextID()
	if err != nil {
		return &idgenservice.GetSnowflakeIdResponse{Code: 1, Message: err.Error()}, nil
	}
	return &idgenservice.GetSnowflakeIdResponse{ID: id, Code: 0}, nil
}

func (h *IdGenServiceHandler) BatchGetSnowflakeId(_ context.Context, req *idgenservice.BatchGetSnowflakeIdRequest) (*idgenservice.BatchGetSnowflakeIdResponse, error) {
	ids, err := h.snowflakeSvc.BatchNextID(int(req.Count))
	if err != nil {
		return &idgenservice.BatchGetSnowflakeIdResponse{Code: 1, Message: err.Error()}, nil
	}
	return &idgenservice.BatchGetSnowflakeIdResponse{IDs: ids, Code: 0}, nil
}

func (h *IdGenServiceHandler) RegisterBizTag(ctx context.Context, req *idgenservice.RegisterBizTagRequest) (*idgenservice.RegisterBizTagResponse, error) {
	err := h.segmentSvc.RegisterBizTag(ctx, req.BizTag, req.InitID, int(req.Step), req.Description)
	if err != nil {
		return &idgenservice.RegisterBizTagResponse{Code: 1, Message: err.Error()}, nil
	}
	return &idgenservice.RegisterBizTagResponse{Code: 0}, nil
}

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
