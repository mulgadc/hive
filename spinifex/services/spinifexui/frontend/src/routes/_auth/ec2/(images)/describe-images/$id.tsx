import { useSuspenseQuery } from "@tanstack/react-query"
import { createFileRoute, type SearchSchemaInput } from "@tanstack/react-router"

import { BackLink } from "@/components/back-link"
import { PageHeading } from "@/components/page-heading"
import { StateBadge } from "@/components/state-badge"
import { isSystemManagedImage } from "@/lib/system-managed"
import { ec2ImageQueryOptions } from "@/queries/ec2"

import { AmiDetails } from "../../-components/ami-details"

export const Route = createFileRoute("/_auth/ec2/(images)/describe-images/$id")(
  {
    validateSearch: (search: { system?: string } & SearchSchemaInput) => ({
      system: search.system === "1" ? "1" : undefined,
    }),
    // ?system=1 reveals platform-managed AMIs that the listing hides.
    loader: async ({ context, params }) =>
      await context.queryClient.ensureQueryData(
        ec2ImageQueryOptions(params.id),
      ),
    head: ({ loaderData }) => ({
      meta: [
        {
          title: `${loaderData?.Images?.[0]?.Name ?? "Image"} | EC2 | Mulga`,
        },
      ],
    }),
    component: ImageDetail,
  },
)

function ImageDetail() {
  const { id } = Route.useParams()
  const { system } = Route.useSearch()
  const showSystem = system === "1"
  const { data } = useSuspenseQuery(ec2ImageQueryOptions(id))
  const image = data?.Images?.[0]

  const hiddenAsSystem =
    !showSystem && image !== undefined && isSystemManagedImage(image)

  if (!image?.ImageId || hiddenAsSystem) {
    return (
      <>
        <BackLink to="/ec2/describe-images">Back to images</BackLink>
        <p className="text-muted-foreground">Image not found.</p>
      </>
    )
  }

  return (
    <>
      <BackLink to="/ec2/describe-images">Back to images</BackLink>

      <div className="space-y-6">
        <PageHeading
          actions={<StateBadge state={image.State} />}
          subtitle="EC2 Image Details"
          title={image.ImageId}
        />

        <AmiDetails image={image} showExtendedDetails />
      </div>
    </>
  )
}
