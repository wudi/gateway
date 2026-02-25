package cache

// StoreStats contains storage-level statistics.
type StoreStats struct {
	Size      int   `json:"size"`
	MaxSize   int   `json:"max_size"`   // 0 if N/A (e.g., Redis)
	Evictions int64 `json:"evictions"`  // 0 if not tracked (e.g., Redis)
}

// Store abstracts the cache storage backend.
type Store interface {
	Get(key string) (*Entry, bool)
	Set(key string, entry *Entry)
	Delete(key string)
	DeleteByPrefix(prefix string)
	DeleteByTags(tags []string) int
	Purge()
	Stats() StoreStats
}
