---
name: go-best-practices
description: Go best practices and security guidelines. This skill should be used when writing, reviewing, or refactoring Go code to ensure correct error handling, secure HTTP servers, proper concurrency patterns, and idiomatic Go. Triggers on tasks involving Go backend code, HTTP handlers, goroutines, or system-level programming.
---

# Go Best Practices

Comprehensive best practices for writing secure, correct, and idiomatic Go code. Contains rules prioritized by impact, covering security (gosec compliance), error handling, HTTP servers, concurrency, and common pitfalls. All rules are enforced by `make security` (go vet, gosec, govulncheck, staticcheck).

## When to Apply

Reference these guidelines when:
- Writing new Go code (handlers, services, packages)
- Creating or modifying HTTP servers
- Working with goroutines, channels, or mutexes
- Writing functions that return errors
- Reviewing Go code for correctness and security
- Implementing AWS-compatible service handlers

## Rule Categories by Priority

| Priority | Category | Impact | Prefix |
|----------|----------|--------|--------|
| 1 | HTTP Server Security | CRITICAL | `http-` |
| 2 | Error Handling | HIGH | `errors-` |
| 3 | Concurrency | HIGH | `concurrency-` |
| 4 | Resource Management | MEDIUM | `resources-` |
| 5 | Code Style & Idioms | LOW-MEDIUM | `style-` |

## Quick Reference

### 1. HTTP Server Security (CRITICAL)

- `http-server-timeouts` - Always configure all timeouts on http.Server
- `http-handle-write-errors` - Always handle errors from ResponseWriter.Write and JSON encoding
- `http-input-validation` - Validate and limit request bodies, never trust client input

### 2. Error Handling (HIGH)

- `errors-always-check` - Never ignore returned errors (gosec G104)
- `errors-wrap-context` - Wrap errors with fmt.Errorf and %w for context
- `errors-sentinel` - Use sentinel errors and errors.Is/errors.As for comparison

### 3. Concurrency (HIGH)

- `concurrency-goroutine-leaks` - Ensure goroutines can always exit
- `concurrency-mutex-patterns` - Correct mutex usage and defer unlock
- `concurrency-channel-ownership` - Clear channel ownership and closing conventions

### 4. Resource Management (MEDIUM)

- `resources-close-bodies` - Always close http response bodies and file handles
- `resources-context-propagation` - Pass and respect context.Context for cancellation
- `resources-defer-cleanup` - Use defer for cleanup to ensure execution on all paths

### 5. Code Style & Idioms (LOW-MEDIUM)

- `style-slog-logging` - Use log/slog instead of log, with appropriate levels
- `style-error-types` - Use awserrors package for AWS error responses
- `style-table-driven-tests` - Use table-driven tests with subtests

## How to Use

Read individual rule files for detailed explanations and code examples:

```
rules/http-server-timeouts.md
rules/errors-always-check.md
```

Each rule file contains:
- Brief explanation of why it matters
- Incorrect code example with explanation
- Correct code example with explanation
- gosec/vet rule ID where applicable
