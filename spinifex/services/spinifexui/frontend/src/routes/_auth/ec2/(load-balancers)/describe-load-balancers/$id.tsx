import type { Subnet } from "@aws-sdk/client-ec2"
import type {
  Listener,
  LoadBalancerAttribute,
  Tag,
} from "@aws-sdk/client-elastic-load-balancing-v2"
import { useSuspenseQuery } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { Trash2 } from "lucide-react"
import { useState } from "react"

import { BackLink } from "@/components/back-link"
import { DeleteConfirmationDialog } from "@/components/delete-confirmation-dialog"
import { DetailCard } from "@/components/detail-card"
import { DetailRow } from "@/components/detail-row"
import { ErrorBanner } from "@/components/error-banner"
import { PageHeading } from "@/components/page-heading"
import { StateBadge } from "@/components/state-badge"
import { Button } from "@/components/ui/button"
import { Tabs, TabsList, TabsPanel, TabsTab } from "@/components/ui/tabs"
import { getNameTag } from "@/lib/utils"
import { useDeleteLoadBalancer } from "@/mutations/elbv2"
import { ec2SubnetsQueryOptions } from "@/queries/ec2"
import {
  elbv2ListenersQueryOptions,
  elbv2LoadBalancerAttributesQueryOptions,
  elbv2LoadBalancerQueryOptions,
  elbv2TagsQueryOptions,
} from "@/queries/elbv2"

export const Route = createFileRoute(
  "/_auth/ec2/(load-balancers)/describe-load-balancers/$id",
)({
  loader: async ({ context, params }) => {
    const arn = decodeURIComponent(params.id)
    await Promise.all([
      context.queryClient.ensureQueryData(elbv2LoadBalancerQueryOptions(arn)),
      context.queryClient.ensureQueryData(elbv2ListenersQueryOptions(arn)),
      context.queryClient.ensureQueryData(
        elbv2LoadBalancerAttributesQueryOptions(arn),
      ),
      context.queryClient.ensureQueryData(elbv2TagsQueryOptions([arn])),
      context.queryClient.ensureQueryData(ec2SubnetsQueryOptions),
    ])
  },
  head: ({ params }) => ({
    meta: [
      {
        title: `${decodeURIComponent(params.id)} | Load Balancer | Mulga`,
      },
    ],
  }),
  component: LoadBalancerDetail,
})

function formatDefaultAction(listener: Listener): string {
  const action = listener.DefaultActions?.[0]
  if (!action) {
    return "—"
  }
  if (action.Type === "forward" && action.TargetGroupArn) {
    return `forward → ${action.TargetGroupArn}`
  }
  return action.Type ?? "—"
}

function LoadBalancerDetail() {
  const { id } = Route.useParams()
  const arn = decodeURIComponent(id)
  const navigate = useNavigate()
  const { data: lbData } = useSuspenseQuery(elbv2LoadBalancerQueryOptions(arn))
  const { data: listenersData } = useSuspenseQuery(
    elbv2ListenersQueryOptions(arn),
  )
  const { data: attrsData } = useSuspenseQuery(
    elbv2LoadBalancerAttributesQueryOptions(arn),
  )
  const { data: tagsData } = useSuspenseQuery(elbv2TagsQueryOptions([arn]))
  const { data: subnetsData } = useSuspenseQuery(ec2SubnetsQueryOptions)

  const deleteMutation = useDeleteLoadBalancer()
  const [showDeleteDialog, setShowDeleteDialog] = useState(false)

  const lb = lbData.LoadBalancers?.[0]

  if (!lb?.LoadBalancerArn) {
    return (
      <>
        <BackLink to="/ec2/describe-load-balancers">
          Back to load balancers
        </BackLink>
        <p className="text-muted-foreground">Load balancer not found.</p>
      </>
    )
  }

  const handleDelete = async () => {
    try {
      await deleteMutation.mutateAsync(arn)
      navigate({ to: "/ec2/describe-load-balancers" })
    } finally {
      setShowDeleteDialog(false)
    }
  }

  const subnets = subnetsData.Subnets ?? []
  const subnetLabel = (subnetId: string): string => {
    const subnet = subnets.find((s: Subnet) => s.SubnetId === subnetId)
    if (!subnet) {
      return subnetId
    }
    const name = getNameTag(subnet.Tags)
    const cidr = subnet.CidrBlock ? ` (${subnet.CidrBlock})` : ""
    return name ? `${subnetId} — ${name}${cidr}` : `${subnetId}${cidr}`
  }

  const listeners = listenersData.Listeners ?? []
  const attributes = attrsData.Attributes ?? []
  const lbTags =
    tagsData?.TagDescriptions?.find((td) => td.ResourceArn === arn)?.Tags ?? []

  return (
    <>
      <BackLink to="/ec2/describe-load-balancers">
        Back to load balancers
      </BackLink>

      {deleteMutation.error && (
        <ErrorBanner
          error={deleteMutation.error}
          msg="Failed to delete load balancer"
        />
      )}

      <div className="space-y-6">
        <PageHeading
          actions={
            <div className="flex items-center gap-2">
              <Button
                onClick={() => setShowDeleteDialog(true)}
                size="sm"
                variant="destructive"
              >
                <Trash2 className="size-4" />
                Delete
              </Button>
              <StateBadge state={lb.State?.Code} />
            </div>
          }
          subtitle="Load balancer details"
          title={lb.LoadBalancerName ?? lb.LoadBalancerArn}
        />

        <Tabs defaultValue="overview">
          <TabsList>
            <TabsTab value="overview">Overview</TabsTab>
            <TabsTab value="listeners">Listeners</TabsTab>
            <TabsTab value="attributes">Attributes</TabsTab>
            <TabsTab value="tags">Tags</TabsTab>
          </TabsList>

          <TabsPanel value="overview">
            <DetailCard>
              <DetailCard.Header>Load balancer</DetailCard.Header>
              <DetailCard.Content>
                <DetailRow label="ARN" value={lb.LoadBalancerArn} />
                <DetailRow label="DNS name" value={lb.DNSName} />
                <DetailRow
                  label="State"
                  value={
                    lb.State?.Reason
                      ? `${lb.State.Code} — ${lb.State.Reason}`
                      : lb.State?.Code
                  }
                />
                <DetailRow label="Type" value={lb.Type} />
                <DetailRow label="Scheme" value={lb.Scheme} />
                <DetailRow label="IP address type" value={lb.IpAddressType} />
                <DetailRow label="VPC" value={lb.VpcId} />
                <DetailRow
                  label="Created at"
                  value={
                    lb.CreatedTime
                      ? new Date(lb.CreatedTime).toLocaleString()
                      : undefined
                  }
                />
              </DetailCard.Content>
            </DetailCard>

            <DetailCard>
              <DetailCard.Header>Availability zones</DetailCard.Header>
              <DetailCard.Content>
                {(lb.AvailabilityZones ?? []).map((az) => (
                  <DetailRow
                    key={`${az.ZoneName}-${az.SubnetId}`}
                    label={az.ZoneName ?? "—"}
                    value={az.SubnetId ? subnetLabel(az.SubnetId) : undefined}
                  />
                ))}
              </DetailCard.Content>
            </DetailCard>

            {(lb.SecurityGroups ?? []).length > 0 && (
              <DetailCard>
                <DetailCard.Header>Security groups</DetailCard.Header>
                <DetailCard.Content>
                  {(lb.SecurityGroups ?? []).map((sg) => (
                    <DetailRow key={sg} label="Security group" value={sg} />
                  ))}
                </DetailCard.Content>
              </DetailCard>
            )}
          </TabsPanel>

          <TabsPanel value="listeners">
            <p className="mb-3 text-sm text-muted-foreground">
              Listeners cannot be edited. Delete and re-add to change.
            </p>
            {listeners.length > 0 ? (
              <div className="overflow-x-auto rounded-lg border bg-card">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="border-b text-left text-muted-foreground">
                      <th className="px-4 py-2 font-medium">Protocol</th>
                      <th className="px-4 py-2 font-medium">Port</th>
                      <th className="px-4 py-2 font-medium">Default action</th>
                      <th className="px-4 py-2 font-medium">Listener ARN</th>
                    </tr>
                  </thead>
                  <tbody>
                    {listeners.map((listener: Listener) => (
                      <tr
                        className="border-b last:border-0"
                        key={listener.ListenerArn ?? ""}
                      >
                        <td className="px-4 py-2">{listener.Protocol}</td>
                        <td className="px-4 py-2">{listener.Port}</td>
                        <td className="px-4 py-2 font-mono text-xs">
                          {formatDefaultAction(listener)}
                        </td>
                        <td className="px-4 py-2 font-mono text-xs">
                          {listener.ListenerArn}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <p className="text-muted-foreground">No listeners configured.</p>
            )}
          </TabsPanel>

          <TabsPanel value="attributes">
            {attributes.length > 0 ? (
              <DetailCard>
                <DetailCard.Header>Attributes</DetailCard.Header>
                <DetailCard.Content>
                  {attributes.map((attr: LoadBalancerAttribute) => (
                    <DetailRow
                      key={attr.Key ?? ""}
                      label={attr.Key ?? ""}
                      value={attr.Value}
                    />
                  ))}
                </DetailCard.Content>
              </DetailCard>
            ) : (
              <p className="text-muted-foreground">No attributes reported.</p>
            )}
          </TabsPanel>

          <TabsPanel value="tags">
            {lbTags.length > 0 ? (
              <DetailCard>
                <DetailCard.Header>Tags</DetailCard.Header>
                <DetailCard.Content>
                  {lbTags.map((tag: Tag) => (
                    <DetailRow
                      key={tag.Key ?? ""}
                      label={tag.Key ?? ""}
                      value={tag.Value}
                    />
                  ))}
                </DetailCard.Content>
              </DetailCard>
            ) : (
              <p className="text-muted-foreground">No tags.</p>
            )}
          </TabsPanel>
        </Tabs>
      </div>

      <DeleteConfirmationDialog
        description={`Are you sure you want to delete load balancer "${
          lb.LoadBalancerName ?? lb.LoadBalancerArn
        }"? This action cannot be undone.`}
        isPending={deleteMutation.isPending}
        onConfirm={handleDelete}
        onOpenChange={setShowDeleteDialog}
        open={showDeleteDialog}
        title="Delete load balancer"
      />
    </>
  )
}
