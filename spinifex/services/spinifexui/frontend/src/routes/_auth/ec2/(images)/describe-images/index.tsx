import type { Image } from "@aws-sdk/client-ec2"
import { useSuspenseQuery } from "@tanstack/react-query"
import { createFileRoute, type SearchSchemaInput } from "@tanstack/react-router"

import { ListCard } from "@/components/list-card"
import { PageHeading } from "@/components/page-heading"
import { StateBadge } from "@/components/state-badge"
import { isSystemManagedImage } from "@/lib/system-managed"
import { ec2ImagesQueryOptions } from "@/queries/ec2"

export const Route = createFileRoute("/_auth/ec2/(images)/describe-images/")({
  validateSearch: (search: { system?: string } & SearchSchemaInput) => ({
    system: search.system === "1" ? "1" : undefined,
  }),
  // ?system=1 reveals platform-managed AMIs (e.g. the HAProxy/LB image).
  loader: async ({ context }) => {
    await context.queryClient.ensureQueryData(ec2ImagesQueryOptions)
  },
  head: () => ({
    meta: [
      {
        title: "Images | EC2 | Mulga",
      },
    ],
  }),
  component: Images,
})

function Images() {
  const { data } = useSuspenseQuery(ec2ImagesQueryOptions)
  const { system } = Route.useSearch()
  const showSystem = system === "1"

  const allImages = data.Images ?? []
  const images = showSystem
    ? allImages
    : allImages.filter((image) => !isSystemManagedImage(image))
  const hiddenCount = allImages.length - images.length

  return (
    <>
      <PageHeading title="EC2 Images" />

      {images.length > 0 ? (
        <div className="space-y-4">
          {images.map((image: Image) => {
            if (!image.ImageId) {
              return null
            }
            return (
              <ListCard
                badge={<StateBadge state={image.State} />}
                key={image.ImageId}
                params={{ id: image.ImageId }}
                subtitle={image.ImageId ?? ""}
                title={image.Name ?? image.ImageId ?? ""}
                to="/ec2/describe-images/$id"
              />
            )
          })}
        </div>
      ) : (
        <p className="text-muted-foreground">No images found.</p>
      )}
      {!showSystem && hiddenCount > 0 && (
        <p className="mt-4 text-xs text-muted-foreground">
          {hiddenCount} platform-managed image{hiddenCount === 1 ? "" : "s"}{" "}
          hidden. Append <code>?system=1</code> to the URL to show them.
        </p>
      )}
    </>
  )
}
