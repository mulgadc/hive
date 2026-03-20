import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import type { AwsCredentials } from "./auth"

const validCreds: AwsCredentials = {
  accessKeyId: "AKIAIOSFODNN7EXAMPLE",
  secretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
}

let getCredentials: typeof import("./auth").getCredentials
let setCredentials: typeof import("./auth").setCredentials
let clearCredentials: typeof import("./auth").clearCredentials

beforeEach(async () => {
  // Reset module to get a fresh in-memory cache for each test
  vi.resetModules()
  const auth = await import("./auth")
  getCredentials = auth.getCredentials
  setCredentials = auth.setCredentials
  clearCredentials = auth.clearCredentials
})

afterEach(() => {
  localStorage.clear()
})

describe("setCredentials", () => {
  it("stores credentials in localStorage", () => {
    setCredentials(validCreds)
    const stored = localStorage.getItem("spinifex:v1:aws-credentials")
    expect(JSON.parse(stored ?? "")).toEqual(validCreds)
  })

  it("caches credentials in memory", () => {
    setCredentials(validCreds)
    // Clear localStorage to prove it reads from cache
    localStorage.clear()
    expect(getCredentials()).toEqual(validCreds)
  })
})

describe("getCredentials", () => {
  it("returns null when nothing is stored", () => {
    expect(getCredentials()).toBeNull()
  })

  it("reads from localStorage on first call", () => {
    localStorage.setItem(
      "spinifex:v1:aws-credentials",
      JSON.stringify(validCreds),
    )
    expect(getCredentials()).toEqual(validCreds)
  })

  it("returns cached value on subsequent calls", () => {
    setCredentials(validCreds)
    localStorage.clear()
    // Should still return from cache
    expect(getCredentials()).toEqual(validCreds)
    expect(getCredentials()).toEqual(validCreds)
  })

  it("returns null for invalid JSON in localStorage", () => {
    localStorage.setItem("spinifex:v1:aws-credentials", "not-json")
    expect(getCredentials()).toBeNull()
  })

  it("returns null when stored data fails schema validation", () => {
    localStorage.setItem(
      "spinifex:v1:aws-credentials",
      JSON.stringify({ accessKeyId: "short", secretAccessKey: "" }),
    )
    expect(getCredentials()).toBeNull()
  })

  it("returns null when stored object is missing fields", () => {
    localStorage.setItem(
      "spinifex:v1:aws-credentials",
      JSON.stringify({ accessKeyId: "AKIAIOSFODNN7EXAMPLE" }),
    )
    expect(getCredentials()).toBeNull()
  })
})

describe("clearCredentials", () => {
  it("removes credentials from localStorage", () => {
    setCredentials(validCreds)
    clearCredentials()
    expect(localStorage.getItem("spinifex:v1:aws-credentials")).toBeNull()
  })

  it("clears the in-memory cache", () => {
    setCredentials(validCreds)
    clearCredentials()
    expect(getCredentials()).toBeNull()
  })
})
