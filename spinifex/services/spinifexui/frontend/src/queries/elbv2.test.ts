import { describe, expect, it } from "vitest"

import {
  elbv2ListenersQueryOptions,
  elbv2LoadBalancerAttributesQueryOptions,
  elbv2LoadBalancerQueryOptions,
  elbv2LoadBalancersQueryOptions,
  elbv2TagsQueryOptions,
  elbv2TargetGroupAttributesQueryOptions,
  elbv2TargetGroupQueryOptions,
  elbv2TargetGroupsQueryOptions,
  elbv2TargetHealthQueryOptions,
} from "./elbv2"

describe("elbv2 query keys", () => {
  it("elbv2LoadBalancersQueryOptions has correct key", () => {
    expect(elbv2LoadBalancersQueryOptions.queryKey).toEqual([
      "elbv2",
      "loadBalancers",
    ])
  })

  it("elbv2LoadBalancerQueryOptions includes arn", () => {
    expect(elbv2LoadBalancerQueryOptions("arn:lb").queryKey).toEqual([
      "elbv2",
      "loadBalancers",
      "arn:lb",
    ])
  })

  it("elbv2LoadBalancerAttributesQueryOptions includes arn + attributes", () => {
    expect(elbv2LoadBalancerAttributesQueryOptions("arn:lb").queryKey).toEqual([
      "elbv2",
      "loadBalancers",
      "arn:lb",
      "attributes",
    ])
  })

  it("elbv2TargetGroupsQueryOptions has correct key", () => {
    expect(elbv2TargetGroupsQueryOptions.queryKey).toEqual([
      "elbv2",
      "targetGroups",
    ])
  })

  it("elbv2TargetGroupQueryOptions includes arn", () => {
    expect(elbv2TargetGroupQueryOptions("arn:tg").queryKey).toEqual([
      "elbv2",
      "targetGroups",
      "arn:tg",
    ])
  })

  it("elbv2TargetGroupAttributesQueryOptions includes arn + attributes", () => {
    expect(elbv2TargetGroupAttributesQueryOptions("arn:tg").queryKey).toEqual([
      "elbv2",
      "targetGroups",
      "arn:tg",
      "attributes",
    ])
  })

  it("elbv2ListenersQueryOptions includes lb arn", () => {
    expect(elbv2ListenersQueryOptions("arn:lb").queryKey).toEqual([
      "elbv2",
      "listeners",
      "arn:lb",
    ])
  })

  it("elbv2TargetHealthQueryOptions includes tg arn + health", () => {
    expect(elbv2TargetHealthQueryOptions("arn:tg").queryKey).toEqual([
      "elbv2",
      "targetGroups",
      "arn:tg",
      "health",
    ])
  })

  it("elbv2TagsQueryOptions spreads resource arns into key", () => {
    expect(elbv2TagsQueryOptions(["arn:lb", "arn:tg"]).queryKey).toEqual([
      "elbv2",
      "tags",
      "arn:lb",
      "arn:tg",
    ])
  })
})

type QueryFnWithSignal = (ctx: { signal: AbortSignal }) => Promise<unknown>

function callQueryFn(queryFn: unknown): Promise<unknown> {
  return (queryFn as QueryFnWithSignal)({
    signal: new AbortController().signal,
  })
}

describe("elbv2 query stubs throw until implemented", () => {
  it("loadBalancers queryFn throws not-implemented", () => {
    expect(() => callQueryFn(elbv2LoadBalancersQueryOptions.queryFn)).toThrow(
      /not implemented/,
    )
  })

  it("targetGroups queryFn throws not-implemented", () => {
    expect(() => callQueryFn(elbv2TargetGroupsQueryOptions.queryFn)).toThrow(
      /not implemented/,
    )
  })
})
