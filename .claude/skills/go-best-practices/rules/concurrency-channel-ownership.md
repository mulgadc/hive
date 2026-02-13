---
title: Clear Channel Ownership and Closing
impact: HIGH
impactDescription: prevents panics from double-close and sends on closed channels
tags: go, concurrency, channels, ownership
---

## Clear Channel Ownership and Closing

Follow clear ownership rules for channels: only the sender (producer) should close a channel, never the receiver. Closing a channel from the wrong goroutine causes panics.

**Incorrect (receiver closes, or double close):**

```go
func producer(ch chan<- int) {
    for i := 0; i < 10; i++ {
        ch <- i
    }
    // forgot to close — receiver blocks forever
}

func consumer(ch <-chan int, done chan struct{}) {
    for val := range ch {
        process(val)
    }
    close(done)
    close(done)  // panic: close of closed channel
}
```

**Correct (sender closes, clear ownership):**

```go
func producer(ch chan<- int) {
    defer close(ch)  // sender closes when done
    for i := 0; i < 10; i++ {
        ch <- i
    }
}

func consumer(ch <-chan int) {
    for val := range ch {
        process(val)
    }
    // channel was closed by producer, range exits naturally
}
```

**Key points:**
- The goroutine that creates/sends on a channel should close it
- Use `defer close(ch)` at the top of the sending function
- Use directional channel types (`chan<-`, `<-chan`) in function signatures to enforce ownership
- For signaling (done channels), use `chan struct{}` and close it once — don't send values
- Use `sync.Once` if multiple goroutines might try to close the same channel
