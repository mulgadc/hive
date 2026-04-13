import { createFileRoute } from "@tanstack/react-router"

import { ec2ImagesQueryOptions } from "@/queries/ec2"
import { elbv2LoadBalancersQueryOptions } from "@/queries/elbv2"

import { DescribeLoadBalancersPage } from "../-components/describe-load-balancers-page"

export const Route = createFileRoute(
  "/_auth/ec2/(load-balancers)/describe-load-balancers/",
)({
  loader: async ({ context }) => {
    await Promise.all([
      context.queryClient.ensureQueryData(elbv2LoadBalancersQueryOptions),
      context.queryClient.ensureQueryData(ec2ImagesQueryOptions),
    ])
  },
  head: () => ({
    meta: [
      {
        title: "Load Balancers | EC2 | Mulga",
      },
    ],
  }),
  component: DescribeLoadBalancersPage,
})
