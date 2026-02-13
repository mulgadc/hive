---
title: Correct Mutex Usage Patterns
impact: HIGH
impactDescription: prevents data races, deadlocks, and race condition bugs
tags: go, concurrency, mutex, sync, data-race
---

## Correct Mutex Usage Patterns

Use mutexes correctly to protect shared state. Common mistakes include forgetting to unlock, holding locks across blocking operations, and using the wrong lock type.

**Incorrect (common mutex mistakes):**

```go
// Forgetting to unlock on error paths
func (s *Store) Get(key string) (string, error) {
    s.mu.Lock()
    val, ok := s.data[key]
    if !ok {
        return "", ErrNotFound  // mutex never unlocked!
    }
    s.mu.Unlock()
    return val, nil
}

// Holding lock across blocking operation
func (s *Store) Refresh() {
    s.mu.Lock()
    defer s.mu.Unlock()
    data, _ := http.Get(s.url)  // blocks while holding lock!
    s.data = parse(data)
}

// Using RWMutex wrong — write lock where read lock suffices
func (s *Store) Count() int {
    s.mu.Lock()  // should be RLock for read-only access
    defer s.mu.Unlock()
    return len(s.data)
}
```

**Correct:**

```go
// Always use defer Unlock
func (s *Store) Get(key string) (string, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    val, ok := s.data[key]
    if !ok {
        return "", ErrNotFound
    }
    return val, nil
}

// Don't hold lock across blocking ops
func (s *Store) Refresh() {
    data, err := http.Get(s.url)  // fetch without lock
    if err != nil {
        return
    }
    parsed := parse(data)

    s.mu.Lock()
    s.data = parsed  // only hold lock for assignment
    s.mu.Unlock()
}

// Use RLock for read-only access
func (s *Store) Count() int {
    s.mu.RLock()
    defer s.mu.RUnlock()
    return len(s.data)
}
```

**Key points:**
- Always use `defer mu.Unlock()` immediately after `mu.Lock()` to prevent missed unlocks
- Use `sync.RWMutex` when reads vastly outnumber writes — `RLock()` for reads, `Lock()` for writes
- Never hold a lock while doing I/O, network calls, or channel operations
- Keep critical sections as small as possible
- Document which fields a mutex protects with a comment above the mutex field
