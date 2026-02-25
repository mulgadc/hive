import { z } from "zod"

export const createInstanceSchema = z.object({
  imageId: z.string("Please select an Image"),
  instanceType: z.string("Please select an instance type"),
  keyName: z.string("Please select a key pair"),
  subnetId: z.string().optional(),
  count: z
    .int("Instance count must be a whole number")
    .min(1, "Instance count must be at least 1"),
})

export type CreateInstanceFormData = z.infer<typeof createInstanceSchema>

export type CreateInstanceParams = CreateInstanceFormData

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

export const createSnapshotSchema = z.object({
  volumeId: z.string().min(1, "Volume is required"),
  description: z.string().optional(),
})

export type CreateSnapshotFormData = z.infer<typeof createSnapshotSchema>

export const copySnapshotSchema = z.object({
  sourceSnapshotId: z.string().min(1, "Source snapshot is required"),
  sourceRegion: z.string().min(1, "Source region is required"),
  description: z.string().optional(),
})

export type CopySnapshotFormData = z.infer<typeof copySnapshotSchema>

export const attachVolumeSchema = z.object({
  volumeId: z.string().min(1, "Volume is required"),
  instanceId: z.string().min(1, "Instance is required"),
  device: z.string().optional(),
})

export type AttachVolumeFormData = z.infer<typeof attachVolumeSchema>

export const detachVolumeSchema = z.object({
  volumeId: z.string().min(1, "Volume is required"),
  instanceId: z.string().optional(),
  force: z.boolean().optional(),
})

export type DetachVolumeFormData = z.infer<typeof detachVolumeSchema>

export const modifyInstanceTypeSchema = z.object({
  instanceType: z.string().min(1, "Instance type is required"),
})

export type ModifyInstanceTypeFormData = z.infer<
  typeof modifyInstanceTypeSchema
>

export const createImageSchema = z.object({
  name: z.string().min(1, "Name is required"),
  description: z.string().optional(),
})

export type CreateImageFormData = z.infer<typeof createImageSchema>

export type CreateImageParams = CreateImageFormData & { instanceId: string }

export const createSubnetSchema = z.object({
  vpcId: z.string().min(1, "VPC is required"),
  cidrBlock: z
    .string()
    .min(1, "CIDR block is required")
    .regex(
      /^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\/\d{1,2}$/,
      "Must be a valid CIDR block (e.g. 10.0.1.0/24)",
    ),
  availabilityZone: z.string().optional(),
})

export type CreateSubnetFormData = z.infer<typeof createSubnetSchema>

export const createVpcSchema = z.object({
  cidrBlock: z
    .string()
    .min(1, "CIDR block is required")
    .regex(
      /^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\/\d{1,2}$/,
      "Must be a valid CIDR block (e.g. 10.0.0.0/16)",
    ),
  name: z.string().optional(),
})

export type CreateVpcFormData = z.infer<typeof createVpcSchema>
