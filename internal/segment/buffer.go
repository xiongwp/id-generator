package segment

import (
	"database/sql"
	"sync"
)

type Buffer struct {
	mu      sync.Mutex
	current int64
	max     int64
	next    *Buffer
	DB      *sql.DB
}

func (b *Buffer) Next() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.current >= b.max {
		if b.next != nil {
			*b = *b.next
			go b.Load()
		} else {
			return -1
		}
	}

	b.current++
	return b.current
}

func (b *Buffer) Load() {
	start, end, _ := Fetch(b.DB)

	b.next = &Buffer{
		current: start,
		max:     end,
		DB:      b.DB,
	}
}
