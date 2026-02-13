---
title: Use log/slog for Structured Logging
impact: LOW-MEDIUM
impactDescription: enables consistent, queryable structured logs across the codebase
tags: go, logging, slog, structured
---

## Use log/slog for Structured Logging

Use `log/slog` instead of `log` or `fmt.Println` for all logging. Use appropriate log levels and structured key-value pairs.

**Incorrect:**

```go
import "log"

log.Printf("failed to connect to %s: %v", addr, err)
fmt.Println("starting server on", addr)
log.Fatal("database connection failed")
```

**Correct:**

```go
import "log/slog"

slog.Error("Failed to connect", "addr", addr, "error", err)
slog.Info("Server starting", "addr", addr)
slog.Debug("Cache hit", "key", key, "ttl", ttl)
```

**Log levels:**
- `slog.Debug` — verbose diagnostic info, disabled in production
- `slog.Info` — normal operational events (startup, shutdown, config loaded)
- `slog.Warn` — unexpected but recoverable situations
- `slog.Error` — failures that need attention (lost connections, failed operations)

**Key points:**
- Key-value pairs must alternate: key (string), value (any)
- Keep keys lowercase, snake_case: `"node_id"`, `"bind_ip"`, `"error"`
- Always include `"error", err` when logging an error
- Don't log and return the same error — pick one (usually return)
