import { createFileRoute } from "@tanstack/react-router"

import { ec2SubnetsQueryOptions } from "@/queries/ec2"
import {
  elbv2ListenersQueryOptions,
  elbv2LoadBalancerAttributesQueryOptions,
  elbv2LoadBalancerQueryOptions,
  elbv2TagsQueryOptions,
  elbv2TargetGroupsQueryOptions,
} from "@/queries/elbv2"

import { LoadBalancerDetailPage } from "../-components/load-balancer-detail-page"

export const Route = createFileRoute(
  "/_auth/ec2/(load-balancers)/describe-load-balancers/$id",
)({
  loader: async ({ context, params }) => {
    const arn = decodeURIComponent(params.id)
    await Promise.all([
      context.queryClient.ensureQueryData(elbv2LoadBalancerQueryOptions(arn)),
      context.queryClient.ensureQueryData(elbv2ListenersQueryOptions(arn)),
      context.queryClient.ensureQueryData(
        elbv2LoadBalancerAttributesQueryOptions(arn),
      ),
      context.queryClient.ensureQueryData(elbv2TagsQueryOptions([arn])),
      context.queryClient.ensureQueryData(elbv2TargetGroupsQueryOptions),
      context.queryClient.ensureQueryData(ec2SubnetsQueryOptions),
    ])
  },
  head: ({ params }) => ({
    meta: [
      {
        title: `${decodeURIComponent(params.id)} | Load Balancer | Mulga`,
      },
    ],
  }),
  component: RouteComponent,
})

function RouteComponent() {
  const { id } = Route.useParams()
  return <LoadBalancerDetailPage arn={decodeURIComponent(id)} />
}
