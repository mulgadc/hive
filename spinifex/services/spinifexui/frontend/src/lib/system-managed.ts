import type { Image } from "@aws-sdk/client-ec2"

// Tag key applied by Spinifex to platform-managed resources
// (HAProxy VMs, their ENIs, the LB AMI). The value identifies the
// owning component (e.g. "elbv2"). See spinifex/docs/TAG-CONVENTIONS.md.
export const SYSTEM_MANAGED_TAG_KEY = "spinifex:managed-by"

// Value of SYSTEM_MANAGED_TAG_KEY carried by the LB system AMI and its VMs.
export const LB_MANAGED_BY_VALUE = "elbv2"

// Canonical name of the LB system image in utils.AvailableImages.
// Kept in sync with spinifex/utils/images.go.
export const LB_IMAGE_NAME = "lb-alpine-3.21.6-x86_64"

export function isSystemManagedImage(image: Image): boolean {
  return image.Tags?.some((tag) => tag.Key === SYSTEM_MANAGED_TAG_KEY) ?? false
}

export function isLbImage(image: Image): boolean {
  return (
    image.Tags?.some(
      (tag) =>
        tag.Key === SYSTEM_MANAGED_TAG_KEY && tag.Value === LB_MANAGED_BY_VALUE,
    ) ?? false
  )
}

export function hasLbImage(images: Image[]): boolean {
  return images.some(isLbImage)
}
