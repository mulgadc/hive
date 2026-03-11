import { describe, expect, it } from "vitest"

import { createPolicySchema, createUserSchema } from "./iam"

describe("createUserSchema", () => {
  it("accepts a valid user name", () => {
    const result = createUserSchema.safeParse({ userName: "admin" })
    expect(result.success).toBe(true)
  })

  it("rejects empty user name", () => {
    const result = createUserSchema.safeParse({ userName: "" })
    expect(result.success).toBe(false)
  })

  it("rejects user name over 64 chars", () => {
    const result = createUserSchema.safeParse({ userName: "a".repeat(65) })
    expect(result.success).toBe(false)
  })

  it("rejects user name with invalid characters", () => {
    const result = createUserSchema.safeParse({ userName: "user name!" })
    expect(result.success).toBe(false)
  })

  it("accepts user name with allowed special characters", () => {
    const result = createUserSchema.safeParse({ userName: "user+=,.@-test" })
    expect(result.success).toBe(true)
  })

  it("allows optional path", () => {
    const result = createUserSchema.safeParse({
      userName: "admin",
      path: "/engineering/",
    })
    expect(result.success).toBe(true)
  })
})

describe("createPolicySchema", () => {
  it("accepts valid policy params", () => {
    const result = createPolicySchema.safeParse({
      policyName: "ReadOnly",
      policyDocument: '{"Version":"2012-10-17","Statement":[]}',
    })
    expect(result.success).toBe(true)
  })

  it("rejects empty policy name", () => {
    const result = createPolicySchema.safeParse({
      policyName: "",
      policyDocument: "{}",
    })
    expect(result.success).toBe(false)
  })

  it("rejects policy name over 128 chars", () => {
    const result = createPolicySchema.safeParse({
      policyName: "a".repeat(129),
      policyDocument: "{}",
    })
    expect(result.success).toBe(false)
  })

  it("rejects invalid JSON in policy document", () => {
    const result = createPolicySchema.safeParse({
      policyName: "ReadOnly",
      policyDocument: "not json",
    })
    expect(result.success).toBe(false)
  })

  it("rejects empty policy document", () => {
    const result = createPolicySchema.safeParse({
      policyName: "ReadOnly",
      policyDocument: "",
    })
    expect(result.success).toBe(false)
  })

  it("allows optional description", () => {
    const result = createPolicySchema.safeParse({
      policyName: "ReadOnly",
      description: "Read-only access",
      policyDocument: "{}",
    })
    expect(result.success).toBe(true)
  })
})
