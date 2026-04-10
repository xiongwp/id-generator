package main

import (
	"database/sql"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/xiongwp/id-generator/internal/generator"
	"github.com/xiongwp/id-generator/internal/metrics"
	"github.com/xiongwp/id-generator/internal/segment"
	"github.com/xiongwp/id-generator/internal/service"
	"github.com/xiongwp/id-generator/internal/worker"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc"

	pb "github.com/xiongwp/id-generator/internal/proto"

	_ "github.com/go-sql-driver/mysql"
)

func main() {
	// ================================
	// 1️⃣ 初始化 metrics
	// ================================
	metrics.Init()

	go func() {
		http.Handle("/metrics", promhttp.Handler())
		log.Println("metrics server at :2112")
		log.Fatal(http.ListenAndServe(":2112", nil))
	}()

	// ================================
	// 2️⃣ 初始化 etcd（workerId）
	// ================================
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{"etcd:2379"},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		log.Println("etcd connect failed, fallback to local:", err)
	}

	workerID := worker.Register(cli)
	log.Println("workerID:", workerID)

	// ================================
	// 3️⃣ 初始化 Snowflake++
	// ================================
	regionID := int64(1) // 你可以配置化
	sf := generator.New(regionID, workerID)

	// ================================
	// 4️⃣ 初始化 DB（segment）
	// ================================
	db, err := NewDB()
	if err != nil {
		log.Fatal("failed to connect to database:", err)
	}
	// ================================
	// 5️⃣ 初始化 segment buffer
	// ================================
	buf := &segment.Buffer{
		DB: db,
	}
	buf.Load() // 预加载第一个 segment

	// ================================
	// 6️⃣ 启动 gRPC 服务
	// ================================
	lis, err := net.Listen("tcp", ":9090")
	if err != nil {
		log.Fatal(err)
	}

	grpcServer := grpc.NewServer()

	pb.RegisterIDServiceServer(grpcServer, &service.Server{
		Sf:  sf,
		Seg: buf,
	})

	log.Println("gRPC server started at :9090")

	if err := grpcServer.Serve(lis); err != nil {
		log.Fatal(err)
	}
}

func NewDB() (*sql.DB, error) {
	dsn := "root:password@tcp(mysql:3318)/id_db?charset=utf8mb4&parseTime=true&loc=Local&allowPublicKeyRetrieval=true"

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}

	// ✅ 必须 Ping（sql.Open 不会真正连接）
	if err := db.Ping(); err != nil {
		return nil, err
	}

	// ✅ 连接池（非常重要）
	db.SetMaxOpenConns(50)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(1 * time.Hour)

	return db, nil
}
