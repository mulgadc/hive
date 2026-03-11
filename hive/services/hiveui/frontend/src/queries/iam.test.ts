import { afterEach, describe, expect, it, vi } from "vitest"

const mockSend = vi.fn().mockResolvedValue({})

vi.mock("@/lib/awsClient", () => ({
  getIamClient: () => ({ send: mockSend }),
}))

import {
  iamAccessKeysQueryOptions,
  iamAttachedUserPoliciesQueryOptions,
  iamPoliciesQueryOptions,
  iamPolicyQueryOptions,
  iamPolicyVersionQueryOptions,
  iamUserQueryOptions,
  iamUsersQueryOptions,
} from "./iam"

describe("query keys", () => {
  it("iamUsersQueryOptions has correct key", () => {
    expect(iamUsersQueryOptions.queryKey).toEqual(["iam", "users"])
  })

  it("iamUserQueryOptions includes userName in key", () => {
    expect(iamUserQueryOptions("admin").queryKey).toEqual([
      "iam",
      "users",
      "admin",
    ])
  })

  it("iamAccessKeysQueryOptions includes userName in key", () => {
    expect(iamAccessKeysQueryOptions("admin").queryKey).toEqual([
      "iam",
      "access-keys",
      "admin",
    ])
  })

  it("iamPoliciesQueryOptions has correct key", () => {
    expect(iamPoliciesQueryOptions.queryKey).toEqual(["iam", "policies"])
  })

  it("iamPolicyQueryOptions includes policyArn in key", () => {
    expect(
      iamPolicyQueryOptions("arn:aws:iam::123:policy/ReadOnly").queryKey,
    ).toEqual(["iam", "policies", "arn:aws:iam::123:policy/ReadOnly"])
  })

  it("iamPolicyVersionQueryOptions includes policyArn and versionId in key", () => {
    expect(
      iamPolicyVersionQueryOptions("arn:aws:iam::123:policy/ReadOnly", "v1")
        .queryKey,
    ).toEqual([
      "iam",
      "policy-versions",
      "arn:aws:iam::123:policy/ReadOnly",
      "v1",
    ])
  })

  it("iamAttachedUserPoliciesQueryOptions includes userName in key", () => {
    expect(iamAttachedUserPoliciesQueryOptions("admin").queryKey).toEqual([
      "iam",
      "attached-user-policies",
      "admin",
    ])
  })
})

describe("staleTime", () => {
  it("users use staleTime", () => {
    expect(iamUsersQueryOptions.staleTime).toBe(300_000)
  })

  it("user uses staleTime", () => {
    expect(iamUserQueryOptions("admin").staleTime).toBe(300_000)
  })

  it("access keys use staleTime", () => {
    expect(iamAccessKeysQueryOptions("admin").staleTime).toBe(300_000)
  })

  it("policies use staleTime", () => {
    expect(iamPoliciesQueryOptions.staleTime).toBe(300_000)
  })

  it("policy uses staleTime", () => {
    expect(iamPolicyQueryOptions("arn:test").staleTime).toBe(300_000)
  })

  it("policy version uses staleTime", () => {
    expect(iamPolicyVersionQueryOptions("arn:test", "v1").staleTime).toBe(
      300_000,
    )
  })

  it("attached user policies use staleTime", () => {
    expect(iamAttachedUserPoliciesQueryOptions("admin").staleTime).toBe(
      300_000,
    )
  })
})

describe("queryFn", () => {
  afterEach(() => {
    mockSend.mockClear()
  })

  it("iamUsersQueryOptions sends ListUsersCommand", async () => {
    const queryFn = iamUsersQueryOptions.queryFn as (
      ctx: never,
    ) => Promise<unknown>
    await queryFn({} as never)
    expect(mockSend).toHaveBeenCalledOnce()
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({})
  })

  it("iamUserQueryOptions sends GetUserCommand with userName", async () => {
    const queryFn = iamUserQueryOptions("admin").queryFn as (
      ctx: never,
    ) => Promise<unknown>
    await queryFn({} as never)
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({ UserName: "admin" })
  })

  it("iamAccessKeysQueryOptions sends ListAccessKeysCommand", async () => {
    const queryFn = iamAccessKeysQueryOptions("admin").queryFn as (
      ctx: never,
    ) => Promise<unknown>
    await queryFn({} as never)
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({ UserName: "admin" })
  })

  it("iamPoliciesQueryOptions sends ListPoliciesCommand with Local scope", async () => {
    const queryFn = iamPoliciesQueryOptions.queryFn as (
      ctx: never,
    ) => Promise<unknown>
    await queryFn({} as never)
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({ Scope: "Local" })
  })

  it("iamPolicyQueryOptions sends GetPolicyCommand with policyArn", async () => {
    const queryFn = iamPolicyQueryOptions("arn:test").queryFn as (
      ctx: never,
    ) => Promise<unknown>
    await queryFn({} as never)
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      PolicyArn: "arn:test",
    })
  })

  it("iamPolicyVersionQueryOptions sends GetPolicyVersionCommand", async () => {
    const queryFn = iamPolicyVersionQueryOptions("arn:test", "v1").queryFn as (
      ctx: never,
    ) => Promise<unknown>
    await queryFn({} as never)
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      PolicyArn: "arn:test",
      VersionId: "v1",
    })
  })

  it("iamAttachedUserPoliciesQueryOptions sends ListAttachedUserPoliciesCommand", async () => {
    const queryFn = iamAttachedUserPoliciesQueryOptions("admin").queryFn as (
      ctx: never,
    ) => Promise<unknown>
    await queryFn({} as never)
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({ UserName: "admin" })
  })
})
