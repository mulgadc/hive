import { DeleteLoadBalancerCommand } from "@aws-sdk/client-elastic-load-balancing-v2"
import { useMutation, useQueryClient } from "@tanstack/react-query"

import { getElbv2Client } from "@/lib/awsClient"

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
  return useMutation({
    mutationFn: () => notImplemented("useCreateTargetGroup"),
  })
}

export function useDeleteTargetGroup() {
  return useMutation({
    mutationFn: () => notImplemented("useDeleteTargetGroup"),
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
