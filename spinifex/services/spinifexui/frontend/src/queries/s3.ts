import { ListBucketsCommand, ListObjectsV2Command } from "@aws-sdk/client-s3"
import { queryOptions } from "@tanstack/react-query"

import { getS3Client } from "@/lib/awsClient"

export const s3BucketsQueryOptions = queryOptions({
  queryKey: ["s3", "buckets"],
  queryFn: () => {
    const command = new ListBucketsCommand({})
    return getS3Client().send(command)
  },
})

export const s3BucketObjectsQueryOptions = (
  bucketName: string,
  prefix?: string,
) =>
  queryOptions({
    queryKey: ["s3", "buckets", bucketName, "objects", prefix ?? ""],
    queryFn: () => {
      const command = new ListObjectsV2Command({
        Bucket: bucketName,
        Prefix: prefix,
        Delimiter: "/",
      })
      return getS3Client().send(command)
    },
  })
