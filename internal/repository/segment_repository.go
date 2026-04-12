package repository

import (
	"context"
	"fmt"

	"github.com/xiongwp/id-generator/internal/domain/model"
	"github.com/xiongwp/id-generator/internal/infrastructure/database"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SegmentRepository 号段仓储接口
type SegmentRepository interface {
	// GetAllBizTags 返回所有已注册的业务标签
	GetAllBizTags(ctx context.Context) ([]model.LeafAlloc, error)

	// GetByBizTag 查询单个业务标签
	GetByBizTag(ctx context.Context, bizTag string) (*model.LeafAlloc, error)

	// AllocNextSegment 原子地推进 max_id，返回本次号段 [old_max_id, old_max_id+step)
	// 使用 UPDATE ... SET max_id=max_id+step 保证并发安全
	AllocNextSegment(ctx context.Context, bizTag string) (*model.LeafAlloc, error)

	// Register 注册新业务标签（不存在时插入，存在时忽略）
	Register(ctx context.Context, alloc *model.LeafAlloc) error
}

type segmentRepository struct {
	mgr *database.Manager
}

// NewSegmentRepository 创建号段仓储
func NewSegmentRepository(mgr *database.Manager) SegmentRepository {
	return &segmentRepository{mgr: mgr}
}

func (r *segmentRepository) GetAllBizTags(ctx context.Context) ([]model.LeafAlloc, error) {
	var allocs []model.LeafAlloc
	if err := r.mgr.DB().WithContext(ctx).Find(&allocs).Error; err != nil {
		return nil, fmt.Errorf("segment: GetAllBizTags: %w", err)
	}
	return allocs, nil
}

func (r *segmentRepository) GetByBizTag(ctx context.Context, bizTag string) (*model.LeafAlloc, error) {
	var alloc model.LeafAlloc
	result := r.mgr.DB().WithContext(ctx).Where("biz_tag = ?", bizTag).First(&alloc)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("segment: GetByBizTag(%s): %w", bizTag, result.Error)
	}
	return &alloc, nil
}

// AllocNextSegment 原子推进 max_id 并返回更新后的行。
// 采用 UPDATE-then-SELECT（同一事务内）保证返回值与推进量一致：
//  1. UPDATE leaf_alloc SET max_id = max_id + step WHERE biz_tag = ?
//  2. SELECT * FROM leaf_alloc WHERE biz_tag = ?  FOR UPDATE
func (r *segmentRepository) AllocNextSegment(ctx context.Context, bizTag string) (*model.LeafAlloc, error) {
	var alloc model.LeafAlloc

	err := r.mgr.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 推进 max_id
		if err := tx.Model(&model.LeafAlloc{}).
			Where("biz_tag = ?", bizTag).
			Update("max_id", gorm.Expr("max_id + step")).Error; err != nil {
			return fmt.Errorf("update max_id: %w", err)
		}
		// 2. 读取最新值（加行锁，保证串行化）
		return tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("biz_tag = ?", bizTag).
			First(&alloc).Error
	})
	if err != nil {
		return nil, fmt.Errorf("segment: AllocNextSegment(%s): %w", bizTag, err)
	}
	return &alloc, nil
}

func (r *segmentRepository) Register(ctx context.Context, alloc *model.LeafAlloc) error {
	result := r.mgr.DB().WithContext(ctx).
		Where("biz_tag = ?", alloc.BizTag).
		FirstOrCreate(alloc)
	return result.Error
}
