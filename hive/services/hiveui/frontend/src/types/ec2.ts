import { z } from "zod"

export const createInstanceSchema = z.object({
  imageId: z.string("Please select an Image"),
  instanceType: z.string("Please select an instance type"),
  keyName: z.string("Please select a key pair"),
  count: z
    .int("Instance count must be a whole number")
    .min(1, "Instance count must be at least 1"),
})

export type CreateInstanceFormData = z.infer<typeof createInstanceSchema>

export type CreateInstanceParams = CreateInstanceFormData & {
  securityGroupIds: string[]
  subnetId: string
}

export const createKeyPairSchema = z.object({
  keyName: z
    .string()
    .min(1, "Key name is required")
    .max(255, "Key name must be 255 characters or less")
    .regex(
      /^[\w\s._\-:/()#,@[\]+=&;{}!$*]+$/,
      "Key name contains invalid characters",
    ),
})

export type CreateKeyPairData = z.infer<typeof createKeyPairSchema>

export const importKeyPairSchema = z.object({
  keyName: z
    .string()
    .min(1, "Key name is required")
    .max(255, "Key name must be 255 characters or less")
    .regex(
      /^[\w\s._\-:/()#,@[\]+=&;{}!$*]+$/,
      "Key name contains invalid characters",
    ),
  publicKeyMaterial: z
    .string()
    .min(1, "Public key is required")
    .refine((key) => key.trim().length > 0, "Public key cannot be empty"),
})

export type ImportKeyPairData = z.infer<typeof importKeyPairSchema>

export const createVolumeSchema = z.object({
  size: z
    .number()
    .int("Size must be a whole number")
    .min(1, "Size must be at least 1 GiB")
    .max(16_384, "Size must be at most 16384 GiB"),
  availabilityZone: z.string().min(1, "Availability zone is required"),
})

export type CreateVolumeFormData = z.infer<typeof createVolumeSchema>

export const modifyVolumeSchema = z.object({
  size: z
    .number()
    .int("Size must be a whole number")
    .min(1, "Size must be at least 1 GiB"),
})

export type ModifyVolumeFormData = z.infer<typeof modifyVolumeSchema>

export type ModifyVolumeParams = ModifyVolumeFormData & { volumeId: string }
