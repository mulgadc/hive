---
title: Prevent Goroutine Leaks
impact: HIGH
impactDescription: prevents memory leaks and resource exhaustion from orphaned goroutines
tags: go, concurrency, goroutines, context, leaks
---

## Prevent Goroutine Leaks

Every goroutine must have a clear exit path. Goroutines that block forever on channels or I/O without a cancellation mechanism leak memory and resources.

**Incorrect (goroutine can never exit):**

```go
func startWorker(jobs <-chan Job) {
    go func() {
        for job := range jobs {
            process(job)
        }
    }()
    // If no one closes jobs channel and no context, this goroutine lives forever
}

func pollStatus(url string, interval time.Duration) {
    go func() {
        for {
            resp, _ := http.Get(url)
            // runs forever, no way to stop
            time.Sleep(interval)
        }
    }()
}
```

**Correct (goroutine exits via context or done channel):**

```go
func startWorker(ctx context.Context, jobs <-chan Job) {
    go func() {
        for {
            select {
            case <-ctx.Done():
                return
            case job, ok := <-jobs:
                if !ok {
                    return
                }
                process(job)
            }
        }
    }()
}

func pollStatus(ctx context.Context, url string, interval time.Duration) {
    go func() {
        ticker := time.NewTicker(interval)
        defer ticker.Stop()
        for {
            select {
            case <-ctx.Done():
                return
            case <-ticker.C:
                // use context-aware HTTP request
                req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
                http.DefaultClient.Do(req)
            }
        }
    }()
}
```

**Key points:**
- Always pass `context.Context` to functions that spawn goroutines
- Use `select` with `ctx.Done()` to enable cancellation
- Use `time.NewTicker` (and defer `Stop()`) instead of `time.Sleep` in loops
- Close channels from the sender side to signal completion
- In server shutdown, use `server.Shutdown(ctx)` to drain connections gracefully
