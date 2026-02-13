---
title: Wrap Errors with Context
impact: HIGH
impactDescription: enables debugging by preserving error chains and adding call context
tags: go, errors, debugging, fmt
---

## Wrap Errors with Context

When returning an error from a called function, wrap it with `fmt.Errorf` and `%w` to add context about what operation failed. Bare error returns make debugging difficult because you lose the call chain.

**Incorrect (bare error returns):**

```go
func LoadConfig(path string) (*Config, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, err  // caller gets "open /etc/config.yaml: no such file or directory"
    }

    var cfg Config
    if err := json.Unmarshal(data, &cfg); err != nil {
        return nil, err  // caller gets "invalid character '}'" — no idea what file
    }

    return &cfg, nil
}
```

**Correct (wrapped with context):**

```go
func LoadConfig(path string) (*Config, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, fmt.Errorf("read config %s: %w", path, err)
    }

    var cfg Config
    if err := json.Unmarshal(data, &cfg); err != nil {
        return nil, fmt.Errorf("parse config %s: %w", path, err)
    }

    return &cfg, nil
}
```

**Key points:**
- Use `%w` (not `%v`) to wrap errors — this preserves `errors.Is()` and `errors.As()` checks
- Add the operation name and key parameters (file path, ID, etc.)
- Keep wrap messages lowercase, no trailing punctuation (Go convention)
- Don't prefix with "failed to" or "error" — the error itself communicates failure
