import { DeleteObjectCommand, PutObjectCommand } from "@aws-sdk/client-s3"
import { useMutation, useQueryClient } from "@tanstack/react-query"

import { getS3Client } from "@/lib/awsClient"

interface UploadObjectParams {
  bucket: string
  key: string
  file: File
}

export function useUploadObject() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async ({ bucket, key, file }: UploadObjectParams) => {
      const arrayBuffer = await file.arrayBuffer()
      const body = new Uint8Array(arrayBuffer)

      const command = new PutObjectCommand({
        Bucket: bucket,
        Key: key,
        Body: body,
        ContentType: file.type,
      })

      return await getS3Client().send(command)
    },
    onSuccess: (_, variables) => {
      queryClient.invalidateQueries({
        queryKey: ["s3", "buckets", variables.bucket, "objects"],
      })
    },
  })
}

export function useDeleteObject() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async ({ bucket, key }: { bucket: string; key: string }) => {
      const command = new DeleteObjectCommand({
        Bucket: bucket,
        Key: key,
      })

      return await getS3Client().send(command)
    },
    onSuccess: (_, variables) => {
      queryClient.invalidateQueries({
        queryKey: ["s3", "buckets", variables.bucket, "objects"],
      })
    },
  })
}
