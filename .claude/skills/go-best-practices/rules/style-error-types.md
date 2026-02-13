---
title: Use awserrors Package for AWS Error Responses
impact: LOW-MEDIUM
impactDescription: ensures consistent AWS-compatible error responses across all handlers
tags: go, aws, errors, handlers, compatibility
---

## Use awserrors Package for AWS Error Responses

When returning AWS-compatible error responses, use the `awserrors` package instead of manually constructing error strings or XML. This ensures consistent error formatting across all service handlers.

**Incorrect (manual error strings):**

```go
func handleDescribeInstances(w http.ResponseWriter, r *http.Request) {
    // ...
    if err != nil {
        w.WriteHeader(http.StatusInternalServerError)
        fmt.Fprintf(w, `<Error><Code>InternalError</Code><Message>%s</Message></Error>`, err)
        return
    }
}
```

**Correct (awserrors package):**

```go
func handleDescribeInstances(w http.ResponseWriter, r *http.Request) {
    // ...
    if err != nil {
        awserrors.WriteError(w, awserrors.InternalError(err.Error()))
        return
    }
}
```

**Key points:**
- The `awserrors` package handles XML formatting, status codes, and AWS error code conventions
- Use predefined error constructors where available
- This ensures AWS SDK clients can parse error responses correctly
