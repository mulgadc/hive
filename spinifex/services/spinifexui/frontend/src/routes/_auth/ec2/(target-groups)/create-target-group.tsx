import { zodResolver } from "@hookform/resolvers/zod"
import { useSuspenseQuery } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { useForm } from "react-hook-form"

import { BackLink } from "@/components/back-link"
import {
  CliCommandPanel,
  type CliCommand,
  type CommandPart,
} from "@/components/cli-command-panel"
import { TargetGroupForm } from "@/components/elbv2/target-group-form"
import { ErrorBanner } from "@/components/error-banner"
import { FormActions } from "@/components/form-actions"
import { PageHeading } from "@/components/page-heading"
import { useCreateTargetGroup } from "@/mutations/elbv2"
import { ec2VpcsQueryOptions } from "@/queries/ec2"
import {
  type CreateTargetGroupFormData,
  createTargetGroupSchema,
} from "@/types/elbv2"

export const Route = createFileRoute(
  "/_auth/ec2/(target-groups)/create-target-group",
)({
  loader: async ({ context }) => {
    await context.queryClient.ensureQueryData(ec2VpcsQueryOptions)
  },
  head: () => ({
    meta: [
      {
        title: "Create Target Group | EC2 | Mulga",
      },
    ],
  }),
  component: CreateTargetGroup,
})

function CreateTargetGroup() {
  const navigate = useNavigate()
  const { data: vpcsData } = useSuspenseQuery(ec2VpcsQueryOptions)
  const createMutation = useCreateTargetGroup()

  const vpcs = vpcsData.Vpcs ?? []

  const form = useForm({
    resolver: zodResolver(createTargetGroupSchema),
    defaultValues: {
      name: "",
      protocol: "HTTP",
      port: 80,
      vpcId: vpcs[0]?.VpcId ?? "",
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
    },
  })

  const {
    handleSubmit,
    watch,
    formState: { isSubmitting },
  } = form

  const onSubmit = async (data: CreateTargetGroupFormData) => {
    const result = await createMutation.mutateAsync(data)
    const arn = result.TargetGroups?.[0]?.TargetGroupArn
    if (arn) {
      navigate({
        to: "/ec2/describe-target-groups/$id",
        params: { id: encodeURIComponent(arn) },
      })
    } else {
      navigate({ to: "/ec2/describe-target-groups" })
    }
  }

  return (
    <>
      <BackLink to="/ec2/describe-target-groups">
        Back to target groups
      </BackLink>

      <PageHeading title="Create target group" />

      {createMutation.error && (
        <ErrorBanner
          error={createMutation.error}
          msg="Failed to create target group"
        />
      )}

      <form className="max-w-4xl space-y-6" onSubmit={handleSubmit(onSubmit)}>
        <TargetGroupForm form={form} vpcs={vpcs} />

        <CliCommandPanel commands={buildCreateTargetGroupCommands(watch)} />

        <FormActions
          isPending={createMutation.isPending}
          isSubmitting={isSubmitting}
          onCancel={() => navigate({ to: "/ec2/describe-target-groups" })}
          pendingLabel="Creating…"
          submitLabel="Create target group"
        />
      </form>
    </>
  )
}

function buildCreateTargetGroupCommands(
  watch: (name?: string) => unknown,
): CliCommand[] {
  const asString = (key: string): string => {
    const raw = watch(key)
    return typeof raw === "string" ? raw : ""
  }
  const asNumber = (key: string): number | undefined => {
    const raw = watch(key)
    return typeof raw === "number" && Number.isFinite(raw) ? raw : undefined
  }

  const name = asString("name")
  const protocol = asString("protocol") || "HTTP"
  const port = asNumber("port")
  const vpcId = asString("vpcId")
  const hcPath = asString("healthCheck.path") || "/"
  const hcPort = asString("healthCheck.port") || "traffic-port"
  const hcInterval = asNumber("healthCheck.intervalSeconds")
  const hcTimeout = asNumber("healthCheck.timeoutSeconds")
  const hcHealthy = asNumber("healthCheck.healthyThresholdCount")
  const hcUnhealthy = asNumber("healthCheck.unhealthyThresholdCount")
  const hcMatcher = asString("healthCheck.matcher") || "200"

  const parts: CommandPart[] = [
    {
      type: "bin",
      value: "AWS_PROFILE=spinifex aws elbv2 create-target-group",
    },
    { type: "flag", value: " \\\n  --name" },
    { type: "value", value: ` ${name || "<Name>"}` },
    { type: "flag", value: " \\\n  --protocol" },
    { type: "value", value: ` ${protocol}` },
    { type: "flag", value: " \\\n  --port" },
    { type: "value", value: ` ${port ?? 80}` },
    { type: "flag", value: " \\\n  --target-type" },
    { type: "value", value: " instance" },
  ]
  if (vpcId) {
    parts.push(
      { type: "flag", value: " \\\n  --vpc-id" },
      { type: "value", value: ` ${vpcId}` },
    )
  }
  parts.push(
    { type: "flag", value: " \\\n  --health-check-protocol" },
    { type: "value", value: " HTTP" },
    { type: "flag", value: " \\\n  --health-check-path" },
    { type: "value", value: ` ${hcPath}` },
    { type: "flag", value: " \\\n  --health-check-port" },
    { type: "value", value: ` ${hcPort}` },
    { type: "flag", value: " \\\n  --health-check-interval-seconds" },
    { type: "value", value: ` ${hcInterval ?? 30}` },
    { type: "flag", value: " \\\n  --health-check-timeout-seconds" },
    { type: "value", value: ` ${hcTimeout ?? 5}` },
    { type: "flag", value: " \\\n  --healthy-threshold-count" },
    { type: "value", value: ` ${hcHealthy ?? 5}` },
    { type: "flag", value: " \\\n  --unhealthy-threshold-count" },
    { type: "value", value: ` ${hcUnhealthy ?? 2}` },
    { type: "flag", value: " \\\n  --matcher" },
    { type: "value", value: ` HttpCode=${hcMatcher}` },
  )

  return [{ label: "Create Target Group", parts }]
}
