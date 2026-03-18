import { describe, expect, it } from "vitest"

import {
  attachVolumeSchema,
  copySnapshotSchema,
  createInstanceSchema,
  createKeyPairSchema,
  createSnapshotSchema,
  createSubnetSchema,
  createVolumeSchema,
  createVpcSchema,
  importKeyPairSchema,
  modifyVolumeSchema,
} from "./ec2"

describe("createInstanceSchema", () => {
  it("accepts valid instance params", () => {
    const result = createInstanceSchema.safeParse({
      imageId: "ami-123",
      instanceType: "t2.micro",
      keyName: "my-key",
      count: 1,
    })
    expect(result.success).toBe(true)
  })

  it("requires count to be at least 1", () => {
    const result = createInstanceSchema.safeParse({
      imageId: "ami-123",
      instanceType: "t2.micro",
      keyName: "my-key",
      count: 0,
    })
    expect(result.success).toBe(false)
  })

  it("requires count to be an integer", () => {
    const result = createInstanceSchema.safeParse({
      imageId: "ami-123",
      instanceType: "t2.micro",
      keyName: "my-key",
      count: 1.5,
    })
    expect(result.success).toBe(false)
  })

  it("allows optional subnetId", () => {
    const result = createInstanceSchema.safeParse({
      imageId: "ami-123",
      instanceType: "t2.micro",
      keyName: "my-key",
      count: 1,
      subnetId: "subnet-abc",
    })
    expect(result.success).toBe(true)
  })

  it("supports capacity refine", () => {
    const refined = createInstanceSchema.refine((data) => data.count <= 3, {
      message: "Cannot exceed available capacity",
      path: ["count"],
    })
    const result = refined.safeParse({
      imageId: "ami-123",
      instanceType: "t2.micro",
      keyName: "my-key",
      count: 5,
    })
    expect(result.success).toBe(false)
    if (!result.success) {
      expect(result.error.issues[0]?.message).toBe(
        "Cannot exceed available capacity",
      )
    }
  })
})

describe("createKeyPairSchema", () => {
  it("accepts a valid key name", () => {
    const result = createKeyPairSchema.safeParse({ keyName: "my-key" })
    expect(result.success).toBe(true)
  })

  it("rejects empty key name", () => {
    const result = createKeyPairSchema.safeParse({ keyName: "" })
    expect(result.success).toBe(false)
  })

  it("rejects key name over 255 chars", () => {
    const result = createKeyPairSchema.safeParse({ keyName: "a".repeat(256) })
    expect(result.success).toBe(false)
  })
})

describe("importKeyPairSchema", () => {
  it("accepts valid key pair import", () => {
    const result = importKeyPairSchema.safeParse({
      keyName: "my-key",
      publicKeyMaterial: "ssh-rsa AAAAB3Nza...",
    })
    expect(result.success).toBe(true)
  })

  it("rejects empty public key", () => {
    const result = importKeyPairSchema.safeParse({
      keyName: "my-key",
      publicKeyMaterial: "",
    })
    expect(result.success).toBe(false)
  })

  it("rejects whitespace-only public key", () => {
    const result = importKeyPairSchema.safeParse({
      keyName: "my-key",
      publicKeyMaterial: "   ",
    })
    expect(result.success).toBe(false)
  })
})

describe("createVolumeSchema", () => {
  it("accepts valid volume params", () => {
    const result = createVolumeSchema.safeParse({
      size: 10,
      availabilityZone: "us-east-1a",
    })
    expect(result.success).toBe(true)
  })

  it("rejects size below 1", () => {
    const result = createVolumeSchema.safeParse({
      size: 0,
      availabilityZone: "us-east-1a",
    })
    expect(result.success).toBe(false)
  })

  it("rejects size above 16384", () => {
    const result = createVolumeSchema.safeParse({
      size: 16_385,
      availabilityZone: "us-east-1a",
    })
    expect(result.success).toBe(false)
  })

  it("rejects fractional size", () => {
    const result = createVolumeSchema.safeParse({
      size: 10.5,
      availabilityZone: "us-east-1a",
    })
    expect(result.success).toBe(false)
  })
})

describe("modifyVolumeSchema", () => {
  it("accepts valid size", () => {
    const result = modifyVolumeSchema.safeParse({ size: 20 })
    expect(result.success).toBe(true)
  })

  it("rejects size below 1", () => {
    const result = modifyVolumeSchema.safeParse({ size: 0 })
    expect(result.success).toBe(false)
  })
})

describe("createVpcSchema", () => {
  it("accepts valid CIDR block", () => {
    const result = createVpcSchema.safeParse({ cidrBlock: "10.0.0.0/16" })
    expect(result.success).toBe(true)
  })

  it("accepts CIDR block with optional name", () => {
    const result = createVpcSchema.safeParse({
      cidrBlock: "10.0.0.0/16",
      name: "my-vpc",
    })
    expect(result.success).toBe(true)
  })

  it("rejects invalid CIDR format", () => {
    const result = createVpcSchema.safeParse({ cidrBlock: "not-a-cidr" })
    expect(result.success).toBe(false)
  })

  it("rejects empty CIDR block", () => {
    const result = createVpcSchema.safeParse({ cidrBlock: "" })
    expect(result.success).toBe(false)
  })
})

describe("createSubnetSchema", () => {
  it("accepts valid subnet params", () => {
    const result = createSubnetSchema.safeParse({
      vpcId: "vpc-123",
      cidrBlock: "10.0.1.0/24",
    })
    expect(result.success).toBe(true)
  })

  it("rejects invalid CIDR block", () => {
    const result = createSubnetSchema.safeParse({
      vpcId: "vpc-123",
      cidrBlock: "invalid",
    })
    expect(result.success).toBe(false)
  })

  it("allows optional availability zone", () => {
    const result = createSubnetSchema.safeParse({
      vpcId: "vpc-123",
      cidrBlock: "10.0.1.0/24",
      availabilityZone: "us-east-1a",
    })
    expect(result.success).toBe(true)
  })
})

describe("createSnapshotSchema", () => {
  it("accepts valid snapshot params", () => {
    const result = createSnapshotSchema.safeParse({ volumeId: "vol-123" })
    expect(result.success).toBe(true)
  })

  it("rejects empty volumeId", () => {
    const result = createSnapshotSchema.safeParse({ volumeId: "" })
    expect(result.success).toBe(false)
  })
})

describe("copySnapshotSchema", () => {
  it("accepts valid copy params", () => {
    const result = copySnapshotSchema.safeParse({
      sourceSnapshotId: "snap-123",
      sourceRegion: "us-east-1",
    })
    expect(result.success).toBe(true)
  })
})

describe("attachVolumeSchema", () => {
  it("accepts valid attach params", () => {
    const result = attachVolumeSchema.safeParse({
      volumeId: "vol-123",
      instanceId: "i-123",
    })
    expect(result.success).toBe(true)
  })

  it("rejects empty instanceId", () => {
    const result = attachVolumeSchema.safeParse({
      volumeId: "vol-123",
      instanceId: "",
    })
    expect(result.success).toBe(false)
  })
})
