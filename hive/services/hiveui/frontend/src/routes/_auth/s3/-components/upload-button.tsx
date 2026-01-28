import { Upload } from "lucide-react"
import { useRef } from "react"

import { Button } from "@/components/ui/button"
import { useUploadObject } from "@/mutations/s3"

interface UploadButtonProps {
  bucket: string
  prefix?: string
}

export function UploadButton({ bucket, prefix = "" }: UploadButtonProps) {
  const fileInputRef = useRef<HTMLInputElement>(null)
  const uploadMutation = useUploadObject()

  async function handleFileSelect(event: React.ChangeEvent<HTMLInputElement>) {
    const files = event.target.files
    if (!files || files.length === 0) {
      return
    }

    try {
      await Promise.all(
        Array.from(files).map((file) => {
          const key = `${prefix}${file.name}`
          return uploadMutation.mutateAsync({
            bucket,
            key,
            file,
          })
        }),
      )
    } finally {
      // Reset the input so the same file can be uploaded again
      if (fileInputRef.current) {
        fileInputRef.current.value = ""
      }
    }
  }

  return (
    <>
      <input
        accept="*/*"
        className="hidden"
        multiple
        onChange={handleFileSelect}
        ref={fileInputRef}
        type="file"
      />
      <Button
        disabled={uploadMutation.isPending}
        onClick={() => fileInputRef.current?.click()}
        size="sm"
      >
        <Upload className="mr-2 size-4" />
        {uploadMutation.isPending ? "Uploading…" : "Upload Files"}
      </Button>
    </>
  )
}
