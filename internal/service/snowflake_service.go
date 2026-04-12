package service

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
)

// 雪花算法位布局（总 63 位，符号位保持为 0）:
//   [42 bits timestamp] [5 bits datacenter] [5 bits worker] [12 bits sequence]
//
// 支持约 139 年（从 epoch 起）、32 个数据中心、32 个 worker、每毫秒 4096 个 ID。
const (
	snowflakeEpoch        = int64(1700000000000) // 2023-11-15 自定义纪元（毫秒）
	workerIDBits          = 5
	datacenterIDBits      = 5
	sequenceBits          = 12
	maxWorkerID           = int64(-1) ^ (int64(-1) << workerIDBits)     // 31
	maxDatacenterID       = int64(-1) ^ (int64(-1) << datacenterIDBits) // 31
	maxSequence           = int64(-1) ^ (int64(-1) << sequenceBits)     // 4095
	workerIDShift         = sequenceBits
	datacenterIDShift     = sequenceBits + workerIDBits
	timestampShift        = sequenceBits + workerIDBits + datacenterIDBits
	etcdWorkerKeyPrefix   = "/id-generator/workers/"
	etcdLeaseTTL          = 30 // seconds
	etcdMaxWorkerSlots    = 32 // 最多 32 个 worker（对应 maxWorkerID+1）
)

// SnowflakeService 雪花 ID 生成服务接口
type SnowflakeService interface {
	// NextID 生成下一个雪花 ID
	NextID() (int64, error)
	// BatchNextID 批量生成（count ≤ 10000）
	BatchNextID(count int) ([]int64, error)
	// WorkerID 返回当前 worker 编号
	WorkerID() int64
	// DatacenterID 返回当前数据中心编号
	DatacenterID() int64
}

// SnowflakeConfig 雪花 ID 服务配置
type SnowflakeConfig struct {
	DatacenterID      int64  `mapstructure:"datacenter_id"`
	FallbackWorkerID  int64  `mapstructure:"fallback_worker_id"`
}

type snowflakeService struct {
	mu           sync.Mutex
	datacenterID int64
	workerID     int64
	lastTS       int64
	sequence     int64
	logger       *zap.Logger
}

// NewSnowflakeService 创建雪花 ID 服务，通过 etcd 动态分配 workerID。
// 若 etcd 不可用，降级使用 cfg.FallbackWorkerID。
func NewSnowflakeService(
	etcdClient *clientv3.Client,
	cfg SnowflakeConfig,
	logger *zap.Logger,
) (SnowflakeService, error) {
	workerID := cfg.FallbackWorkerID

	if etcdClient != nil {
		wid, err := registerWorker(etcdClient, cfg.DatacenterID, cfg.FallbackWorkerID, logger)
		if err != nil {
			logger.Warn("etcd worker registration failed, using fallback worker_id",
				zap.Int64("fallback_worker_id", cfg.FallbackWorkerID),
				zap.Error(err),
			)
		} else {
			workerID = wid
		}
	}

	if cfg.DatacenterID < 0 || cfg.DatacenterID > maxDatacenterID {
		return nil, fmt.Errorf("snowflake: datacenter_id %d out of range [0, %d]", cfg.DatacenterID, maxDatacenterID)
	}
	if workerID < 0 || workerID > maxWorkerID {
		return nil, fmt.Errorf("snowflake: worker_id %d out of range [0, %d]", workerID, maxWorkerID)
	}

	logger.Info("snowflake service initialized",
		zap.Int64("datacenterID", cfg.DatacenterID),
		zap.Int64("workerID", workerID),
	)

	return &snowflakeService{
		datacenterID: cfg.DatacenterID,
		workerID:     workerID,
		logger:       logger,
	}, nil
}

func (s *snowflakeService) NextID() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := currentMillis()

	if now < s.lastTS {
		// 时钟回拨：等待时钟追上，最多等 10ms
		wait := s.lastTS - now
		if wait > 10 {
			return 0, fmt.Errorf("snowflake: clock moved backwards by %d ms", wait)
		}
		for now < s.lastTS {
			time.Sleep(time.Millisecond)
			now = currentMillis()
		}
	}

	if now == s.lastTS {
		s.sequence = (s.sequence + 1) & maxSequence
		if s.sequence == 0 {
			// 当前毫秒序列溢出，等到下一毫秒
			for now <= s.lastTS {
				time.Sleep(time.Millisecond)
				now = currentMillis()
			}
		}
	} else {
		s.sequence = 0
	}

	s.lastTS = now

	id := (now-snowflakeEpoch)<<timestampShift |
		(s.datacenterID << datacenterIDShift) |
		(s.workerID << workerIDShift) |
		s.sequence

	return id, nil
}

func (s *snowflakeService) BatchNextID(count int) ([]int64, error) {
	if count <= 0 || count > 10000 {
		return nil, fmt.Errorf("count must be in [1, 10000], got %d", count)
	}
	ids := make([]int64, 0, count)
	for i := 0; i < count; i++ {
		id, err := s.NextID()
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (s *snowflakeService) WorkerID() int64     { return s.workerID }
func (s *snowflakeService) DatacenterID() int64 { return s.datacenterID }

// ─── etcd Worker 注册 ──────────────────────────────────────────────────────────

// registerWorker 通过 etcd 竞争注册 worker 槽位，返回分配到的 workerID。
// 使用带 TTL 的租约 + 事务（compare-and-set）保证同一槽位只有一个实例持有。
func registerWorker(client *clientv3.Client, datacenterID, fallback int64, logger *zap.Logger) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 申请租约（TTL = 30s，需定期续约）
	leaseResp, err := client.Grant(ctx, etcdLeaseTTL)
	if err != nil {
		return fallback, fmt.Errorf("grant lease: %w", err)
	}

	// 尝试从 0 到 maxWorkerSlots-1 抢占空闲槽位
	key := fmt.Sprintf("%sdc%d/", etcdWorkerKeyPrefix, datacenterID)
	for slot := int64(0); slot < etcdMaxWorkerSlots; slot++ {
		slotKey := fmt.Sprintf("%s%d", key, slot)

		txnResp, err := client.Txn(ctx).
			If(clientv3.Compare(clientv3.CreateRevision(slotKey), "=", 0)). // key 不存在
			Then(clientv3.OpPut(slotKey, "1", clientv3.WithLease(leaseResp.ID))).
			Commit()
		if err != nil {
			continue
		}
		if txnResp.Succeeded {
			// 抢到槽位，启动 keepAlive goroutine（后台运行，生命周期与进程一致）
			go keepAlive(client, leaseResp.ID, slotKey, logger)
			logger.Info("etcd worker slot acquired",
				zap.String("key", slotKey),
				zap.Int64("workerID", slot),
			)
			return slot, nil
		}
	}

	_, _ = client.Revoke(context.Background(), leaseResp.ID) //nolint:errcheck
	return fallback, fmt.Errorf("all %d worker slots occupied", etcdMaxWorkerSlots)
}

var keepAliveActive atomic.Bool

// keepAlive 持续续约，防止 key 过期被他人抢占
func keepAlive(client *clientv3.Client, leaseID clientv3.LeaseID, key string, logger *zap.Logger) {
	keepAliveActive.Store(true)
	ch, err := client.KeepAlive(context.Background(), leaseID)
	if err != nil {
		logger.Error("keepAlive start failed", zap.Error(err))
		return
	}
	for resp := range ch {
		if resp == nil {
			logger.Warn("etcd keepAlive channel closed", zap.String("key", key))
			return
		}
	}
}

func currentMillis() int64 {
	return time.Now().UnixMilli()
}
