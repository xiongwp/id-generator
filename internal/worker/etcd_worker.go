package worker

import (
	"context"
	"fmt"
	"os"
	"strconv"

	clientv3 "go.etcd.io/etcd/client/v3"
)

func Register(cli *clientv3.Client) int64 {
	for i := 0; i < 1024; i++ {
		key := fmt.Sprintf("/snowflake/%d", i)

		lease, _ := cli.Grant(context.Background(), 10)

		resp, _ := cli.Txn(context.Background()).
			If(clientv3.Compare(clientv3.CreateRevision(key), "=", 0)).
			Then(clientv3.OpPut(key, "1", clientv3.WithLease(lease.ID))).
			Commit()

		if resp.Succeeded {
			go cli.KeepAlive(context.Background(), lease.ID)
			os.WriteFile("/tmp/worker", []byte(fmt.Sprint(i)), 0644)
			return int64(i)
		}
	}

	// fallback
	data, _ := os.ReadFile("/tmp/worker")
	id, _ := strconv.ParseInt(string(data), 10, 64)
	return id
}
