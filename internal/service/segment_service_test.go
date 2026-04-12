package service_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/xiongwp/id-generator/internal/domain/model"
	"github.com/xiongwp/id-generator/internal/service"
	"go.uber.org/zap"
)

// ─── Mock Repository ──────────────────────────────────────────────────────────

// mockSegmentRepo 模拟 SegmentRepository，atomic 推进 max_id。
type mockSegmentRepo struct {
	mu        sync.Mutex
	allocs    map[string]*model.LeafAlloc
	callCount int
}

func newMockRepo(allocs []model.LeafAlloc) *mockSegmentRepo {
	m := &mockSegmentRepo{allocs: make(map[string]*model.LeafAlloc)}
	for _, a := range allocs {
		clone := a
		m.allocs[a.BizTag] = &clone
	}
	return m
}

func (r *mockSegmentRepo) GetAllBizTags(_ context.Context) ([]model.LeafAlloc, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]model.LeafAlloc, 0, len(r.allocs))
	for _, v := range r.allocs {
		result = append(result, *v)
	}
	return result, nil
}

func (r *mockSegmentRepo) GetByBizTag(_ context.Context, bizTag string) (*model.LeafAlloc, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.allocs[bizTag]
	if !ok {
		return nil, nil
	}
	return v, nil
}

func (r *mockSegmentRepo) AllocNextSegment(_ context.Context, bizTag string) (*model.LeafAlloc, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.callCount++
	v, ok := r.allocs[bizTag]
	if !ok {
		return nil, fmt.Errorf("biz_tag not found: %s", bizTag)
	}
	v.MaxID += int64(v.Step)
	clone := *v
	return &clone, nil
}

func (r *mockSegmentRepo) Register(_ context.Context, alloc *model.LeafAlloc) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	clone := *alloc
	r.allocs[alloc.BizTag] = &clone
	return nil
}

// ─── Tests ────────────────────────────────────────────────────────────────────

func newSvc(t *testing.T, allocs []model.LeafAlloc) (service.SegmentService, *mockSegmentRepo) {
	t.Helper()
	repo := newMockRepo(allocs)
	svc := service.NewSegmentService(repo, 0.9, zap.NewNop())
	return svc, repo
}

// TestNextID_BasicSequence 验证首批 IDs 严格从 0 开始连续递增。
func TestNextID_BasicSequence(t *testing.T) {
	svc, _ := newSvc(t, []model.LeafAlloc{
		{BizTag: "order", MaxID: 0, Step: 100},
	})
	ctx := context.Background()

	if err := svc.Preload(ctx); err != nil {
		t.Fatal(err)
	}

	for i := int64(0); i < 100; i++ {
		id, err := svc.NextID(ctx, "order")
		if err != nil {
			t.Fatalf("NextID failed at i=%d: %v", i, err)
		}
		if id != i {
			t.Fatalf("expected id=%d, got %d", i, id)
		}
	}
}

// TestNextID_CrossSegmentBoundary 验证跨号段时 ID 仍单调递增，且触发二次 DB 调用。
func TestNextID_CrossSegmentBoundary(t *testing.T) {
	svc, repo := newSvc(t, []model.LeafAlloc{
		{BizTag: "order", MaxID: 0, Step: 10},
	})
	ctx := context.Background()

	if err := svc.Preload(ctx); err != nil {
		t.Fatal(err)
	}

	const totalIDs = 25
	ids := make([]int64, totalIDs)
	for i := 0; i < totalIDs; i++ {
		id, err := svc.NextID(ctx, "order")
		if err != nil {
			t.Fatalf("NextID[%d] error: %v", i, err)
		}
		ids[i] = id
	}

	// 验证单调递增
	for i := 1; i < totalIDs; i++ {
		if ids[i] <= ids[i-1] {
			t.Errorf("ids not monotone: ids[%d]=%d <= ids[%d]=%d", i, ids[i], i-1, ids[i-1])
		}
	}

	// 25 个 ID、step=10：至少需要 3 次 AllocNextSegment（包含预热）
	repo.mu.Lock()
	calls := repo.callCount
	repo.mu.Unlock()
	if calls < 3 {
		t.Errorf("expected ≥3 DB calls for cross-segment, got %d", calls)
	}
}

// TestNextID_UnknownBizTag 验证未注册的 biz_tag 返回 ErrBizTagNotFound。
func TestNextID_UnknownBizTag(t *testing.T) {
	svc, _ := newSvc(t, nil)
	ctx := context.Background()

	_, err := svc.NextID(ctx, "no_such_tag")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// TestBatchNextID_CountValidation 验证 count 越界时返回错误。
func TestBatchNextID_CountValidation(t *testing.T) {
	svc, _ := newSvc(t, []model.LeafAlloc{
		{BizTag: "order", MaxID: 0, Step: 10000},
	})
	ctx := context.Background()
	if err := svc.Preload(ctx); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		count   int
		wantErr bool
	}{
		{0, true},
		{-1, true},
		{10001, true},
		{1, false},
		{10000, false},
	}
	for _, tc := range cases {
		ids, err := svc.BatchNextID(ctx, "order", tc.count)
		if tc.wantErr && err == nil {
			t.Errorf("count=%d: expected error, got nil", tc.count)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("count=%d: unexpected error: %v", tc.count, err)
		}
		if !tc.wantErr && len(ids) != tc.count {
			t.Errorf("count=%d: expected %d IDs, got %d", tc.count, tc.count, len(ids))
		}
	}
}

// TestBatchNextID_Unique 验证批量 ID 无重复。
func TestBatchNextID_Unique(t *testing.T) {
	svc, _ := newSvc(t, []model.LeafAlloc{
		{BizTag: "order", MaxID: 0, Step: 50},
	})
	ctx := context.Background()
	if err := svc.Preload(ctx); err != nil {
		t.Fatal(err)
	}

	ids, err := svc.BatchNextID(ctx, "order", 200)
	if err != nil {
		t.Fatal(err)
	}

	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate ID: %d", id)
		}
		seen[id] = struct{}{}
	}
}

// TestNextID_Concurrent 高并发场景下验证无重复 ID。
func TestNextID_Concurrent(t *testing.T) {
	const goroutines = 20
	const idsEach = 50

	svc, _ := newSvc(t, []model.LeafAlloc{
		{BizTag: "concurrent", MaxID: 0, Step: 100},
	})
	ctx := context.Background()
	if err := svc.Preload(ctx); err != nil {
		t.Fatal(err)
	}

	type result struct {
		ids []int64
		err error
	}
	ch := make(chan result, goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			ids, err := svc.BatchNextID(ctx, "concurrent", idsEach)
			ch <- result{ids, err}
		}()
	}

	seen := make(map[int64]struct{}, goroutines*idsEach)
	for i := 0; i < goroutines; i++ {
		r := <-ch
		if r.err != nil {
			t.Errorf("goroutine error: %v", r.err)
			continue
		}
		for _, id := range r.ids {
			if _, dup := seen[id]; dup {
				t.Errorf("duplicate ID in concurrent test: %d", id)
			}
			seen[id] = struct{}{}
		}
	}

	if len(seen) != goroutines*idsEach {
		t.Errorf("expected %d unique IDs, got %d", goroutines*idsEach, len(seen))
	}
}

// TestRegisterBizTag 验证注册新 biz_tag 后可以获取 ID。
func TestRegisterBizTag(t *testing.T) {
	svc, _ := newSvc(t, nil)
	ctx := context.Background()

	if err := svc.RegisterBizTag(ctx, "new_tag", 1000, 500, "test tag"); err != nil {
		t.Fatalf("RegisterBizTag failed: %v", err)
	}

	id, err := svc.NextID(ctx, "new_tag")
	if err != nil {
		t.Fatalf("NextID after register failed: %v", err)
	}
	if id < 1000 {
		t.Errorf("expected id ≥ 1000 (initID), got %d", id)
	}
}
