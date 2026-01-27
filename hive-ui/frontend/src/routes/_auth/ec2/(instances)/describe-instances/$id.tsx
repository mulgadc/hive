import { useSuspenseQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"

import { BackLink } from "@/components/back-link"
import { DetailCard } from "@/components/detail-card"
import { DetailRow } from "@/components/detail-row"
import { PageHeading } from "@/components/page-heading"
import { StateBadge } from "@/components/state-badge"
import { formatDateTime } from "@/lib/utils"
import { ec2ImageQueryOptions, ec2InstanceQueryOptions } from "@/queries/ec2"
import { AmiDetails } from "../../-components/ami-details"
import { InstanceActions } from "../../-components/instance-actions"

export const Route = createFileRoute(
  "/_auth/ec2/(instances)/describe-instances/$id",
)({
  loader: async ({ context, params }) => {
    const instanceData = await context.queryClient.ensureQueryData(
      ec2InstanceQueryOptions(params.id),
    )
    const imageId = instanceData.Reservations?.[0]?.Instances?.[0]?.ImageId
    await context.queryClient.ensureQueryData(ec2ImageQueryOptions(imageId))
    return instanceData
  },
  head: ({ loaderData }) => ({
    meta: [
      {
        title: `${loaderData?.Reservations?.[0]?.Instances?.[0]?.InstanceId ?? "Instance"} | EC2 | Mulga`,
      },
    ],
  }),
  component: InstanceDetail,
})

function InstanceDetail() {
  const { id } = Route.useParams()
  const { data } = useSuspenseQuery(ec2InstanceQueryOptions(id))
  const instance = data.Reservations?.[0]?.Instances?.[0]

  const { data: imageData } = useSuspenseQuery(
    ec2ImageQueryOptions(instance?.ImageId),
  )
  const image = imageData?.Images?.[0]

  if (!instance?.InstanceId) {
    return (
      <>
        <BackLink to="/ec2/describe-instances">Back to instances</BackLink>
        <p className="text-muted-foreground">Instance not found.</p>
      </>
    )
  }

  const launchTime = formatDateTime(instance.LaunchTime)

  return (
    <>
      <BackLink to="/ec2/describe-instances">Back to instances</BackLink>

      <div className="space-y-6">
        <PageHeading
          actions={<StateBadge state={instance.State?.Name} />}
          subtitle="EC2 Instance Details"
          title={instance.InstanceId}
        />

        <InstanceActions
          instanceId={instance.InstanceId}
          state={instance.State?.Name}
        />

        {/* Instance Details */}
        <DetailCard>
          <DetailCard.Header>Instance Information</DetailCard.Header>
          <DetailCard.Content>
            <DetailRow label="Instance ID" value={instance.InstanceId} />
            <DetailRow label="Instance Type" value={instance.InstanceType} />
            <DetailRow label="Instance State" value={instance.State?.Name} />
            <DetailRow
              label="State Code"
              value={instance.State?.Code?.toString()}
            />
            <DetailRow label="Launch Time" value={launchTime} />
            <DetailRow
              label="Availability Zone"
              value={instance.Placement?.AvailabilityZone}
            />
          </DetailCard.Content>
        </DetailCard>

        {/* Network & Security */}
        {/* <DetailCard>
          <DetailCard.Header>Network & Security</DetailCard.Header>
          <DetailCard.Content>
            <DetailRow
              label="Private IP Address"
              value={instance.PrivateIpAddress}
            />
            <DetailRow
              label="Public IP Address"
              value={instance.PublicIpAddress}
            />
            <DetailRow label="Private DNS" value={instance.PrivateDnsName} />
            <DetailRow label="Public DNS" value={instance.PublicDnsName} />
            <DetailRow label="VPC ID" value={instance.VpcId} />
            <DetailRow label="Subnet ID" value={instance.SubnetId} />
            <DetailRow label="Key Name" value={instance.KeyName} />
            <DetailRow
              label="Security Groups"
              value={instance.SecurityGroups?.map((sg) => sg.GroupName).join(
                ", ",
              )}
            />
          </DetailCard.Content>
        </DetailCard> */}

        {/* AMI Details */}
        {image && <AmiDetails image={image} />}
      </div>
    </>
  )
}
