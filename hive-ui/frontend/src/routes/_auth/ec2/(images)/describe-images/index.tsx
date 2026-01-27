import type { Image } from "@aws-sdk/client-ec2"
import { useSuspenseQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"

import { ListCard } from "@/components/list-card"
import { PageHeading } from "@/components/page-heading"
import { Badge } from "@/components/ui/badge"
import { ec2ImagesQueryOptions } from "@/queries/ec2"

export const Route = createFileRoute("/_auth/ec2/(images)/describe-images/")({
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

  const images = data.Images || []

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
                badge={
                  image.State ? (
                    <Badge
                      variant={
                        image.State === "available" ? "default" : "outline"
                      }
                    >
                      {image.State}
                    </Badge>
                  ) : undefined
                }
                key={image.ImageId}
                params={{ id: image.ImageId }}
                subtitle={image.Name}
                title={image.ImageId}
                to="/ec2/describe-images/$id"
              />
            )
          })}
        </div>
      ) : (
        <p className="text-muted-foreground">No images found.</p>
      )}
    </>
  )
}
