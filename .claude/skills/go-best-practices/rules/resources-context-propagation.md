---
title: Propagate and Respect context.Context
impact: MEDIUM
impactDescription: enables cancellation, timeouts, and graceful shutdown across the call chain
tags: go, context, cancellation, timeout, graceful-shutdown
---

## Propagate and Respect context.Context

Pass `context.Context` as the first parameter to functions that do I/O, run goroutines, or may need cancellation. Never store contexts in structs — they represent a single request lifecycle.

**Incorrect (context ignored or stored):**

```go
type Service struct {
    ctx context.Context  // don't store context in structs
}

func (s *Service) FetchData() ([]byte, error) {
    resp, err := http.Get(s.url)  // no context — can't cancel
    return io.ReadAll(resp.Body)
}
```

**Correct (context flows through the call chain):**

```go
func (s *Service) FetchData(ctx context.Context) ([]byte, error) {
    req, err := http.NewRequestWithContext(ctx, "GET", s.url, nil)
    if err != nil {
        return nil, fmt.Errorf("create request: %w", err)
    }

    resp, err := s.client.Do(req)
    if err != nil {
        return nil, fmt.Errorf("fetch data: %w", err)
    }
    defer resp.Body.Close()

    return io.ReadAll(resp.Body)
}
```

**Key points:**
- `context.Context` is always the first parameter, named `ctx`
- Use `context.WithTimeout` or `context.WithCancel` to set deadlines
- Check `ctx.Err()` or `select` on `ctx.Done()` in long-running loops
- Use `http.NewRequestWithContext` instead of `http.Get`/`http.Post`
- In tests, use `context.Background()` or `t.Context()` (Go 1.24+)
