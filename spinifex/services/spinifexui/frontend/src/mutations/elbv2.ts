import {
  type Action,
  type Tag,
  CreateListenerCommand,
  CreateLoadBalancerCommand,
  CreateTargetGroupCommand,
  DeleteListenerCommand,
  DeleteLoadBalancerCommand,
  DeleteTargetGroupCommand,
} from "@aws-sdk/client-elastic-load-balancing-v2"
import { useMutation, useQueryClient } from "@tanstack/react-query"

import { getElbv2Client } from "@/lib/awsClient"
import type {
  CreateLoadBalancerFormData,
  CreateTargetGroupFormData,
} from "@/types/elbv2"

// Mutations are filled in per slice (2/3/4/5/6/7). Remaining stubs throw so an
// accidental caller fails loudly rather than silently.

const notImplemented = (name: string): never => {
  throw new Error(`elbv2 mutation '${name}' not implemented yet`)
}

export interface CreateLoadBalancerParams {
  name: string
  scheme: "internet-facing" | "internal"
  subnetIds: string[]
  securityGroupIds: string[]
  tags: { key: string; value: string }[]
}

export function useCreateLoadBalancer() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (params: CreateLoadBalancerParams) => {
      const tags: Tag[] = params.tags
        .filter((t) => t.key.length > 0)
        .map((t) => ({ Key: t.key, Value: t.value }))
      const command = new CreateLoadBalancerCommand({
        Name: params.name,
        Scheme: params.scheme,
        Type: "application",
        IpAddressType: "ipv4",
        Subnets: params.subnetIds,
        SecurityGroups:
          params.securityGroupIds.length > 0
            ? params.securityGroupIds
            : undefined,
        Tags: tags.length > 0 ? tags : undefined,
      })
      return getElbv2Client().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["elbv2", "loadBalancers"] })
    },
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

export interface CreateListenerParams {
  loadBalancerArn: string
  protocol: "HTTP"
  port: number
  defaultTargetGroupArn: string
}

export function useCreateListener() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (params: CreateListenerParams) => {
      const defaultActions: Action[] = [
        {
          Type: "forward",
          TargetGroupArn: params.defaultTargetGroupArn,
        },
      ]
      const command = new CreateListenerCommand({
        LoadBalancerArn: params.loadBalancerArn,
        Protocol: params.protocol,
        Port: params.port,
        DefaultActions: defaultActions,
      })
      return getElbv2Client().send(command)
    },
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({
        queryKey: ["elbv2", "listeners", variables.loadBalancerArn],
      })
    },
  })
}

export function useDeleteListener() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (listenerArn: string) => {
      const command = new DeleteListenerCommand({ ListenerArn: listenerArn })
      return getElbv2Client().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["elbv2", "listeners"] })
    },
  })
}

export interface LbWizardCreatedResource {
  type: string
  id: string | undefined
}

export interface LbWizardResult {
  loadBalancerArn: string | undefined
  created: LbWizardCreatedResource[]
  error?: Error
  failedStep?: string
}

export interface CreateLoadBalancerWizardParams {
  lb: Omit<CreateLoadBalancerFormData, "listener">
  listener: {
    protocol: "HTTP"
    port: number
    targetGroupMode: "new" | "existing"
    existingTargetGroupArn?: string
    newTargetGroup?: CreateTargetGroupFormData
  }
}

export function useCreateLoadBalancerWizard() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (
      params: CreateLoadBalancerWizardParams,
    ): Promise<LbWizardResult> => {
      const client = getElbv2Client()
      const created: LbWizardCreatedResource[] = []
      let currentStep = ""

      try {
        // Step 1: create new TG if requested
        let targetGroupArn = params.listener.existingTargetGroupArn
        if (params.listener.targetGroupMode === "new") {
          const tg = params.listener.newTargetGroup
          if (!tg) {
            throw new Error(
              "internal error: new target group requested but no data supplied",
            )
          }
          currentStep = "creating target group"
          const tgTags: Tag[] = tg.tags
            .filter((t) => t.key.length > 0)
            .map((t) => ({ Key: t.key, Value: t.value }))
          const tgResult = await client.send(
            new CreateTargetGroupCommand({
              Name: tg.name,
              Protocol: tg.protocol,
              Port: tg.port,
              VpcId: tg.vpcId,
              TargetType: "instance",
              HealthCheckProtocol: tg.healthCheck.protocol,
              HealthCheckPath: tg.healthCheck.path,
              HealthCheckPort: tg.healthCheck.port,
              HealthCheckIntervalSeconds: tg.healthCheck.intervalSeconds,
              HealthCheckTimeoutSeconds: tg.healthCheck.timeoutSeconds,
              HealthyThresholdCount: tg.healthCheck.healthyThresholdCount,
              UnhealthyThresholdCount: tg.healthCheck.unhealthyThresholdCount,
              Matcher: { HttpCode: tg.healthCheck.matcher },
              Tags: tgTags.length > 0 ? tgTags : undefined,
            }),
          )
          targetGroupArn = tgResult.TargetGroups?.[0]?.TargetGroupArn
          if (!targetGroupArn) {
            throw new Error(
              "Target group was created but no ARN was returned by the API",
            )
          }
          created.push({ type: "Target Group", id: targetGroupArn })
        }

        if (!targetGroupArn) {
          throw new Error("Target group selection is required")
        }

        // Step 2: create LB
        currentStep = "creating load balancer"
        const lbTags: Tag[] = params.lb.tags
          .filter((t) => t.key.length > 0)
          .map((t) => ({ Key: t.key, Value: t.value }))
        const lbResult = await client.send(
          new CreateLoadBalancerCommand({
            Name: params.lb.name,
            Scheme: params.lb.scheme,
            Type: "application",
            IpAddressType: "ipv4",
            Subnets: params.lb.subnetIds,
            SecurityGroups:
              params.lb.securityGroupIds.length > 0
                ? params.lb.securityGroupIds
                : undefined,
            Tags: lbTags.length > 0 ? lbTags : undefined,
          }),
        )
        const loadBalancerArn = lbResult.LoadBalancers?.[0]?.LoadBalancerArn
        if (!loadBalancerArn) {
          throw new Error(
            "Load balancer was created but no ARN was returned by the API",
          )
        }
        created.push({ type: "Load Balancer", id: loadBalancerArn })

        // Step 3: create listener
        currentStep = "creating listener"
        await client.send(
          new CreateListenerCommand({
            LoadBalancerArn: loadBalancerArn,
            Protocol: params.listener.protocol,
            Port: params.listener.port,
            DefaultActions: [
              { Type: "forward", TargetGroupArn: targetGroupArn },
            ],
          }),
        )
        created.push({ type: "Listener", id: undefined })

        return { loadBalancerArn, created }
      } catch (error) {
        return {
          loadBalancerArn: undefined,
          created,
          error: error instanceof Error ? error : new Error(String(error)),
          failedStep: currentStep,
        }
      }
    },
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: ["elbv2"] })
    },
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
