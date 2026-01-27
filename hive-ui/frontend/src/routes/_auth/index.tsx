import { useSuspenseQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"

import { PageHeading } from "@/components/page-heading"
import { ec2RegionsQueryOptions } from "@/queries/ec2"

export const Route = createFileRoute("/_auth/")({
  loader: async ({ context }) => {
    await context.queryClient.ensureQueryData(ec2RegionsQueryOptions)
  },
  head: () => ({
    meta: [
      {
        title: "Dashboard | Mulga",
      },
    ],
  }),
  component: App,
})

function App() {
  const { data: regions } = useSuspenseQuery(ec2RegionsQueryOptions)

  return (
    <PageHeading title={`${regions?.Regions?.[0]?.RegionName} Dashboard`} />
  )
}
