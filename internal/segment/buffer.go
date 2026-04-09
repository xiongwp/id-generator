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
	db      *sql.DB
}

func (b *Buffer) Next() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.current >= b.max {
		if b.next != nil {
			*b = *b.next
			go b.load()
		} else {
			return -1
		}
	}

	b.current++
	return b.current
}

func (b *Buffer) load() {
	start, end, _ := Fetch(b.db)

	b.next = &Buffer{
		current: start,
		max:     end,
		db:      b.db,
	}
}
