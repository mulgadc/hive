---
title: Handle HTTP Response Write Errors
impact: CRITICAL
impactDescription: prevents silent failures and satisfies gosec G104
tags: go, http, error-handling, gosec, handlers
---

## Handle HTTP Response Write Errors

Always check errors returned by `http.ResponseWriter.Write()`, `io.Writer.Write()`, `json.Encoder.Encode()`, and `fmt.Fprintf(w, ...)` in HTTP handlers. Ignoring these errors means you won't know when clients disconnect or responses fail to send.

**Incorrect (errors ignored — gosec G104):**

```go
func handleHealth(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
    w.Write([]byte("ok"))  // error ignored
}

func writeJSON(w http.ResponseWriter, status int, v any) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    json.NewEncoder(w).Encode(v)  // error ignored
}
```

**Correct (errors handled):**

```go
func handleHealth(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
    if _, err := w.Write([]byte("ok")); err != nil {
        slog.Error("Failed to write health response", "error", err)
    }
}

func writeJSON(w http.ResponseWriter, status int, v any) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    if err := json.NewEncoder(w).Encode(v); err != nil {
        slog.Error("Failed to encode JSON response", "error", err)
    }
}
```

**Key points:**
- Log write errors at error level — they indicate client disconnects or network issues
- Don't try to write an HTTP error response after a write failure (the connection is likely broken)
- This applies to all `io.Writer` methods, not just `http.ResponseWriter`
- `fmt.Fprintf(w, ...)` also returns an error that must be checked

**gosec rule:** G104 (CWE-703) — Errors unhandled
