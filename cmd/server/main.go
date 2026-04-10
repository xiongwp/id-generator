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
	dsn := "root:root@tcp(mysql:3318)/idgen?charset=utf8mb4&parseTime=True&loc=Local"

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatal("mysql connect failed:", err)
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)

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
