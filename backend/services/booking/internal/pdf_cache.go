package booking

import (
	"container/list"
	"context"
	"sync"
	"time"

	"banya.local/backend/internal/platform"
)

type pdfCacheEntry struct {
	key   string
	data  []byte
	until time.Time
}
type pdfCache struct {
	mu            sync.Mutex
	entries       map[string]*list.Element
	lru           list.List
	bytes, budget int
	slots         chan struct{}
}

func newPDFCache(budgetMiB, concurrent int) *pdfCache {
	return &pdfCache{entries: map[string]*list.Element{}, budget: platform.Positive(budgetMiB, 16) << 20, slots: make(chan struct{}, platform.Positive(concurrent, 2))}
}
func (c *pdfCache) get(key string, now time.Time) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.entries[key]; e != nil {
		v := e.Value.(pdfCacheEntry)
		if now.Before(v.until) {
			c.lru.MoveToFront(e)
			return v.data
		}
		c.remove(e)
	}
	return nil
}
func (c *pdfCache) remove(e *list.Element) {
	v := e.Value.(pdfCacheEntry)
	delete(c.entries, v.key)
	c.bytes -= len(v.data)
	c.lru.Remove(e)
}
func (c *pdfCache) put(key string, data []byte) {
	if len(data) > c.budget {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.entries[key]; e != nil {
		c.remove(e)
	}
	for c.bytes+len(data) > c.budget || len(c.entries) >= 1000 {
		c.remove(c.lru.Back())
	}
	c.entries[key] = c.lru.PushFront(pdfCacheEntry{key: key, data: data, until: time.Now().Add(5 * time.Minute)})
	c.bytes += len(data)
}
func (s *Service) cachedPDF(ctx context.Context, t Ticket) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.pdfOnce.Do(func() { s.pdfs = newPDFCache(s.Config.PDFCacheMiB, s.Config.PDFMaxConcurrent) })
	cache := s.pdfs
	if data := cache.get(t.OrderID, time.Now()); data != nil {
		return data, nil
	}
	select {
	case cache.slots <- struct{}{}:
		defer func() { <-cache.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return nil, platform.Err(503, "pdf_busy", "Генерация билетов занята; повторите запрос")
	}
	// A preceding request may have filled the cache while this one was authenticating.
	if data := cache.get(t.OrderID, time.Now()); data != nil {
		return data, nil
	}
	data, err := ticketPDFContext(ctx, t)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	cache.put(t.OrderID, data)
	return data, nil
}
