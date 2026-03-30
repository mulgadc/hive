import { Sha256 } from "@aws-crypto/sha256-browser"
import { HttpRequest } from "@smithy/protocol-http"
import { SignatureV4 } from "@smithy/signature-v4"

import type { AwsCredentials } from "./auth"

const AWS_REGION = "ap-southeast-2"
const GATEWAY_PORT = 9999

interface SignedFetchOptions {
  action: string
  credentials: AwsCredentials
  service?: string
}

export async function signedFetch<T>({
  action,
  credentials,
  service = "spinifex",
}: SignedFetchOptions): Promise<T> {
  const hostname = window.location.hostname
  const port = GATEWAY_PORT
  const protocol = window.location.protocol.replace(":", "")
  const body = `Action=${action}`

  const request = new HttpRequest({
    method: "POST",
    protocol,
    hostname,
    port,
    path: "/",
    headers: {
      host: `${hostname}:${port}`,
      "content-type": "application/x-www-form-urlencoded",
    },
    body,
  })

  const signer = new SignatureV4({
    credentials: {
      accessKeyId: credentials.accessKeyId,
      secretAccessKey: credentials.secretAccessKey,
    },
    region: AWS_REGION,
    service,
    sha256: Sha256,
  })

  const signed = await signer.sign(request)

  const headers: Record<string, string> = {}
  for (const [key, value] of Object.entries(signed.headers)) {
    if (typeof value === "string") {
      headers[key] = value
    }
  }

  const url = `${protocol}://${hostname}:${port}/`
  const response = await fetch(url, {
    method: "POST",
    headers,
    body,
  })

  if (!response.ok) {
    throw new Error(`${action} failed: ${response.status}`)
  }

  // oxlint-disable-next-line typescript/no-unsafe-type-assertion -- response.json() returns Promise<any>
  return response.json() as Promise<T>
}
