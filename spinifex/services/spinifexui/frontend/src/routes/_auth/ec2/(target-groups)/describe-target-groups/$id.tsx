import { createFileRoute } from "@tanstack/react-router"

import { ec2InstancesQueryOptions } from "@/queries/ec2"
import {
  elbv2TagsQueryOptions,
  elbv2TargetGroupAttributesQueryOptions,
  elbv2TargetGroupQueryOptions,
} from "@/queries/elbv2"

import { TargetGroupDetailPage } from "../-components/target-group-detail-page"

export const Route = createFileRoute(
  "/_auth/ec2/(target-groups)/describe-target-groups/$id",
)({
  loader: async ({ context, params }) => {
    const arn = decodeURIComponent(params.id)
    await Promise.all([
      context.queryClient.ensureQueryData(elbv2TargetGroupQueryOptions(arn)),
      context.queryClient.ensureQueryData(
        elbv2TargetGroupAttributesQueryOptions(arn),
      ),
      context.queryClient.ensureQueryData(elbv2TagsQueryOptions([arn])),
      context.queryClient.ensureQueryData(ec2InstancesQueryOptions),
    ])
  },
  head: ({ params }) => ({
    meta: [
      {
        title: `${decodeURIComponent(params.id)} | Target Group | Mulga`,
      },
    ],
  }),
  component: RouteComponent,
})

function RouteComponent() {
  const { id } = Route.useParams()
  return <TargetGroupDetailPage arn={decodeURIComponent(id)} />
}
