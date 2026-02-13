---
title: Validate and Limit HTTP Request Input
impact: CRITICAL
impactDescription: prevents denial of service via oversized payloads and injection attacks
tags: go, http, security, validation, input
---

## Validate and Limit HTTP Request Input

Always limit request body size and validate input in HTTP handlers. An unbounded `json.NewDecoder(r.Body).Decode()` allows attackers to send arbitrarily large payloads, exhausting server memory.

**Incorrect (unbounded request body):**

```go
func handleCreate(w http.ResponseWriter, r *http.Request) {
    var req CreateRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, "bad request", http.StatusBadRequest)
        return
    }
    // process req...
}
```

**Correct (limited and validated):**

```go
const maxRequestBodySize = 1 << 20 // 1 MB

func handleCreate(w http.ResponseWriter, r *http.Request) {
    r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)

    var req CreateRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, "bad request", http.StatusBadRequest)
        return
    }

    // Validate required fields
    if req.Name == "" {
        http.Error(w, "name is required", http.StatusBadRequest)
        return
    }
    // process req...
}
```

**Key points:**
- Use `http.MaxBytesReader` to limit body size — it returns a 413 error if exceeded
- Validate all required fields after decoding
- Sanitize string inputs if they'll be used in commands, queries, or file paths
- Use early returns for validation failures
