import { useSuspenseQuery } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { Trash2 } from "lucide-react"
import { useState } from "react"

import { BackLink } from "@/components/back-link"
import { DetailCard } from "@/components/detail-card"
import { DetailRow } from "@/components/detail-row"
import { ErrorBanner } from "@/components/error-banner"
import { PageHeading } from "@/components/page-heading"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { formatDateTime } from "@/lib/utils"
import { useDeletePolicy } from "@/mutations/iam"
import {
  iamPolicyQueryOptions,
  iamPolicyVersionQueryOptions,
} from "@/queries/iam"
import { PolicyDocumentViewer } from "../../-components/policy-document-viewer"

export const Route = createFileRoute(
  "/_auth/iam/(policies)/list-policies/$policyArn",
)({
  loader: async ({ context, params }) => {
    const policyArn = decodeURIComponent(params.policyArn)
    const policyData = await context.queryClient.ensureQueryData(
      iamPolicyQueryOptions(policyArn),
    )
    const versionId = policyData.Policy?.DefaultVersionId
    if (versionId) {
      await context.queryClient.ensureQueryData(
        iamPolicyVersionQueryOptions(policyArn, versionId),
      )
    }
  },
  head: ({ params }) => ({
    meta: [
      {
        title: `${decodeURIComponent(params.policyArn)} | IAM | Mulga`,
      },
    ],
  }),
  component: PolicyDetail,
})

function PolicyDetail() {
  const { policyArn: encodedArn } = Route.useParams()
  const policyArn = decodeURIComponent(encodedArn)
  const navigate = useNavigate()
  const { data: policyData } = useSuspenseQuery(
    iamPolicyQueryOptions(policyArn),
  )
  const policy = policyData.Policy
  const deleteMutation = useDeletePolicy()
  const [showDeleteDialog, setShowDeleteDialog] = useState(false)

  const versionId = policy?.DefaultVersionId ?? "v1"
  const { data: versionData } = useSuspenseQuery(
    iamPolicyVersionQueryOptions(policyArn, versionId),
  )

  const policyDocument = versionData?.PolicyVersion?.Document
    ? decodeURIComponent(versionData.PolicyVersion.Document)
    : null

  const handleDelete = async () => {
    try {
      await deleteMutation.mutateAsync(policyArn)
      navigate({ to: "/iam/list-policies" })
    } finally {
      setShowDeleteDialog(false)
    }
  }

  if (!policy) {
    return (
      <>
        <BackLink to="/iam/list-policies">Back to policies</BackLink>
        <p className="text-muted-foreground">Policy not found.</p>
      </>
    )
  }

  return (
    <>
      <BackLink to="/iam/list-policies">Back to policies</BackLink>

      {deleteMutation.error && (
        <ErrorBanner
          error={deleteMutation.error}
          msg="Failed to delete policy"
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
          subtitle="IAM Policy Details"
          title={policy.PolicyName ?? ""}
        />

        <DetailCard>
          <DetailCard.Header>Policy Information</DetailCard.Header>
          <DetailCard.Content>
            <DetailRow label="Policy Name" value={policy.PolicyName} />
            <DetailRow label="Policy ID" value={policy.PolicyId} />
            <DetailRow label="ARN" value={policy.Arn} />
            <DetailRow label="Path" value={policy.Path} />
            <DetailRow label="Description" value={policy.Description || "-"} />
            <DetailRow
              label="Created"
              value={formatDateTime(policy.CreateDate)}
            />
          </DetailCard.Content>
        </DetailCard>

        {policyDocument && (
          <DetailCard>
            <DetailCard.Header>Policy Document</DetailCard.Header>
            <div className="p-4">
              <PolicyDocumentViewer document={policyDocument} />
            </div>
          </DetailCard>
        )}
      </div>

      <AlertDialog onOpenChange={setShowDeleteDialog} open={showDeleteDialog}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete Policy</AlertDialogTitle>
            <AlertDialogDescription>
              Are you sure you want to delete the policy "{policy.PolicyName}"?
              This action cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              disabled={deleteMutation.isPending}
              onClick={handleDelete}
            >
              {deleteMutation.isPending ? "Deleting..." : "Delete"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
