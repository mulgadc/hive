import { useSuspenseQuery } from "@tanstack/react-query"
import { createFileRoute, Link } from "@tanstack/react-router"

import { BackLink } from "@/components/back-link"
import { DetailCard } from "@/components/detail-card"
import { DetailRow } from "@/components/detail-row"
import { PageHeading } from "@/components/page-heading"
import { Badge } from "@/components/ui/badge"
import { Button, buttonVariants } from "@/components/ui/button"
import { formatDateTime } from "@/lib/utils"
import { ec2VolumeQueryOptions } from "@/queries/ec2"

export const Route = createFileRoute(
  "/_auth/ec2/(volumes)/describe-volumes/$id",
)({
  loader: async ({ context, params }) => {
    await context.queryClient.ensureQueryData(ec2VolumeQueryOptions(params.id))
  },
  head: ({ params }) => ({
    meta: [
      {
        title: `${params.id} | EC2 | Mulga`,
      },
    ],
  }),
  component: VolumeDetail,
})

function VolumeDetail() {
  const { id } = Route.useParams()
  const { data } = useSuspenseQuery(ec2VolumeQueryOptions(id))
  const volume = data.Volumes?.[0]

  if (!volume?.VolumeId) {
    return (
      <>
        <BackLink to="/ec2/describe-volumes">Back to volumes</BackLink>
        <p className="text-muted-foreground">Volume not found.</p>
      </>
    )
  }

  const createTime = formatDateTime(volume.CreateTime)

  return (
    <>
      <BackLink to="/ec2/describe-volumes">Back to volumes</BackLink>

      <div className="space-y-6">
        <PageHeading
          actions={
            <div className="flex items-center gap-2">
              {volume.State === "available" ? (
                <Link
                  className={buttonVariants({ variant: "outline" })}
                  params={{ id: volume.VolumeId }}
                  to="/ec2/modify-volume/$id"
                >
                  Resize Volume
                </Link>
              ) : (
                <Button disabled variant="outline">
                  Resize Volume
                </Button>
              )}
              {volume.State && (
                <Badge
                  variant={volume.State === "available" ? "default" : "outline"}
                >
                  {volume.State}
                </Badge>
              )}
            </div>
          }
          subtitle="EC2 Volume Details"
          title={volume.VolumeId}
        />

        <DetailCard>
          <DetailCard.Header>Volume Information</DetailCard.Header>
          <DetailCard.Content>
            <DetailRow label="Volume ID" value={volume.VolumeId} />
            <DetailRow label="Size" value={`${volume.Size} GiB`} />
            <DetailRow label="Volume Type" value={volume.VolumeType} />
            <DetailRow label="State" value={volume.State} />
            <DetailRow
              label="Availability Zone"
              value={volume.AvailabilityZone}
            />
            <DetailRow label="Create Time" value={createTime} />
            <DetailRow
              label="Encrypted"
              value={volume.Encrypted ? "Yes" : "No"}
            />
            <DetailRow label="KMS Key ID" value={volume.KmsKeyId} />
          </DetailCard.Content>
        </DetailCard>

        {volume.Attachments && volume.Attachments.length > 0 && (
          <DetailCard>
            <DetailCard.Header>Attachments</DetailCard.Header>
            <DetailCard.Content>
              {volume.Attachments.map((attachment) => (
                <div
                  className="space-y-2 border-b pb-2 last:border-0 last:pb-0"
                  key={attachment.InstanceId}
                >
                  <DetailRow
                    label="Instance ID"
                    value={attachment.InstanceId}
                  />
                  <DetailRow label="Device" value={attachment.Device} />
                  <DetailRow label="Status" value={attachment.State} />
                  <DetailRow
                    label="Attach Time"
                    value={formatDateTime(attachment.AttachTime)}
                  />
                  <DetailRow
                    label="Delete on Termination"
                    value={attachment.DeleteOnTermination ? "Yes" : "No"}
                  />
                </div>
              ))}
            </DetailCard.Content>
          </DetailCard>
        )}
      </div>
    </>
  )
}
