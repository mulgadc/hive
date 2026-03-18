import { z } from "zod"

// Versioned storage key - increment version when schema changes
const STORAGE_KEY = "hive:v1:aws-credentials"

export const awsCredentialsSchema = z.object({
  accessKeyId: z
    .string()
    .min(16, "Access Key ID must be at least 16 characters"),
  secretAccessKey: z.string().min(1, "Secret Access Key is required"),
})

export type AwsCredentials = z.infer<typeof awsCredentialsSchema>

// In-memory cache to avoid repeated localStorage reads
let credentialsCache: AwsCredentials | null | undefined

export function getCredentials(): AwsCredentials | null {
  // Return cached value if available
  if (credentialsCache !== undefined) {
    return credentialsCache
  }

  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (!stored) {
      credentialsCache = null
      return null
    }
    const parsed = JSON.parse(stored)
    const result = awsCredentialsSchema.safeParse(parsed)
    credentialsCache = result.success ? result.data : null
    return credentialsCache
  } catch {
    credentialsCache = null
    return null
  }
}

export function setCredentials(credentials: AwsCredentials): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(credentials))
    credentialsCache = credentials
  } catch {
    // localStorage might be full or disabled - cache in memory only
    credentialsCache = credentials
  }
}

export function clearCredentials(): void {
  try {
    localStorage.removeItem(STORAGE_KEY)
  } catch {
    // Ignore errors when clearing
  }
  credentialsCache = null
}
