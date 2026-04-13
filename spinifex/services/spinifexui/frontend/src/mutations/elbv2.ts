import { useMutation } from "@tanstack/react-query"

// Slice 1 — stubs. Filled in per slice (3/4/5/6/7).
// Each mutationFn throws so an accidental caller fails loudly.

const notImplemented = (name: string): never => {
  throw new Error(`elbv2 mutation '${name}' not implemented yet`)
}

export function useCreateLoadBalancer() {
  return useMutation({
    mutationFn: () => notImplemented("useCreateLoadBalancer"),
  })
}

export function useDeleteLoadBalancer() {
  return useMutation({
    mutationFn: () => notImplemented("useDeleteLoadBalancer"),
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
