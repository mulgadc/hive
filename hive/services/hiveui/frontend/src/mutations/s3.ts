import { CreateBucketCommand, DeleteObjectCommand } from "@aws-sdk/client-s3"
import { Upload } from "@aws-sdk/lib-storage"
import { useMutation, useQueryClient } from "@tanstack/react-query"

import { getS3Client } from "@/lib/awsClient"
import type { CreateBucketFormData } from "@/types/s3"

interface UploadObjectParams {
  bucket: string
  key: string
  file: File
}

export function useUploadObject() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async ({ bucket, key, file }: UploadObjectParams) => {
      try {
        const upload = new Upload({
          client: getS3Client(),
          params: {
            Bucket: bucket,
            Key: key,
            Body: file,
            ContentType: file.type,
          },
        })

        return await upload.done()
      } catch {
        throw new Error("Failed to upload object")
      }
    },
    onSuccess: (_, variables) => {
      queryClient.invalidateQueries({
        queryKey: ["s3", "buckets", variables.bucket, "objects"],
      })
    },
  })
}

export function useCreateBucket() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async ({ bucketName }: CreateBucketFormData) => {
      try {
        const command = new CreateBucketCommand({
          Bucket: bucketName,
        })

        return await getS3Client().send(command)
      } catch {
        throw new Error("Failed to create bucket")
      }
    },
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: ["s3", "buckets"],
      })
    },
  })
}

export function useDeleteObject() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async ({ bucket, key }: { bucket: string; key: string }) => {
      try {
        const command = new DeleteObjectCommand({
          Bucket: bucket,
          Key: key,
        })

        return await getS3Client().send(command)
      } catch {
        throw new Error("Failed to delete object")
      }
    },
    onSuccess: (_, variables) => {
      queryClient.invalidateQueries({
        queryKey: ["s3", "buckets", variables.bucket, "objects"],
      })
    },
  })
}
