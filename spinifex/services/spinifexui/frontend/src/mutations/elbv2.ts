import {
  type Tag,
  CreateTargetGroupCommand,
  DeleteLoadBalancerCommand,
  DeleteTargetGroupCommand,
} from "@aws-sdk/client-elastic-load-balancing-v2"
import { useMutation, useQueryClient } from "@tanstack/react-query"

import { getElbv2Client } from "@/lib/awsClient"
import type { CreateTargetGroupFormData } from "@/types/elbv2"

// Mutations are filled in per slice (2/3/4/5/6/7). Remaining stubs throw so an
// accidental caller fails loudly rather than silently.

const notImplemented = (name: string): never => {
  throw new Error(`elbv2 mutation '${name}' not implemented yet`)
}

export function useCreateLoadBalancer() {
  return useMutation({
    mutationFn: () => notImplemented("useCreateLoadBalancer"),
  })
}

export function useDeleteLoadBalancer() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (loadBalancerArn: string) => {
      const command = new DeleteLoadBalancerCommand({
        LoadBalancerArn: loadBalancerArn,
      })
      return getElbv2Client().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["elbv2", "loadBalancers"] })
    },
  })
}

export function useModifyLoadBalancerAttributes() {
  return useMutation({
    mutationFn: () => notImplemented("useModifyLoadBalancerAttributes"),
  })
}

export function useCreateTargetGroup() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (params: CreateTargetGroupFormData) => {
      const tags: Tag[] = params.tags
        .filter((t) => t.key.length > 0)
        .map((t) => ({ Key: t.key, Value: t.value }))
      const command = new CreateTargetGroupCommand({
        Name: params.name,
        Protocol: params.protocol,
        Port: params.port,
        VpcId: params.vpcId,
        TargetType: "instance",
        HealthCheckProtocol: params.healthCheck.protocol,
        HealthCheckPath: params.healthCheck.path,
        HealthCheckPort: params.healthCheck.port,
        HealthCheckIntervalSeconds: params.healthCheck.intervalSeconds,
        HealthCheckTimeoutSeconds: params.healthCheck.timeoutSeconds,
        HealthyThresholdCount: params.healthCheck.healthyThresholdCount,
        UnhealthyThresholdCount: params.healthCheck.unhealthyThresholdCount,
        Matcher: { HttpCode: params.healthCheck.matcher },
        Tags: tags.length > 0 ? tags : undefined,
      })
      return getElbv2Client().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["elbv2", "targetGroups"] })
    },
  })
}

export function useDeleteTargetGroup() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (targetGroupArn: string) => {
      const command = new DeleteTargetGroupCommand({
        TargetGroupArn: targetGroupArn,
      })
      return getElbv2Client().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["elbv2", "targetGroups"] })
    },
  })
}

export function useModifyTargetGroupAttributes() {
  return useMutation({
    mutationFn: () => notImplemented("useModifyTargetGroupAttributes"),
  })
}

export function useCreateListener() {
  return useMutation({
    mutationFn: () => notImplemented("useCreateListener"),
  })
}

export function useDeleteListener() {
  return useMutation({
    mutationFn: () => notImplemented("useDeleteListener"),
  })
}

export function useRegisterTargets() {
  return useMutation({
    mutationFn: () => notImplemented("useRegisterTargets"),
  })
}

export function useDeregisterTargets() {
  return useMutation({
    mutationFn: () => notImplemented("useDeregisterTargets"),
  })
}
