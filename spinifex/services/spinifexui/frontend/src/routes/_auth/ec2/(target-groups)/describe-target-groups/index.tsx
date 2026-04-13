import { createFileRoute } from "@tanstack/react-router"

import { PageHeading } from "@/components/page-heading"

export const Route = createFileRoute(
  "/_auth/ec2/(target-groups)/describe-target-groups/",
)({
  head: () => ({
    meta: [
      {
        title: "Target Groups | EC2 | Mulga",
      },
    ],
  }),
  component: TargetGroups,
})

function TargetGroups() {
  return (
    <>
      <PageHeading title="EC2 Target Groups" />
      <p className="text-muted-foreground">
        Target group management UI is coming soon.
      </p>
    </>
  )
}
