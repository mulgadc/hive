import { createFileRoute } from "@tanstack/react-router"

import { PageHeading } from "@/components/page-heading"

export const Route = createFileRoute(
  "/_auth/ec2/(load-balancers)/describe-load-balancers/",
)({
  head: () => ({
    meta: [
      {
        title: "Load Balancers | EC2 | Mulga",
      },
    ],
  }),
  component: LoadBalancers,
})

function LoadBalancers() {
  return (
    <>
      <PageHeading title="EC2 Load Balancers" />
      <p className="text-muted-foreground">
        Load balancer management UI is coming soon.
      </p>
    </>
  )
}
