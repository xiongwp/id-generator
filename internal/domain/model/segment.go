package model

import (
	"sync"
	"sync/atomic"
	"time"
)

// LeafAlloc 号段分配表映射（对应 leaf_alloc 表）
type LeafAlloc struct {
	BizTag      string    `gorm:"column:biz_tag;primaryKey"`
	MaxID       int64     `gorm:"column:max_id"`
	Step        int       `gorm:"column:step"`
	Description *string   `gorm:"column:description"`
	UpdateTime  time.Time `gorm:"column:update_time;autoUpdateTime"`
}

func (LeafAlloc) TableName() string { return "leaf_alloc" }

// ─── Segment：单个号段 ──────────────────────────────────────────────────────────

// Segment 代表从数据库分配到的一个号段 [cur, max)。
// cur 使用 atomic int64，允许无锁自增；max 为只读，初始化后不变。
type Segment struct {
	cur  atomic.Int64 // 当前游标（下一个可用 ID）
	max  int64        // 本号段上限（不含）
	step int          // 本次分配的步长（冗余存储，用于诊断）
}

// NewSegment 创建号段：[start, start+step)
func NewSegment(start int64, step int) *Segment {
	s := &Segment{max: start + int64(step), step: step}
	s.cur.Store(start)
	return s
}

// Next 返回下一个 ID；若号段已用完则返回 -1。
func (s *Segment) Next() int64 {
	v := s.cur.Add(1) - 1
	if v >= s.max {
		return -1
	}
	return v
}

// Remaining 返回剩余可用 ID 数量。
func (s *Segment) Remaining() int64 {
	r := s.max - s.cur.Load()
	if r < 0 {
		return 0
	}
	return r
}

// Total 返回本号段总大小（step）。
func (s *Segment) Total() int64 { return int64(s.step) }

// Exhausted 报告号段是否已耗尽。
func (s *Segment) Exhausted() bool { return s.cur.Load() >= s.max }

// ─── SegmentBuffer：双 Buffer ──────────────────────────────────────────────────

// SegmentBuffer 为单个 biz_tag 维护双号段缓冲，减少数据库访问频率。
//
// 双 Buffer 策略：
//   - 正常消费 current 号段；
//   - 当 current 消耗到 loadFactor（默认 90%）时，异步预加载 next 号段；
//   - current 耗尽后无缝切换到 next，同时 next 变为新的预加载目标。
type SegmentBuffer struct {
	mu         sync.Mutex
	bizTag     string
	segments   [2]*Segment // 双 Buffer，index 0/1 轮换
	current    int         // 当前活跃 Buffer 下标（0 或 1）
	loadFactor float64     // 触发预加载的消耗比例
	nextReady  bool        // next Buffer 是否已就绪
	loading    bool        // 是否正在后台加载
}

// NewSegmentBuffer 创建空 SegmentBuffer，需在 Initialize 后方可使用。
func NewSegmentBuffer(bizTag string, loadFactor float64) *SegmentBuffer {
	return &SegmentBuffer{
		bizTag:     bizTag,
		loadFactor: loadFactor,
	}
}

// Initialize 用第一个号段初始化 SegmentBuffer（须在持有 mu 时调用）。
func (b *SegmentBuffer) Initialize(seg *Segment) {
	b.segments[0] = seg
	b.current = 0
	b.nextReady = false
	b.loading = false
}

// Current 返回当前活跃的 Segment（不加锁，由调用方在持有 mu 时调用）。
func (b *SegmentBuffer) Current() *Segment { return b.segments[b.current] }

// next Buffer 下标（0↔1 轮换）。
func (b *SegmentBuffer) nextIdx() int { return 1 - b.current }

// ShouldPreload 判断是否需要触发预加载（须在持有 mu 时调用）。
func (b *SegmentBuffer) ShouldPreload() bool {
	if b.nextReady || b.loading {
		return false
	}
	seg := b.Current()
	consumed := float64(seg.Total()-seg.Remaining()) / float64(seg.Total())
	return consumed >= b.loadFactor
}

// MarkLoading 标记正在加载（须在持有 mu 时调用）。
func (b *SegmentBuffer) MarkLoading() { b.loading = true }

// SetNextSegment 将预加载完成的号段写入 next Buffer（须在持有 mu 时调用）。
func (b *SegmentBuffer) SetNextSegment(seg *Segment) {
	b.segments[b.nextIdx()] = seg
	b.nextReady = true
	b.loading = false
}

// Switch 切换到 next Buffer，返回新的当前 Segment（须在持有 mu 时调用）。
func (b *SegmentBuffer) Switch() *Segment {
	b.current = b.nextIdx()
	b.nextReady = false
	return b.segments[b.current]
}

// NextReady 报告 next Buffer 是否已就绪（须在持有 mu 时调用）。
func (b *SegmentBuffer) NextReady() bool { return b.nextReady }

// Loading 报告是否正在后台加载（须在持有 mu 时调用）。
func (b *SegmentBuffer) Loading() bool { return b.loading }

// CancelLoading 取消加载标记（加载失败时调用，允许后续重试）。
func (b *SegmentBuffer) CancelLoading() { b.loading = false }

// Lock / Unlock 将 SegmentBuffer 的互斥锁暴露给 service 层。
func (b *SegmentBuffer) Lock()   { b.mu.Lock() }
func (b *SegmentBuffer) Unlock() { b.mu.Unlock() }
