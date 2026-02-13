---
title: Close Response Bodies and File Handles
impact: MEDIUM
impactDescription: prevents resource leaks that exhaust file descriptors and connections
tags: go, resources, http, files, defer, leak
---

## Close Response Bodies and File Handles

Always close `http.Response.Body`, files, and other `io.Closer` resources. Failing to close HTTP response bodies leaks TCP connections; failing to close files leaks file descriptors.

**Incorrect (response body not closed):**

```go
func fetchData(url string) ([]byte, error) {
    resp, err := http.Get(url)
    if err != nil {
        return nil, err
    }
    // resp.Body is never closed — leaks TCP connection
    return io.ReadAll(resp.Body)
}
```

**Correct (defer close immediately after error check):**

```go
func fetchData(url string) ([]byte, error) {
    resp, err := http.Get(url)
    if err != nil {
        return nil, fmt.Errorf("fetch %s: %w", url, err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("fetch %s: status %d", url, resp.StatusCode)
    }

    return io.ReadAll(resp.Body)
}
```

**Key points:**
- Place `defer Close()` immediately after the nil-error check, before any other logic
- For files: `defer f.Close()` right after `os.Open`/`os.Create`
- The body must be fully read and closed even for non-2xx responses — otherwise the connection can't be reused
- If you need the close error (e.g., for writes): use a named return and check in defer
