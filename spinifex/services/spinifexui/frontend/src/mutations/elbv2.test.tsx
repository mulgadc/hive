import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { renderHook, waitFor } from "@testing-library/react"
import type { ReactNode } from "react"
import { afterEach, describe, expect, it, vi } from "vitest"

const mockSend = vi.fn().mockResolvedValue({})

vi.mock("@/lib/awsClient", () => ({
  getElbv2Client: () => ({ send: mockSend }),
}))

import type { CreateTargetGroupFormData } from "@/types/elbv2"

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

describe("useDeleteTargetGroup", () => {
  it("sends DeleteTargetGroupCommand with target group ARN", async () => {
    createQueryClient()
    const { result } = renderHook(() => useDeleteTargetGroup(), { wrapper })

    result.current.mutate("arn:aws:elasticloadbalancing:tg/app/foo/abc")

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      TargetGroupArn: "arn:aws:elasticloadbalancing:tg/app/foo/abc",
    })
  })
})

describe("useCreateTargetGroup", () => {
  const baseParams: CreateTargetGroupFormData = {
    name: "my-tg",
    protocol: "HTTP",
    port: 80,
    vpcId: "vpc-123",
    healthCheck: {
      protocol: "HTTP",
      path: "/health",
      port: "traffic-port",
      intervalSeconds: 30,
      timeoutSeconds: 5,
      healthyThresholdCount: 5,
      unhealthyThresholdCount: 2,
      matcher: "200",
    },
    tags: [],
  }

  it("sends CreateTargetGroupCommand with form data and hardcoded instance target type", async () => {
    createQueryClient()
    const { result } = renderHook(() => useCreateTargetGroup(), { wrapper })

    result.current.mutate(baseParams)

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      Name: "my-tg",
      Protocol: "HTTP",
      Port: 80,
      VpcId: "vpc-123",
      TargetType: "instance",
      HealthCheckProtocol: "HTTP",
      HealthCheckPath: "/health",
      HealthCheckPort: "traffic-port",
      HealthCheckIntervalSeconds: 30,
      HealthCheckTimeoutSeconds: 5,
      HealthyThresholdCount: 5,
      UnhealthyThresholdCount: 2,
      Matcher: { HttpCode: "200" },
      Tags: undefined,
    })
  })

  it("passes non-empty tags through and skips empty keys", async () => {
    createQueryClient()
    const { result } = renderHook(() => useCreateTargetGroup(), { wrapper })

    result.current.mutate({
      ...baseParams,
      tags: [
        { key: "env", value: "prod" },
        { key: "", value: "ignored" },
      ],
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input.Tags).toEqual([
      { Key: "env", Value: "prod" },
    ])
  })
})
