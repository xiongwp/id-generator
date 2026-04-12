package main

import (
	"context"
	"time"

	"github.com/spf13/viper"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/fx"
	"go.uber.org/zap"

	grpcsvr "github.com/xiongwp/id-generator/internal/grpc"
	"github.com/xiongwp/id-generator/internal/infrastructure/database"
	kitexsvr "github.com/xiongwp/id-generator/internal/kitex"
	"github.com/xiongwp/id-generator/internal/repository"
	"github.com/xiongwp/id-generator/internal/service"
)

func main() {
	app := fx.New(
		fx.Provide(
			NewConfig,
			NewLogger,
			NewDatabaseManager,
			NewEtcdClient,
			repository.NewSegmentRepository,
			NewSegmentService,
			NewSnowflakeService,
			NewGRPCServer,
			NewKitexServer,
		),
		fx.Invoke(StartServers),
	)
	app.Run()
}

// ─── 配置与基础设施 ─────────────────────────────────────────────────────────────

func NewConfig() (*viper.Viper, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath("./config")
	v.AddConfigPath(".")
	v.AutomaticEnv()
	return v, v.ReadInConfig()
}

func NewLogger() (*zap.Logger, error) {
	return zap.NewProduction()
}

func NewDatabaseManager(v *viper.Viper, logger *zap.Logger) (*database.Manager, error) {
	var cfg database.Config
	if err := v.UnmarshalKey("database", &cfg); err != nil {
		return nil, err
	}
	mgr, err := database.NewManager(cfg)
	if err != nil {
		return nil, err
	}
	logger.Info("database ready", zap.String("dsn", maskDSN(cfg.DSN)))
	return mgr, nil
}

// NewEtcdClient 创建 etcd 客户端；若未配置 endpoints，返回 nil（雪花服务降级到 fallback worker_id）。
func NewEtcdClient(v *viper.Viper, logger *zap.Logger) (*clientv3.Client, error) {
	endpoints := v.GetStringSlice("etcd.endpoints")
	if len(endpoints) == 0 {
		logger.Info("etcd not configured, snowflake will use fallback_worker_id")
		return nil, nil
	}
	dialTimeout := v.GetDuration("etcd.dial_timeout")
	if dialTimeout == 0 {
		dialTimeout = 5 * time.Second
	}
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: dialTimeout,
	})
	if err != nil {
		logger.Warn("etcd client creation failed, using fallback_worker_id",
			zap.Strings("endpoints", endpoints),
			zap.Error(err),
		)
		return nil, nil // 不阻断启动，雪花服务自行降级
	}
	logger.Info("etcd client ready", zap.Strings("endpoints", endpoints))
	return client, nil
}

// ─── 业务服务 ──────────────────────────────────────────────────────────────────

func NewSegmentService(
	repo repository.SegmentRepository,
	v *viper.Viper,
	logger *zap.Logger,
) service.SegmentService {
	loadFactor := v.GetFloat64("segment.load_factor")
	return service.NewSegmentService(repo, loadFactor, logger)
}

func NewSnowflakeService(
	etcdClient *clientv3.Client,
	v *viper.Viper,
	logger *zap.Logger,
) (service.SnowflakeService, error) {
	var cfg service.SnowflakeConfig
	if err := v.UnmarshalKey("snowflake", &cfg); err != nil {
		return nil, err
	}
	return service.NewSnowflakeService(etcdClient, cfg, logger)
}

// ─── RPC 服务器 ────────────────────────────────────────────────────────────────

func NewGRPCServer(
	segmentSvc service.SegmentService,
	snowflakeSvc service.SnowflakeService,
	v *viper.Viper,
	logger *zap.Logger,
) *grpcsvr.Server {
	port := v.GetInt("server.grpc_port")
	if port == 0 {
		port = 9090
	}
	return grpcsvr.NewServer(segmentSvc, snowflakeSvc, port, logger)
}

func NewKitexServer(
	segmentSvc service.SegmentService,
	snowflakeSvc service.SnowflakeService,
	v *viper.Viper,
	logger *zap.Logger,
) (*kitexsvr.Server, error) {
	port := v.GetInt("server.kitex_port")
	if port == 0 {
		port = 9091
	}
	idlPath := v.GetString("server.idl_path")
	if idlPath == "" {
		idlPath = "idl/id_generator.thrift"
	}
	return kitexsvr.NewServer(segmentSvc, snowflakeSvc, port, idlPath, logger)
}

// ─── fx 生命周期 ────────────────────────────────────────────────────────────────

// StartServers 注册 fx 生命周期钩子，启动 gRPC 和 Kitex 服务器。
func StartServers(
	lc fx.Lifecycle,
	grpcSvr *grpcsvr.Server,
	kitexSvr *kitexsvr.Server,
	segmentSvc service.SegmentService,
	etcdClient *clientv3.Client,
	dbMgr *database.Manager,
	logger *zap.Logger,
) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			// 预热所有已注册 biz_tag 的号段（避免首次请求从 DB 加载）
			if err := segmentSvc.Preload(ctx); err != nil {
				logger.Warn("segment preload failed (non-fatal)", zap.Error(err))
			}

			// 启动 gRPC 服务器（异步）
			go func() {
				if err := grpcSvr.Start(); err != nil {
					logger.Error("gRPC server exited", zap.Error(err))
				}
			}()

			// 启动 Kitex 服务器（异步）
			go func() {
				if err := kitexSvr.Start(); err != nil {
					logger.Error("Kitex server exited", zap.Error(err))
				}
			}()

			return nil
		},
		OnStop: func(_ context.Context) error {
			grpcSvr.Stop()
			kitexSvr.Stop()
			if etcdClient != nil {
				_ = etcdClient.Close()
			}
			return dbMgr.Close()
		},
	})
}

// ─── 辅助函数 ──────────────────────────────────────────────────────────────────

// maskDSN 隐藏 DSN 中的密码（日志安全）。
func maskDSN(dsn string) string {
	if len(dsn) > 30 {
		return dsn[:15] + "***"
	}
	return "***"
}
