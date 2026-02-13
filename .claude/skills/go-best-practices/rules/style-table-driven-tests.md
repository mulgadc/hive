---
title: Use Table-Driven Tests
impact: LOW-MEDIUM
impactDescription: improves test coverage, readability, and makes it easy to add new cases
tags: go, testing, table-driven, subtests
---

## Use Table-Driven Tests

Use table-driven tests with `t.Run` subtests for functions with multiple input/output cases. This makes tests easy to extend and provides clear failure messages.

**Incorrect (repetitive test functions):**

```go
func TestValidatePort_Valid(t *testing.T) {
    if err := ValidatePort(8080); err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
}

func TestValidatePort_Zero(t *testing.T) {
    if err := ValidatePort(0); err == nil {
        t.Fatal("expected error for port 0")
    }
}

func TestValidatePort_Negative(t *testing.T) {
    if err := ValidatePort(-1); err == nil {
        t.Fatal("expected error for port -1")
    }
}
```

**Correct (table-driven):**

```go
func TestValidatePort(t *testing.T) {
    tests := []struct {
        name    string
        port    int
        wantErr bool
    }{
        {"valid port", 8080, false},
        {"min valid", 1, false},
        {"max valid", 65535, false},
        {"zero", 0, true},
        {"negative", -1, true},
        {"too high", 65536, true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := ValidatePort(tt.port)
            if (err != nil) != tt.wantErr {
                t.Errorf("ValidatePort(%d) error = %v, wantErr %v", tt.port, err, tt.wantErr)
            }
        })
    }
}
```

**Key points:**
- Name each test case descriptively — it appears in test output on failure
- Use `t.Run` for subtests so individual cases can be run with `-run`
- Keep test struct fields minimal — only what varies between cases
- For complex expected outputs, add a `want` field with the expected value
- Use `t.Parallel()` in subtests when cases are independent
