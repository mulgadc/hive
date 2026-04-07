import { EC2Client } from "@aws-sdk/client-ec2"
import { IAMClient } from "@aws-sdk/client-iam"
import { S3Client } from "@aws-sdk/client-s3"

import { getCredentials } from "./auth"

const AWS_REGION = "ap-southeast-2"

// SDK signs against the real backend host so the SigV4 signature includes
// the correct Host header value. Middleware below rewrites the outgoing URL
// to route through the same-origin reverse proxy.
const AWSGW_SIGN_ENDPOINT = `${window.location.protocol}//localhost:9999`
const S3_SIGN_ENDPOINT = `${window.location.protocol}//localhost:8443`

interface ProxyRequest {
  hostname?: string
  port?: number
  path?: string
}

interface MiddlewareAddable {
  // oxlint-disable-next-line typescript/no-explicit-any -- all AWS SDK clients share this shape but with incompatible generics
  middlewareStack: { add: (...args: any[]) => void }
}

// addProxyRewrite appends a finalizeRequest middleware that redirects the
// actual HTTP request through the UI's reverse proxy after signing is complete.
function addProxyRewrite(client: MiddlewareAddable, proxyPrefix: string): void {
  client.middlewareStack.add(
    // oxlint-disable-next-line typescript/no-explicit-any -- smithy middleware types are generic per-client
    (next: (args: any) => any) => (args: { request?: ProxyRequest }) => {
      const request = args.request
      if (request?.hostname) {
        request.hostname = window.location.hostname
        request.port = Number(window.location.port) || 443
        request.path = `${proxyPrefix}${request.path ?? "/"}`
      }
      // oxlint-disable-next-line typescript/no-unsafe-return -- smithy middleware return type is opaque
      return next(args)
    },
    { step: "finalizeRequest", name: "proxyRewrite", override: true },
  )
}

// Cached singleton clients
let ec2Client: EC2Client | null = null
let iamClient: IAMClient | null = null
let s3Client: S3Client | null = null

export function getEc2Client(): EC2Client {
  if (!ec2Client) {
    const credentials = getCredentials()
    if (!credentials) {
      throw new Error("AWS credentials not configured")
    }
    ec2Client = new EC2Client({
      endpoint: AWSGW_SIGN_ENDPOINT,
      region: AWS_REGION,
      credentials: {
        accessKeyId: credentials.accessKeyId,
        secretAccessKey: credentials.secretAccessKey,
      },
    })
    addProxyRewrite(ec2Client, "/proxy/awsgw")
  }
  return ec2Client
}

export function getIamClient(): IAMClient {
  if (!iamClient) {
    const credentials = getCredentials()
    if (!credentials) {
      throw new Error("AWS credentials not configured")
    }
    iamClient = new IAMClient({
      endpoint: AWSGW_SIGN_ENDPOINT,
      region: AWS_REGION,
      credentials: {
        accessKeyId: credentials.accessKeyId,
        secretAccessKey: credentials.secretAccessKey,
      },
    })
    addProxyRewrite(iamClient, "/proxy/awsgw")
  }
  return iamClient
}

export function getS3Client(): S3Client {
  if (!s3Client) {
    const credentials = getCredentials()
    if (!credentials) {
      throw new Error("AWS credentials not configured")
    }
    s3Client = new S3Client({
      endpoint: S3_SIGN_ENDPOINT,
      region: AWS_REGION,
      credentials: {
        accessKeyId: credentials.accessKeyId,
        secretAccessKey: credentials.secretAccessKey,
      },
      forcePathStyle: true,
    })
    // Remove trailing slashes from request paths to fix compatibility with
    // path-style S3 endpoints where a trailing slash causes the request to
    // be interpreted as GetObject instead of ListObjects
    s3Client.middlewareStack.use({
      applyToStack: (stack) => {
        stack.add(
          (next) => (args) => {
            // oxlint-disable-next-line typescript/no-unsafe-type-assertion -- smithy middleware args.request is typed as unknown
            const request = (args as { request?: { path?: string } }).request
            if (request?.path?.endsWith("/") && request.path !== "/") {
              request.path = request.path.slice(0, -1)
            }
            return next(args)
          },
          { step: "build", name: "removeTrailingSlash" },
        )
      },
    })
    addProxyRewrite(s3Client, "/proxy/s3")
  }
  return s3Client
}

// Call on logout to clear cached clients
export function clearClients(): void {
  ec2Client = null
  iamClient = null
  s3Client = null
}
