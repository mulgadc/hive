import { z } from "zod"

const STORAGE_KEY = "aws-credentials"

export const awsCredentialsSchema = z.object({
  accessKeyId: z
    .string()
    .min(16, "Access Key ID must be at least 16 characters"),
  secretAccessKey: z.string().min(1, "Secret Access Key is required"),
})

export type AwsCredentials = z.infer<typeof awsCredentialsSchema>

export function getCredentials(): AwsCredentials | null {
  const stored = localStorage.getItem(STORAGE_KEY)
  if (!stored) {
    return null
  }
  try {
    const parsed = JSON.parse(stored)
    const result = awsCredentialsSchema.safeParse(parsed)
    return result.success ? result.data : null
  } catch {
    return null
  }
}

export function setCredentials(credentials: AwsCredentials): void {
  localStorage.setItem(STORAGE_KEY, JSON.stringify(credentials))
}

export function clearCredentials(): void {
  localStorage.removeItem(STORAGE_KEY)
}
