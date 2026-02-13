---
title: Use defer for Cleanup
impact: MEDIUM
impactDescription: ensures resources are released on all exit paths including panics
tags: go, defer, cleanup, resources
---

## Use defer for Cleanup

Use `defer` to ensure cleanup code runs on all function exit paths, including early returns and panics. Place the defer immediately after acquiring the resource.

**Incorrect (cleanup missed on error paths):**

```go
func processFile(path string) error {
    f, err := os.Open(path)
    if err != nil {
        return err
    }

    data, err := io.ReadAll(f)
    if err != nil {
        return err  // f is never closed!
    }

    f.Close()
    return process(data)
}
```

**Correct (defer immediately after acquire):**

```go
func processFile(path string) error {
    f, err := os.Open(path)
    if err != nil {
        return fmt.Errorf("open %s: %w", path, err)
    }
    defer f.Close()

    data, err := io.ReadAll(f)
    if err != nil {
        return fmt.Errorf("read %s: %w", path, err)
    }

    return process(data)
}
```

**Key points:**
- Place `defer` on the line immediately after the error check for the acquire operation
- Defers execute in LIFO order — last deferred runs first
- For mutexes: `mu.Lock()` then `defer mu.Unlock()` on the next line
- For write operations where you need the close error, use a named return
