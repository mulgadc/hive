import { ListBucketsCommand, ListObjectsV2Command } from "@aws-sdk/client-s3"
import { queryOptions } from "@tanstack/react-query"

import { getS3Client } from "@/lib/awsClient"

export const s3BucketsQueryOptions = queryOptions({
  queryKey: ["s3", "buckets"],
  queryFn: async () => {
    try {
      const command = new ListBucketsCommand({})
      return await getS3Client().send(command)
    } catch {
      throw new Error("Failed to fetch S3 buckets")
    }
  },
  staleTime: 5000,
})

export const s3BucketObjectsQueryOptions = (
  bucketName: string,
  prefix?: string,
) =>
  queryOptions({
    queryKey: ["s3", "buckets", bucketName, "objects", prefix ?? ""],
    queryFn: async () => {
      try {
        const command = new ListObjectsV2Command({
          Bucket: bucketName,
          Prefix: prefix,
          Delimiter: "/",
        })
        return await getS3Client().send(command)
      } catch {
        throw new Error("Failed to fetch S3 bucket objects")
      }
    },
    staleTime: 5000,
  })
