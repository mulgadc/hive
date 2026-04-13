import {
  DescribeListenersCommand,
  DescribeLoadBalancerAttributesCommand,
  DescribeLoadBalancersCommand,
  DescribeTagsCommand,
} from "@aws-sdk/client-elastic-load-balancing-v2"
import { queryOptions } from "@tanstack/react-query"

import { getElbv2Client } from "@/lib/awsClient"

const PROVISIONING_POLL_MS = 5000

// Slice 1/2 — real implementations for LB list/detail/listeners/attrs/tags.
// Target-group + target-health queries remain stubs until slices 3/5.

const notImplemented = (name: string): never => {
  throw new Error(`elbv2 query '${name}' not implemented yet`)
}

export const elbv2LoadBalancersQueryOptions = queryOptions({
  queryKey: ["elbv2", "loadBalancers"],
  queryFn: () => {
    const command = new DescribeLoadBalancersCommand({})
    return getElbv2Client().send(command)
  },
  refetchInterval: (query) => {
    const lbs = query.state.data?.LoadBalancers ?? []
    const anyProvisioning = lbs.some((lb) => lb.State?.Code === "provisioning")
    return anyProvisioning ? PROVISIONING_POLL_MS : false
  },
})

export const elbv2LoadBalancerQueryOptions = (arn: string) =>
  queryOptions({
    queryKey: ["elbv2", "loadBalancers", arn],
    queryFn: () => {
      const command = new DescribeLoadBalancersCommand({
        LoadBalancerArns: [arn],
      })
      return getElbv2Client().send(command)
    },
    refetchInterval: (query) => {
      const lb = query.state.data?.LoadBalancers?.[0]
      return lb?.State?.Code === "provisioning" ? PROVISIONING_POLL_MS : false
    },
  })

export const elbv2LoadBalancerAttributesQueryOptions = (arn: string) =>
  queryOptions({
    queryKey: ["elbv2", "loadBalancers", arn, "attributes"],
    queryFn: () => {
      const command = new DescribeLoadBalancerAttributesCommand({
        LoadBalancerArn: arn,
      })
      return getElbv2Client().send(command)
    },
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
    queryFn: () => {
      const command = new DescribeListenersCommand({
        LoadBalancerArn: loadBalancerArn,
      })
      return getElbv2Client().send(command)
    },
  })

export const elbv2TargetHealthQueryOptions = (targetGroupArn: string) =>
  queryOptions({
    queryKey: ["elbv2", "targetGroups", targetGroupArn, "health"],
    queryFn: () => notImplemented("elbv2TargetHealthQueryOptions"),
  })

export const elbv2TagsQueryOptions = (resourceArns: string[]) =>
  queryOptions({
    queryKey: ["elbv2", "tags", ...resourceArns],
    queryFn: () => {
      const command = new DescribeTagsCommand({
        ResourceArns: resourceArns,
      })
      return getElbv2Client().send(command)
    },
    enabled: resourceArns.length > 0,
  })
