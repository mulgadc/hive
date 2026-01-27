import { EC2Client } from "@aws-sdk/client-ec2"
import { S3Client } from "@aws-sdk/client-s3"

import { getCredentials } from "./auth"

// Cached singleton clients
let ec2Client: EC2Client | null = null
let s3Client: S3Client | null = null

export function getEc2Client(): EC2Client {
  if (!ec2Client) {
    const credentials = getCredentials()
    if (!credentials) {
      throw new Error("AWS credentials not configured")
    }
    ec2Client = new EC2Client({
      endpoint: "https://localhost:9999",
      region: "ap-southeast-2",
      credentials: {
        accessKeyId: credentials.accessKeyId,
        secretAccessKey: credentials.secretAccessKey,
      },
    })
  }
  return ec2Client
}

export function getS3Client(): S3Client {
  if (!s3Client) {
    const credentials = getCredentials()
    if (!credentials) {
      throw new Error("AWS credentials not configured")
    }
    s3Client = new S3Client({
      endpoint: "https://localhost:8443",
      region: "ap-southeast-2",
      credentials: {
        accessKeyId: credentials.accessKeyId,
        secretAccessKey: credentials.secretAccessKey,
      },
      forcePathStyle: true,
    })
  }
  return s3Client
}

// Call on logout to clear cached clients
export function clearClients(): void {
  ec2Client = null
  s3Client = null
}
