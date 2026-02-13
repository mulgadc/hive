---
title: Always Check Returned Errors
impact: HIGH
impactDescription: prevents silent failures and data corruption (gosec G104)
tags: go, errors, gosec, reliability
---

## Always Check Returned Errors

Never ignore error return values. Every function that returns an error must have that error checked. This is the single most common source of bugs in Go code.

**Incorrect (errors discarded):**

```go
file, _ := os.Open(path)           // crash if file doesn't exist
json.Unmarshal(data, &config)       // silent corruption if data is invalid
db.Exec("DELETE FROM users WHERE id = ?", id)  // silent failure
conn.Close()                        // resource leak on error
```

**Correct (errors handled):**

```go
file, err := os.Open(path)
if err != nil {
    return fmt.Errorf("open config: %w", err)
}

if err := json.Unmarshal(data, &config); err != nil {
    return fmt.Errorf("parse config: %w", err)
}

if _, err := db.Exec("DELETE FROM users WHERE id = ?", id); err != nil {
    return fmt.Errorf("delete user %s: %w", id, err)
}

if err := conn.Close(); err != nil {
    slog.Error("Failed to close connection", "error", err)
}
```

**Acceptable exceptions:**

```go
// In deferred cleanup where you can't return the error
defer func() {
    if err := resp.Body.Close(); err != nil {
        slog.Debug("Failed to close response body", "error", err)
    }
}()

// fmt.Fprintf to a bytes.Buffer never fails — but document why
_, _ = fmt.Fprintf(&buf, "value: %d", n)  // bytes.Buffer.Write never returns error
```

**Key points:**
- Use `_ =` explicitly when you intentionally discard an error, with a comment explaining why
- Log errors in cleanup paths (defer) where you can't propagate them
- Wrap errors with `%w` to preserve the error chain for callers

**gosec rule:** G104 (CWE-703) — Errors unhandled
