import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { renderHook, waitFor } from "@testing-library/react"
import type { ReactNode } from "react"
import { afterEach, describe, expect, it, vi } from "vitest"

const mockSend = vi.fn().mockResolvedValue({})

vi.mock("@/lib/awsClient", () => ({
  getElbv2Client: () => ({ send: mockSend }),
}))

import {
  useCreateListener,
  useCreateLoadBalancer,
  useCreateTargetGroup,
  useDeleteListener,
  useDeleteLoadBalancer,
  useDeleteTargetGroup,
  useDeregisterTargets,
  useModifyLoadBalancerAttributes,
  useModifyTargetGroupAttributes,
  useRegisterTargets,
} from "./elbv2"

let queryClient: QueryClient

function wrapper({ children }: { children: ReactNode }) {
  return (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
}

afterEach(() => {
  mockSend.mockClear()
})

function createQueryClient() {
  queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
  return queryClient
}

const stubs = [
  ["useCreateLoadBalancer", useCreateLoadBalancer],
  ["useModifyLoadBalancerAttributes", useModifyLoadBalancerAttributes],
  ["useCreateTargetGroup", useCreateTargetGroup],
  ["useDeleteTargetGroup", useDeleteTargetGroup],
  ["useModifyTargetGroupAttributes", useModifyTargetGroupAttributes],
  ["useCreateListener", useCreateListener],
  ["useDeleteListener", useDeleteListener],
  ["useRegisterTargets", useRegisterTargets],
  ["useDeregisterTargets", useDeregisterTargets],
] as const

describe("elbv2 mutation stubs throw until implemented", () => {
  for (const [name, useMutationHook] of stubs) {
    it(`${name} fires 'not implemented'`, async () => {
      createQueryClient()
      const { result } = renderHook(() => useMutationHook(), { wrapper })

      result.current.mutate(undefined as never)

      await waitFor(() => expect(result.current.isError).toBe(true))
      expect(result.current.error?.message).toMatch(/not implemented/)
    })
  }
})

describe("useDeleteLoadBalancer", () => {
  it("sends DeleteLoadBalancerCommand with load balancer ARN", async () => {
    createQueryClient()
    const { result } = renderHook(() => useDeleteLoadBalancer(), { wrapper })

    result.current.mutate("arn:aws:elasticloadbalancing:lb/app/foo/abc")

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      LoadBalancerArn: "arn:aws:elasticloadbalancing:lb/app/foo/abc",
    })
  })
})
