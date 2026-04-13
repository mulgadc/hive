import { describe, expect, it } from "vitest"

import {
  createListenerSchema,
  createLoadBalancerSchema,
  createTargetGroupSchema,
  healthCheckSchema,
  registerTargetsSchema,
} from "./elbv2"

describe("createTargetGroupSchema", () => {
  it("accepts a valid HTTP target group", () => {
    const result = createTargetGroupSchema.safeParse({
      name: "my-tg",
      protocol: "HTTP",
      port: 80,
      vpcId: "vpc-123",
      healthCheck: healthCheckSchema.parse({}),
    })
    expect(result.success).toBe(true)
  })

  it("rejects name with invalid characters", () => {
    const result = createTargetGroupSchema.safeParse({
      name: "my_tg",
      protocol: "HTTP",
      port: 80,
      vpcId: "vpc-123",
      healthCheck: healthCheckSchema.parse({}),
    })
    expect(result.success).toBe(false)
  })

  it("rejects name >32 chars", () => {
    const result = createTargetGroupSchema.safeParse({
      name: "x".repeat(33),
      protocol: "HTTP",
      port: 80,
      vpcId: "vpc-123",
      healthCheck: healthCheckSchema.parse({}),
    })
    expect(result.success).toBe(false)
  })

  it("rejects port out of range", () => {
    const result = createTargetGroupSchema.safeParse({
      name: "my-tg",
      protocol: "HTTP",
      port: 70_000,
      vpcId: "vpc-123",
      healthCheck: healthCheckSchema.parse({}),
    })
    expect(result.success).toBe(false)
  })
})

describe("createLoadBalancerSchema", () => {
  const baseListener = {
    protocol: "HTTP" as const,
    port: 80,
    targetGroupMode: "existing" as const,
    existingTargetGroupArn: "arn:tg:1",
  }

  it("accepts a valid ALB with 2+ subnets", () => {
    const result = createLoadBalancerSchema.safeParse({
      name: "my-alb",
      scheme: "internet-facing",
      vpcId: "vpc-1",
      subnetIds: ["subnet-a", "subnet-b"],
      securityGroupIds: ["sg-1"],
      tags: [],
      listener: baseListener,
    })
    expect(result.success).toBe(true)
  })

  it("rejects <2 subnets", () => {
    const result = createLoadBalancerSchema.safeParse({
      name: "my-alb",
      scheme: "internet-facing",
      vpcId: "vpc-1",
      subnetIds: ["subnet-a"],
      listener: baseListener,
    })
    expect(result.success).toBe(false)
  })

  it("rejects names starting with 'internal-'", () => {
    const result = createLoadBalancerSchema.safeParse({
      name: "internal-abc",
      scheme: "internal",
      vpcId: "vpc-1",
      subnetIds: ["subnet-a", "subnet-b"],
      listener: baseListener,
    })
    expect(result.success).toBe(false)
  })

  it("rejects listener with mode=new but no newTargetGroup", () => {
    const result = createLoadBalancerSchema.safeParse({
      name: "my-alb",
      scheme: "internet-facing",
      vpcId: "vpc-1",
      subnetIds: ["subnet-a", "subnet-b"],
      listener: {
        protocol: "HTTP",
        port: 80,
        targetGroupMode: "new",
      },
    })
    expect(result.success).toBe(false)
  })
})

describe("createListenerSchema", () => {
  it("accepts a valid listener", () => {
    const result = createListenerSchema.safeParse({
      protocol: "HTTP",
      port: 80,
      defaultTargetGroupArn: "arn:tg:1",
    })
    expect(result.success).toBe(true)
  })

  it("rejects listener missing default target group", () => {
    const result = createListenerSchema.safeParse({
      protocol: "HTTP",
      port: 80,
      defaultTargetGroupArn: "",
    })
    expect(result.success).toBe(false)
  })
})

describe("registerTargetsSchema", () => {
  it("accepts one or more targets", () => {
    const result = registerTargetsSchema.safeParse({
      targets: [{ instanceId: "i-123" }, { instanceId: "i-456", port: 8080 }],
    })
    expect(result.success).toBe(true)
  })

  it("rejects empty target list", () => {
    const result = registerTargetsSchema.safeParse({ targets: [] })
    expect(result.success).toBe(false)
  })
})

describe("healthCheckSchema defaults", () => {
  it("applies documented defaults", () => {
    const result = healthCheckSchema.parse({})
    expect(result).toEqual({
      protocol: "HTTP",
      path: "/",
      port: "traffic-port",
      intervalSeconds: 30,
      timeoutSeconds: 5,
      healthyThresholdCount: 5,
      unhealthyThresholdCount: 2,
      matcher: "200",
    })
  })
})
