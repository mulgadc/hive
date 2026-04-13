import { queryOptions } from "@tanstack/react-query"

// Slice 1 — stubs. Filled in per slice (2/3/5/6/7).
// Each throws so an accidental caller fails loudly rather than silently.

const notImplemented = (name: string): never => {
  throw new Error(`elbv2 query '${name}' not implemented yet`)
}

export const elbv2LoadBalancersQueryOptions = queryOptions({
  queryKey: ["elbv2", "loadBalancers"],
  queryFn: () => notImplemented("elbv2LoadBalancersQueryOptions"),
})

export const elbv2LoadBalancerQueryOptions = (arn: string) =>
  queryOptions({
    queryKey: ["elbv2", "loadBalancers", arn],
    queryFn: () => notImplemented("elbv2LoadBalancerQueryOptions"),
  })

export const elbv2LoadBalancerAttributesQueryOptions = (arn: string) =>
  queryOptions({
    queryKey: ["elbv2", "loadBalancers", arn, "attributes"],
    queryFn: () => notImplemented("elbv2LoadBalancerAttributesQueryOptions"),
  })

export const elbv2TargetGroupsQueryOptions = queryOptions({
  queryKey: ["elbv2", "targetGroups"],
  queryFn: () => notImplemented("elbv2TargetGroupsQueryOptions"),
})

export const elbv2TargetGroupQueryOptions = (arn: string) =>
  queryOptions({
    queryKey: ["elbv2", "targetGroups", arn],
    queryFn: () => notImplemented("elbv2TargetGroupQueryOptions"),
  })

export const elbv2TargetGroupAttributesQueryOptions = (arn: string) =>
  queryOptions({
    queryKey: ["elbv2", "targetGroups", arn, "attributes"],
    queryFn: () => notImplemented("elbv2TargetGroupAttributesQueryOptions"),
  })

export const elbv2ListenersQueryOptions = (loadBalancerArn: string) =>
  queryOptions({
    queryKey: ["elbv2", "listeners", loadBalancerArn],
    queryFn: () => notImplemented("elbv2ListenersQueryOptions"),
  })

export const elbv2TargetHealthQueryOptions = (targetGroupArn: string) =>
  queryOptions({
    queryKey: ["elbv2", "targetGroups", targetGroupArn, "health"],
    queryFn: () => notImplemented("elbv2TargetHealthQueryOptions"),
  })

export const elbv2TagsQueryOptions = (resourceArns: string[]) =>
  queryOptions({
    queryKey: ["elbv2", "tags", ...resourceArns],
    queryFn: () => notImplemented("elbv2TagsQueryOptions"),
  })
