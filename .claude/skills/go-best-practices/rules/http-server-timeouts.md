---
title: Configure HTTP Server Timeouts
impact: CRITICAL
impactDescription: prevents Slowloris attacks and resource exhaustion (gosec G112)
tags: go, http, security, gosec, server, timeouts
---

## Configure HTTP Server Timeouts

Every `http.Server` must have `ReadHeaderTimeout` configured at minimum. Omitting timeouts allows Slowloris-style attacks where an attacker opens connections and sends headers slowly, exhausting server resources.

**Incorrect (no timeouts — gosec G112):**

```go
server := &http.Server{
    Addr:    ":8080",
    Handler: mux,
}
```

This server has no timeout protection. A malicious client can hold connections open indefinitely.

**Correct (all timeouts configured):**

```go
server := &http.Server{
    Addr:              ":8080",
    Handler:           mux,
    ReadTimeout:       10 * time.Second,
    WriteTimeout:      10 * time.Second,
    IdleTimeout:       30 * time.Second,
    ReadHeaderTimeout: 5 * time.Second,
}
```

**Timeout guidelines:**

| Timeout | Purpose | Typical Value |
|---------|---------|---------------|
| `ReadHeaderTimeout` | Time to read request headers (Slowloris protection) | 5s |
| `ReadTimeout` | Time to read entire request including body | 10-30s |
| `WriteTimeout` | Time to write the response | 10-30s |
| `IdleTimeout` | Time to keep idle keep-alive connections open | 30-120s |

**Adjust for your use case:**
- Short-lived internal APIs (formation, health): 10s read/write, 30s idle
- User-facing APIs with uploads: 30s+ read, 15s write
- Long-polling endpoints: increase WriteTimeout accordingly

**gosec rule:** G112 (CWE-400) — Potential Slowloris Attack
