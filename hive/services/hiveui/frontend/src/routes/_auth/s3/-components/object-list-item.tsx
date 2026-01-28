import type { _Object } from "@aws-sdk/client-s3"
import { GetObjectCommand } from "@aws-sdk/client-s3"
import { Download, File, Trash2 } from "lucide-react"
import { useState } from "react"

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
import { getS3Client } from "@/lib/awsClient"
import { formatSize } from "@/lib/utils"
import { useDeleteObject } from "@/mutations/s3"

interface ObjectListItemProps {
  object: _Object
  bucketName: string
  displayName: string
  fullKey: string
}

export function ObjectListItem({
  object,
  bucketName,
  displayName,
  fullKey,
}: ObjectListItemProps) {
  const [showDeleteDialog, setShowDeleteDialog] = useState(false)
  const deleteMutation = useDeleteObject()

  async function downloadObject() {
    try {
      const command = new GetObjectCommand({
        Bucket: bucketName,
        Key: fullKey,
      })
      const response = await getS3Client().send(command)

      if (response.Body) {
        const blob = await response.Body.transformToByteArray()
        const url = URL.createObjectURL(new Blob([blob]))

        const link = document.createElement("a")
        link.href = url
        link.download = displayName
        document.body.appendChild(link)
        link.click()
        document.body.removeChild(link)
        URL.revokeObjectURL(url)
      }
    } catch {
      throw new Error("Failed to download object")
    }
  }

  async function handleDelete() {
    await deleteMutation.mutateAsync({
      bucket: bucketName,
      key: fullKey,
    })
    setShowDeleteDialog(false)
  }

  return (
    <div
      className="flex items-center justify-between rounded-lg border bg-card p-4"
      key={object.Key}
    >
      <div className="flex items-center gap-3">
        <File className="size-5 text-muted-foreground" />
        <div>
          <h3 className="font-medium">{displayName}</h3>
          {object.LastModified && (
            <p className="text-muted-foreground text-sm">
              Last Modified: {object.LastModified.toLocaleString()}
            </p>
          )}
        </div>
      </div>
      <div className="flex items-center gap-3">
        <div className="text-muted-foreground text-sm">
          {formatSize(object.Size || 0)}
        </div>
        <button
          className="rounded-md p-2 transition-colors hover:bg-accent"
          onClick={downloadObject}
          type="button"
        >
          <Download className="size-4" />
        </button>
        <button
          className="rounded-md p-2 text-destructive transition-colors hover:bg-accent"
          onClick={() => setShowDeleteDialog(true)}
          type="button"
        >
          <Trash2 className="size-4" />
        </button>
      </div>

      <AlertDialog onOpenChange={setShowDeleteDialog} open={showDeleteDialog}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete Object</AlertDialogTitle>
            <AlertDialogDescription>
              Are you sure you want to delete <strong>{displayName}</strong>?
              This action cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive hover:bg-destructive/90"
              onClick={handleDelete}
            >
              {deleteMutation.isPending ? "Deleting…" : "Delete"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
