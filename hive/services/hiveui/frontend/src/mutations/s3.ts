import {
  AbortMultipartUploadCommand,
  CompleteMultipartUploadCommand,
  CreateMultipartUploadCommand,
  DeleteObjectCommand,
  PutObjectCommand,
  UploadPartCommand,
} from "@aws-sdk/client-s3"
import { useMutation, useQueryClient } from "@tanstack/react-query"

import { getS3Client } from "@/lib/awsClient"

const MULTIPART_THRESHOLD = 4 * 1024 * 1024 // 4 MB
const PART_SIZE = 5 * 1024 * 1024 // 5 MB per part

interface CompletedPart {
  ETag: string
  PartNumber: number
}

interface UploadObjectParams {
  bucket: string
  key: string
  file: File
}

async function uploadMultipart(bucket: string, key: string, file: File) {
  const client = getS3Client()

  const { UploadId } = await client.send(
    new CreateMultipartUploadCommand({
      Bucket: bucket,
      Key: key,
      ContentType: file.type,
    }),
  )

  if (!UploadId) {
    throw new Error("Failed to initialize multipart upload: missing UploadId")
  }

  const completedParts: CompletedPart[] = []

  try {
    for (let offset = 0, partNumber = 1; offset < file.size; partNumber++) {
      const end = Math.min(offset + PART_SIZE, file.size)
      const body = new Uint8Array(await file.slice(offset, end).arrayBuffer())

      const { ETag } = await client.send(
        new UploadPartCommand({
          Bucket: bucket,
          Key: key,
          UploadId,
          PartNumber: partNumber,
          Body: body,
        }),
      )

      completedParts.push({ ETag: ETag ?? "", PartNumber: partNumber })
      offset = end
    }

    return await client.send(
      new CompleteMultipartUploadCommand({
        Bucket: bucket,
        Key: key,
        UploadId,
        MultipartUpload: { Parts: completedParts },
      }),
    )
  } catch (error) {
    await client.send(
      new AbortMultipartUploadCommand({ Bucket: bucket, Key: key, UploadId }),
    )
    throw error
  }
}

export function useUploadObject() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async ({ bucket, key, file }: UploadObjectParams) => {
      if (file.size > MULTIPART_THRESHOLD) {
        return await uploadMultipart(bucket, key, file)
      }

      const body = new Uint8Array(await file.arrayBuffer())

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
