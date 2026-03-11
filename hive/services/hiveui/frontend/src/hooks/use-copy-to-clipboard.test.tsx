import { act, renderHook } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { useCopyToClipboard } from "./use-copy-to-clipboard"

beforeEach(() => {
  vi.useFakeTimers()
  Object.assign(navigator, {
    clipboard: { writeText: vi.fn().mockResolvedValue(undefined) },
  })
})

afterEach(() => {
  vi.useRealTimers()
})

describe("useCopyToClipboard", () => {
  it("starts with copied as false", () => {
    const { result } = renderHook(() => useCopyToClipboard())
    expect(result.current.copied).toBe(false)
  })

  it("sets copied to true after calling copy", async () => {
    const { result } = renderHook(() => useCopyToClipboard())

    await act(async () => {
      await result.current.copy("hello")
    })

    expect(navigator.clipboard.writeText).toHaveBeenCalledWith("hello")
    expect(result.current.copied).toBe(true)
  })

  it("resets copied to false after 2 seconds", async () => {
    const { result } = renderHook(() => useCopyToClipboard())

    await act(async () => {
      await result.current.copy("hello")
    })
    expect(result.current.copied).toBe(true)

    act(() => {
      vi.advanceTimersByTime(2000)
    })
    expect(result.current.copied).toBe(false)
  })

  it("resets the timer on rapid successive copies", async () => {
    const { result } = renderHook(() => useCopyToClipboard())

    await act(async () => {
      await result.current.copy("first")
    })

    // Advance 1.5s, then copy again — should restart the 2s timer
    act(() => {
      vi.advanceTimersByTime(1500)
    })
    expect(result.current.copied).toBe(true)

    await act(async () => {
      await result.current.copy("second")
    })

    // 1.5s after second copy — still within the new 2s window
    act(() => {
      vi.advanceTimersByTime(1500)
    })
    expect(result.current.copied).toBe(true)

    // 2s after second copy — should reset
    act(() => {
      vi.advanceTimersByTime(500)
    })
    expect(result.current.copied).toBe(false)
  })

  it("clears the timer on unmount", async () => {
    const { result, unmount } = renderHook(() => useCopyToClipboard())

    await act(async () => {
      await result.current.copy("hello")
    })

    unmount()

    // Advancing time after unmount should not throw
    act(() => {
      vi.advanceTimersByTime(2000)
    })
  })
})
