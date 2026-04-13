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
  useCreateLoadBalancerWizard,
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
  ["useModifyLoadBalancerAttributes", useModifyLoadBalancerAttributes],
  ["useModifyTargetGroupAttributes", useModifyTargetGroupAttributes],
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

describe("useCreateLoadBalancer", () => {
  it("sends CreateLoadBalancerCommand with ALB defaults (Type=application, IpAddressType=ipv4)", async () => {
    createQueryClient()
    const { result } = renderHook(() => useCreateLoadBalancer(), { wrapper })

    result.current.mutate({
      name: "my-alb",
      scheme: "internet-facing",
      subnetIds: ["subnet-a", "subnet-b"],
      securityGroupIds: ["sg-1"],
      tags: [{ key: "env", value: "prod" }],
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      Name: "my-alb",
      Scheme: "internet-facing",
      Type: "application",
      IpAddressType: "ipv4",
      Subnets: ["subnet-a", "subnet-b"],
      SecurityGroups: ["sg-1"],
      Tags: [{ Key: "env", Value: "prod" }],
    })
  })

  it("omits SecurityGroups/Tags when empty", async () => {
    createQueryClient()
    const { result } = renderHook(() => useCreateLoadBalancer(), { wrapper })

    result.current.mutate({
      name: "my-alb",
      scheme: "internal",
      subnetIds: ["subnet-a", "subnet-b"],
      securityGroupIds: [],
      tags: [],
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    const input = mockSend.mock.calls[0]?.[0].input
    expect(input.SecurityGroups).toBeUndefined()
    expect(input.Tags).toBeUndefined()
  })
})

describe("useCreateListener", () => {
  it("sends CreateListenerCommand with forward default action", async () => {
    createQueryClient()
    const { result } = renderHook(() => useCreateListener(), { wrapper })

    result.current.mutate({
      loadBalancerArn: "arn:lb:1",
      protocol: "HTTP",
      port: 80,
      defaultTargetGroupArn: "arn:tg:1",
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      LoadBalancerArn: "arn:lb:1",
      Protocol: "HTTP",
      Port: 80,
      DefaultActions: [{ Type: "forward", TargetGroupArn: "arn:tg:1" }],
    })
  })
})

describe("useDeleteListener", () => {
  it("sends DeleteListenerCommand with listener ARN", async () => {
    createQueryClient()
    const { result } = renderHook(() => useDeleteListener(), { wrapper })

    result.current.mutate("arn:listener:1")

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      ListenerArn: "arn:listener:1",
    })
  })
})

describe("useCreateLoadBalancerWizard", () => {
  const tgParams: CreateTargetGroupFormData = {
    name: "my-tg",
    protocol: "HTTP",
    port: 80,
    vpcId: "vpc-123",
    healthCheck: {
      protocol: "HTTP",
      path: "/",
      port: "traffic-port",
      intervalSeconds: 30,
      timeoutSeconds: 5,
      healthyThresholdCount: 5,
      unhealthyThresholdCount: 2,
      matcher: "200",
    },
    tags: [],
  }

  const lbBase = {
    name: "my-alb",
    scheme: "internet-facing" as const,
    vpcId: "vpc-123",
    subnetIds: ["subnet-a", "subnet-b"],
    securityGroupIds: ["sg-1"],
    tags: [],
  }

  it("creates TG → LB → Listener on happy path with new target group", async () => {
    createQueryClient()
    mockSend.mockReset()
    mockSend
      .mockResolvedValueOnce({
        TargetGroups: [{ TargetGroupArn: "arn:tg:new" }],
      })
      .mockResolvedValueOnce({
        LoadBalancers: [{ LoadBalancerArn: "arn:lb:new" }],
      })
      .mockResolvedValueOnce({})
    const { result } = renderHook(() => useCreateLoadBalancerWizard(), {
      wrapper,
    })

    result.current.mutate({
      lb: lbBase,
      listener: {
        protocol: "HTTP",
        port: 80,
        targetGroupMode: "new",
        newTargetGroup: tgParams,
      },
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.error).toBeUndefined()
    expect(result.current.data?.loadBalancerArn).toBe("arn:lb:new")
    expect(result.current.data?.created).toHaveLength(3)
    expect(mockSend).toHaveBeenCalledTimes(3)
  })

  it("skips TG creation when mode=existing", async () => {
    createQueryClient()
    mockSend.mockReset()
    mockSend
      .mockResolvedValueOnce({
        LoadBalancers: [{ LoadBalancerArn: "arn:lb:new" }],
      })
      .mockResolvedValueOnce({})
    const { result } = renderHook(() => useCreateLoadBalancerWizard(), {
      wrapper,
    })

    result.current.mutate({
      lb: lbBase,
      listener: {
        protocol: "HTTP",
        port: 80,
        targetGroupMode: "existing",
        existingTargetGroupArn: "arn:tg:existing",
      },
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend).toHaveBeenCalledTimes(2)
    expect(result.current.data?.created).toHaveLength(2)
  })

  it("surfaces partial creation when LB step fails", async () => {
    createQueryClient()
    mockSend.mockReset()
    mockSend
      .mockResolvedValueOnce({
        TargetGroups: [{ TargetGroupArn: "arn:tg:new" }],
      })
      .mockRejectedValueOnce(new Error("lb boom"))
    const { result } = renderHook(() => useCreateLoadBalancerWizard(), {
      wrapper,
    })

    result.current.mutate({
      lb: lbBase,
      listener: {
        protocol: "HTTP",
        port: 80,
        targetGroupMode: "new",
        newTargetGroup: tgParams,
      },
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.error?.message).toBe("lb boom")
    expect(result.current.data?.failedStep).toBe("creating load balancer")
    expect(result.current.data?.created).toEqual([
      { type: "Target Group", id: "arn:tg:new" },
    ])
    expect(result.current.data?.loadBalancerArn).toBeUndefined()
  })
})
