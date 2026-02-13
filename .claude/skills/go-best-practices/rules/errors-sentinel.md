---
title: Use Sentinel Errors and errors.Is/As
impact: HIGH
impactDescription: enables correct error matching across wrapped error chains
tags: go, errors, sentinel, comparison
---

## Use Sentinel Errors and errors.Is/As

Never compare errors with `==`. Use `errors.Is()` for sentinel error comparison and `errors.As()` for type assertion. Direct comparison breaks when errors are wrapped.

**Incorrect (direct comparison):**

```go
if err == sql.ErrNoRows {
    return nil, nil  // breaks if err is wrapped
}

if err.Error() == "not found" {
    // fragile string comparison
}

switch err.(type) {
case *os.PathError:
    // breaks with wrapped errors
}
```

**Correct (errors.Is and errors.As):**

```go
if errors.Is(err, sql.ErrNoRows) {
    return nil, nil  // works even if wrapped
}

var pathErr *os.PathError
if errors.As(err, &pathErr) {
    slog.Error("Path error", "path", pathErr.Path, "op", pathErr.Op)
}
```

**Defining sentinel errors:**

```go
var (
    ErrNotFound     = errors.New("not found")
    ErrUnauthorized = errors.New("unauthorized")
)

// Usage
return fmt.Errorf("get volume %s: %w", id, ErrNotFound)

// Checking
if errors.Is(err, ErrNotFound) {
    w.WriteHeader(http.StatusNotFound)
}
```
