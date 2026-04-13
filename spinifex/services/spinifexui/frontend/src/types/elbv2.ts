import { z } from "zod"

const LB_TG_NAME_REGEX = /^(?:[a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9])$/

const lbNameField = z
  .string()
  .min(1, "Name is required")
  .max(32, "Name must be 32 characters or less")
  .regex(
    LB_TG_NAME_REGEX,
    "Name may contain only letters, digits, and hyphens; must start and end with alphanumeric",
  )
  .refine(
    (value) => !value.startsWith("internal-"),
    "Name cannot start with 'internal-'",
  )

const tgNameField = z
  .string()
  .min(1, "Name is required")
  .max(32, "Name must be 32 characters or less")
  .regex(
    LB_TG_NAME_REGEX,
    "Name may contain only letters, digits, and hyphens; must start and end with alphanumeric",
  )

const portField = z
  .number()
  .int("Port must be a whole number")
  .min(1, "Port must be at least 1")
  .max(65_535, "Port must be at most 65535")

export const tagSchema = z.object({
  key: z.string().min(1, "Tag key is required").max(128),
  value: z.string().max(256).default(""),
})

export type TagFormData = z.infer<typeof tagSchema>

// ALB health-check config. NLB variant lands with slice 9.
export const healthCheckSchema = z.object({
  protocol: z.enum(["HTTP"]).default("HTTP"),
  path: z.string().min(1, "Path is required").max(1024).default("/"),
  port: z.string().min(1).default("traffic-port"),
  intervalSeconds: z.number().int().min(5).max(300).default(30),
  timeoutSeconds: z.number().int().min(2).max(120).default(5),
  healthyThresholdCount: z.number().int().min(2).max(10).default(5),
  unhealthyThresholdCount: z.number().int().min(2).max(10).default(2),
  matcher: z
    .string()
    .regex(
      /^\d{3}(?:[-,]\d{3})*$/,
      "Matcher must be HTTP codes like 200 or 200-299 or 200,201",
    )
    .default("200"),
})

export type HealthCheckFormData = z.infer<typeof healthCheckSchema>

export const createTargetGroupSchema = z.object({
  name: tgNameField,
  protocol: z.enum(["HTTP"]).default("HTTP"),
  port: portField,
  vpcId: z.string().min(1, "VPC is required"),
  healthCheck: healthCheckSchema,
  tags: z.array(tagSchema).default([]),
})

export type CreateTargetGroupFormData = z.infer<typeof createTargetGroupSchema>

export const createListenerSchema = z.object({
  protocol: z.enum(["HTTP"]).default("HTTP"),
  port: portField,
  defaultTargetGroupArn: z.string().min(1, "Target group is required"),
})

export type CreateListenerFormData = z.infer<typeof createListenerSchema>

export const createLoadBalancerSchema = z.object({
  name: lbNameField,
  scheme: z.enum(["internet-facing", "internal"]).default("internet-facing"),
  vpcId: z.string().min(1, "VPC is required"),
  subnetIds: z
    .array(z.string())
    .min(2, "At least 2 subnets are required for an ALB"),
  securityGroupIds: z.array(z.string()).default([]),
  tags: z.array(tagSchema).default([]),
  listener: z
    .object({
      protocol: z.enum(["HTTP"]).default("HTTP"),
      port: portField.default(80),
      targetGroupMode: z.enum(["new", "existing"]).default("new"),
      existingTargetGroupArn: z.string().optional(),
      newTargetGroup: createTargetGroupSchema.optional(),
    })
    .refine(
      (value) =>
        value.targetGroupMode === "new"
          ? value.newTargetGroup !== undefined
          : !!value.existingTargetGroupArn,
      { error: "Target group selection is required" },
    ),
})

export type CreateLoadBalancerFormData = z.infer<
  typeof createLoadBalancerSchema
>

export const registerTargetsSchema = z.object({
  targets: z
    .array(
      z.object({
        instanceId: z.string().min(1, "Instance is required"),
        port: portField.optional(),
      }),
    )
    .min(1, "At least one target is required"),
})

export type RegisterTargetsFormData = z.infer<typeof registerTargetsSchema>

// Attributes editor state — free-form key/value pairs. Slice 7 narrows keys to
// DefaultLoadBalancerAttributes / DefaultTargetGroupAttributes.
export const attributesSchema = z.record(z.string(), z.string())

export type AttributesFormData = z.infer<typeof attributesSchema>
