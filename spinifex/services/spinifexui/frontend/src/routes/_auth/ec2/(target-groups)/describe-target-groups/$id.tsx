import type {
  Tag,
  TargetGroupAttribute,
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
import { Button } from "@/components/ui/button"
import { Tabs, TabsList, TabsPanel, TabsTab } from "@/components/ui/tabs"
import { useDeleteTargetGroup } from "@/mutations/elbv2"
import {
  elbv2TagsQueryOptions,
  elbv2TargetGroupAttributesQueryOptions,
  elbv2TargetGroupQueryOptions,
} from "@/queries/elbv2"

export const Route = createFileRoute(
  "/_auth/ec2/(target-groups)/describe-target-groups/$id",
)({
  loader: async ({ context, params }) => {
    const arn = decodeURIComponent(params.id)
    await Promise.all([
      context.queryClient.ensureQueryData(elbv2TargetGroupQueryOptions(arn)),
      context.queryClient.ensureQueryData(
        elbv2TargetGroupAttributesQueryOptions(arn),
      ),
      context.queryClient.ensureQueryData(elbv2TagsQueryOptions([arn])),
    ])
  },
  head: ({ params }) => ({
    meta: [
      {
        title: `${decodeURIComponent(params.id)} | Target Group | Mulga`,
      },
    ],
  }),
  component: TargetGroupDetail,
})

function TargetGroupDetail() {
  const { id } = Route.useParams()
  const arn = decodeURIComponent(id)
  const navigate = useNavigate()
  const { data: tgData } = useSuspenseQuery(elbv2TargetGroupQueryOptions(arn))
  const { data: attrsData } = useSuspenseQuery(
    elbv2TargetGroupAttributesQueryOptions(arn),
  )
  const { data: tagsData } = useSuspenseQuery(elbv2TagsQueryOptions([arn]))

  const deleteMutation = useDeleteTargetGroup()
  const [showDeleteDialog, setShowDeleteDialog] = useState(false)

  const tg = tgData.TargetGroups?.[0]

  if (!tg?.TargetGroupArn) {
    return (
      <>
        <BackLink to="/ec2/describe-target-groups">
          Back to target groups
        </BackLink>
        <p className="text-muted-foreground">Target group not found.</p>
      </>
    )
  }

  const handleDelete = async () => {
    try {
      await deleteMutation.mutateAsync(arn)
      navigate({ to: "/ec2/describe-target-groups" })
    } finally {
      setShowDeleteDialog(false)
    }
  }

  const attributes = attrsData.Attributes ?? []
  const tgTags =
    tagsData?.TagDescriptions?.find((td) => td.ResourceArn === arn)?.Tags ?? []

  return (
    <>
      <BackLink to="/ec2/describe-target-groups">
        Back to target groups
      </BackLink>

      {deleteMutation.error && (
        <ErrorBanner
          error={deleteMutation.error}
          msg="Failed to delete target group"
        />
      )}

      <div className="space-y-6">
        <PageHeading
          actions={
            <Button
              onClick={() => setShowDeleteDialog(true)}
              size="sm"
              variant="destructive"
            >
              <Trash2 className="size-4" />
              Delete
            </Button>
          }
          subtitle="Target group details"
          title={tg.TargetGroupName ?? tg.TargetGroupArn}
        />

        <Tabs defaultValue="overview">
          <TabsList>
            <TabsTab value="overview">Overview</TabsTab>
            <TabsTab value="targets">Targets</TabsTab>
            <TabsTab value="health-checks">Health checks</TabsTab>
            <TabsTab value="attributes">Attributes</TabsTab>
            <TabsTab value="tags">Tags</TabsTab>
          </TabsList>

          <TabsPanel value="overview">
            <DetailCard>
              <DetailCard.Header>Target group</DetailCard.Header>
              <DetailCard.Content>
                <DetailRow label="ARN" value={tg.TargetGroupArn} />
                <DetailRow label="Protocol" value={tg.Protocol} />
                <DetailRow label="Port" value={tg.Port?.toString()} />
                <DetailRow label="VPC" value={tg.VpcId} />
                <DetailRow label="Target type" value={tg.TargetType} />
                <DetailRow
                  label="Protocol version"
                  value={tg.ProtocolVersion}
                />
                <DetailRow label="IP address type" value={tg.IpAddressType} />
              </DetailCard.Content>
            </DetailCard>
          </TabsPanel>

          <TabsPanel value="targets">
            <p className="text-muted-foreground">
              Register/deregister targets lands in slice 5.
            </p>
          </TabsPanel>

          <TabsPanel value="health-checks">
            <p className="mb-3 text-sm text-muted-foreground">
              Health-check settings are set at creation time. Editing lands in a
              future slice (mulga-948).
            </p>
            <DetailCard>
              <DetailCard.Header>Health check</DetailCard.Header>
              <DetailCard.Content>
                <DetailRow
                  label="Enabled"
                  value={
                    tg.HealthCheckEnabled === undefined
                      ? undefined
                      : String(tg.HealthCheckEnabled)
                  }
                />
                <DetailRow label="Protocol" value={tg.HealthCheckProtocol} />
                <DetailRow label="Path" value={tg.HealthCheckPath} />
                <DetailRow label="Port" value={tg.HealthCheckPort} />
                <DetailRow
                  label="Interval (s)"
                  value={tg.HealthCheckIntervalSeconds?.toString()}
                />
                <DetailRow
                  label="Timeout (s)"
                  value={tg.HealthCheckTimeoutSeconds?.toString()}
                />
                <DetailRow
                  label="Healthy threshold"
                  value={tg.HealthyThresholdCount?.toString()}
                />
                <DetailRow
                  label="Unhealthy threshold"
                  value={tg.UnhealthyThresholdCount?.toString()}
                />
                <DetailRow
                  label="Matcher (HTTP codes)"
                  value={tg.Matcher?.HttpCode ?? tg.Matcher?.GrpcCode}
                />
              </DetailCard.Content>
            </DetailCard>
          </TabsPanel>

          <TabsPanel value="attributes">
            {attributes.length > 0 ? (
              <DetailCard>
                <DetailCard.Header>Attributes</DetailCard.Header>
                <DetailCard.Content>
                  {attributes.map((attr: TargetGroupAttribute) => (
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
            {tgTags.length > 0 ? (
              <DetailCard>
                <DetailCard.Header>Tags</DetailCard.Header>
                <DetailCard.Content>
                  {tgTags.map((tag: Tag) => (
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
        description={`Are you sure you want to delete target group "${
          tg.TargetGroupName ?? tg.TargetGroupArn
        }"? This action cannot be undone.`}
        isPending={deleteMutation.isPending}
        onConfirm={handleDelete}
        onOpenChange={setShowDeleteDialog}
        open={showDeleteDialog}
        title="Delete target group"
      />
    </>
  )
}
