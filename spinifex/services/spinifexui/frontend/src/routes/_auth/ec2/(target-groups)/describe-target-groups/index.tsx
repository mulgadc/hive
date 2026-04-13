import { createFileRoute } from "@tanstack/react-router"

import { elbv2TargetGroupsQueryOptions } from "@/queries/elbv2"

import { DescribeTargetGroupsPage } from "../-components/describe-target-groups-page"

export const Route = createFileRoute(
  "/_auth/ec2/(target-groups)/describe-target-groups/",
)({
  loader: async ({ context }) => {
    await context.queryClient.ensureQueryData(elbv2TargetGroupsQueryOptions)
  },
  head: () => ({
    meta: [
      {
        title: "Target Groups | EC2 | Mulga",
      },
    ],
  }),
  component: DescribeTargetGroupsPage,
})
