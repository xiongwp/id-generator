package service

import (
	"context"
	"fmt"
	"sync"

	"github.com/xiongwp/id-generator/internal/domain/model"
	"github.com/xiongwp/id-generator/internal/repository"
	"go.uber.org/zap"
)

// ErrBizTagNotFound 业务标签未注册
var ErrBizTagNotFound = fmt.Errorf("biz_tag not found")

// ErrSegmentExhausted 号段耗尽（极端情况：DB 不可用且双 Buffer 均空）
var ErrSegmentExhausted = fmt.Errorf("segment exhausted")

// SegmentService 号段模式 ID 生成服务接口
type SegmentService interface {
	// NextID 获取指定业务标签的下一个 ID
	NextID(ctx context.Context, bizTag string) (int64, error)

	// BatchNextID 批量获取 ID（count ≤ 10000）
	BatchNextID(ctx context.Context, bizTag string, count int) ([]int64, error)

	// RegisterBizTag 注册新业务标签
	RegisterBizTag(ctx context.Context, bizTag string, initID int64, step int, description string) error

	// Preload 预热所有已注册业务标签的号段（服务启动时调用）
	Preload(ctx context.Context) error
}

// segmentService 实现双 Buffer 号段模式
type segmentService struct {
	repo       repository.SegmentRepository
	loadFactor float64
	buffers    sync.Map // map[bizTag]*model.SegmentBuffer
	logger     *zap.Logger
}

// NewSegmentService 创建号段服务
func NewSegmentService(repo repository.SegmentRepository, loadFactor float64, logger *zap.Logger) SegmentService {
	if loadFactor <= 0 || loadFactor >= 1 {
		loadFactor = 0.9
	}
	return &segmentService{
		repo:       repo,
		loadFactor: loadFactor,
		logger:     logger,
	}
}

// Preload 启动时预热：从 DB 加载所有 biz_tag 并初始化第一个号段
func (s *segmentService) Preload(ctx context.Context) error {
	allocs, err := s.repo.GetAllBizTags(ctx)
	if err != nil {
		return fmt.Errorf("segment preload: %w", err)
	}
	for _, alloc := range allocs {
		if _, loaded := s.buffers.Load(alloc.BizTag); !loaded {
			buf := model.NewSegmentBuffer(alloc.BizTag, s.loadFactor)
			s.buffers.Store(alloc.BizTag, buf)
		}
		buf := s.bufferOf(alloc.BizTag)
		if err := s.loadSegment(ctx, alloc.BizTag, buf); err != nil {
			s.logger.Warn("segment preload: failed to load segment",
				zap.String("bizTag", alloc.BizTag),
				zap.Error(err),
			)
		}
	}
	s.logger.Info("segment preload completed", zap.Int("bizTagCount", len(allocs)))
	return nil
}

// NextID 从双 Buffer 中获取下一个 ID。
// 流程：
//  1. 从 current segment 取号；
//  2. 若消耗超过 loadFactor，触发异步预加载 next segment；
//  3. 若 current 耗尽且 next 就绪，切换 Buffer；
//  4. 若 current 耗尽但 next 未就绪，同步加载（兜底，极少发生）。
func (s *segmentService) NextID(ctx context.Context, bizTag string) (int64, error) {
	buf, err := s.ensureBuffer(ctx, bizTag)
	if err != nil {
		return 0, err
	}

	for {
		buf.Lock()

		// 尝试从 current segment 取号
		seg := buf.Current()
		id := seg.Next()
		if id >= 0 {
			// 检查是否需要触发异步预加载
			if buf.ShouldPreload() {
				buf.MarkLoading()
				go s.asyncLoadNextSegment(context.Background(), bizTag, buf)
			}
			buf.Unlock()
			return id, nil
		}

		// current 耗尽：尝试切换到 next Buffer
		if buf.NextReady() {
			buf.Switch()
			// 新 current 继续循环取号
			buf.Unlock()
			continue
		}

		// next 未就绪且没有在加载：同步加载（兜底路径）
		if !buf.Loading() {
			buf.MarkLoading()
			buf.Unlock()
			if err := s.loadSegment(ctx, bizTag, buf); err != nil {
				return 0, fmt.Errorf("segment.NextID(%s): reload failed: %w", bizTag, err)
			}
			continue
		}

		// 正在异步加载中：解锁后稍候重试（yield）
		buf.Unlock()
		// 使用 runtime.Gosched() 的语义，不睡眠
		continue
	}
}

// BatchNextID 批量获取 ID（count ≤ 10000）
func (s *segmentService) BatchNextID(ctx context.Context, bizTag string, count int) ([]int64, error) {
	if count <= 0 || count > 10000 {
		return nil, fmt.Errorf("count must be in [1, 10000], got %d", count)
	}
	ids := make([]int64, 0, count)
	for i := 0; i < count; i++ {
		id, err := s.NextID(ctx, bizTag)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// RegisterBizTag 注册新业务标签
func (s *segmentService) RegisterBizTag(ctx context.Context, bizTag string, initID int64, step int, description string) error {
	if bizTag == "" {
		return fmt.Errorf("biz_tag must not be empty")
	}
	if step <= 0 {
		step = 10000
	}
	if initID <= 0 {
		initID = 1
	}
	alloc := &model.LeafAlloc{
		BizTag:      bizTag,
		MaxID:       initID,
		Step:        step,
		Description: &description,
	}
	return s.repo.Register(ctx, alloc)
}

// ─── 内部辅助方法 ──────────────────────────────────────────────────────────────

func (s *segmentService) bufferOf(bizTag string) *model.SegmentBuffer {
	v, _ := s.buffers.Load(bizTag)
	return v.(*model.SegmentBuffer)
}

// ensureBuffer 确保 SegmentBuffer 存在并初始化，首次调用时同步加载第一个号段。
func (s *segmentService) ensureBuffer(ctx context.Context, bizTag string) (*model.SegmentBuffer, error) {
	v, loaded := s.buffers.Load(bizTag)
	if loaded {
		return v.(*model.SegmentBuffer), nil
	}

	// 验证 biz_tag 是否在 DB 中注册
	alloc, err := s.repo.GetByBizTag(ctx, bizTag)
	if err != nil {
		return nil, err
	}
	if alloc == nil {
		return nil, fmt.Errorf("%w: %s", ErrBizTagNotFound, bizTag)
	}

	buf := model.NewSegmentBuffer(bizTag, s.loadFactor)
	actual, _ := s.buffers.LoadOrStore(bizTag, buf)
	buf = actual.(*model.SegmentBuffer)

	// 仅首次创建者负责加载；其余并发请求等下一次循环即可获取
	buf.Lock()
	if buf.Current() == nil {
		buf.Unlock()
		if err := s.loadSegment(ctx, bizTag, buf); err != nil {
			return nil, err
		}
	} else {
		buf.Unlock()
	}
	return buf, nil
}

// loadSegment 从 DB 获取下一号段并初始化（或替换）Buffer 的 current/next。
func (s *segmentService) loadSegment(ctx context.Context, bizTag string, buf *model.SegmentBuffer) error {
	alloc, err := s.repo.AllocNextSegment(ctx, bizTag)
	if err != nil {
		return err
	}
	// 号段区间：[maxID - step, maxID)
	start := alloc.MaxID - int64(alloc.Step)
	seg := model.NewSegment(start, alloc.Step)

	buf.Lock()
	defer buf.Unlock()

	if buf.Current() == nil {
		// 首次初始化
		buf.Initialize(seg)
	} else {
		// 设置 next Buffer
		buf.SetNextSegment(seg)
	}

	s.logger.Debug("segment loaded",
		zap.String("bizTag", bizTag),
		zap.Int64("start", start),
		zap.Int64("end", alloc.MaxID),
		zap.Int("step", alloc.Step),
	)
	return nil
}

// asyncLoadNextSegment 异步预加载下一号段
func (s *segmentService) asyncLoadNextSegment(ctx context.Context, bizTag string, buf *model.SegmentBuffer) {
	if err := s.loadSegment(ctx, bizTag, buf); err != nil {
		s.logger.Warn("async load next segment failed",
			zap.String("bizTag", bizTag),
			zap.Error(err),
		)
		// 清除 loading 标记，允许下次重试
		buf.Lock()
		buf.CancelLoading()
		buf.Unlock()
	}
}
