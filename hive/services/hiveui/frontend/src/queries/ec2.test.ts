import { describe, expect, it, vi } from "vitest"

const mockSend = vi.fn().mockResolvedValue({})

vi.mock("@/lib/awsClient", () => ({
  getEc2Client: () => ({ send: mockSend }),
}))

import {
  ec2AvailabilityZonesQueryOptions,
  ec2ImageQueryOptions,
  ec2ImagesQueryOptions,
  ec2InstanceQueryOptions,
  ec2InstancesQueryOptions,
  ec2InstanceTypesQueryOptions,
  ec2KeyPairQueryOptions,
  ec2KeyPairsQueryOptions,
  ec2RegionsQueryOptions,
  ec2SnapshotQueryOptions,
  ec2SnapshotsQueryOptions,
  ec2SubnetQueryOptions,
  ec2SubnetsQueryOptions,
  ec2VolumeQueryOptions,
  ec2VolumesQueryOptions,
  ec2VpcQueryOptions,
  ec2VpcsQueryOptions,
} from "./ec2"

describe("query keys", () => {
  it("ec2InstancesQueryOptions has correct key", () => {
    expect(ec2InstancesQueryOptions.queryKey).toEqual(["ec2", "instances"])
  })

  it("ec2InstanceQueryOptions includes instanceId in key", () => {
    expect(ec2InstanceQueryOptions("i-123").queryKey).toEqual([
      "ec2",
      "instances",
      "i-123",
    ])
  })

  it("ec2ImagesQueryOptions has correct key", () => {
    expect(ec2ImagesQueryOptions.queryKey).toEqual(["ec2", "images"])
  })

  it("ec2ImageQueryOptions uses 'none' for undefined imageId", () => {
    expect(ec2ImageQueryOptions(undefined).queryKey).toEqual([
      "ec2",
      "images",
      "none",
    ])
  })

  it("ec2KeyPairsQueryOptions has correct key", () => {
    expect(ec2KeyPairsQueryOptions.queryKey).toEqual(["ec2", "keypairs"])
  })

  it("ec2KeyPairQueryOptions includes keyPairId", () => {
    expect(ec2KeyPairQueryOptions("kp-abc").queryKey).toEqual([
      "ec2",
      "keypairs",
      "kp-abc",
    ])
  })

  it("ec2VolumesQueryOptions has correct key", () => {
    expect(ec2VolumesQueryOptions.queryKey).toEqual(["ec2", "volumes"])
  })

  it("ec2VolumeQueryOptions includes volumeId", () => {
    expect(ec2VolumeQueryOptions("vol-1").queryKey).toEqual([
      "ec2",
      "volumes",
      "vol-1",
    ])
  })

  it("ec2SnapshotsQueryOptions has correct key", () => {
    expect(ec2SnapshotsQueryOptions.queryKey).toEqual(["ec2", "snapshots"])
  })

  it("ec2SnapshotQueryOptions includes snapshotId", () => {
    expect(ec2SnapshotQueryOptions("snap-1").queryKey).toEqual([
      "ec2",
      "snapshots",
      "snap-1",
    ])
  })

  it("ec2VpcsQueryOptions has correct key", () => {
    expect(ec2VpcsQueryOptions.queryKey).toEqual(["ec2", "vpcs"])
  })

  it("ec2VpcQueryOptions includes vpcId", () => {
    expect(ec2VpcQueryOptions("vpc-1").queryKey).toEqual([
      "ec2",
      "vpcs",
      "vpc-1",
    ])
  })

  it("ec2SubnetsQueryOptions has correct key", () => {
    expect(ec2SubnetsQueryOptions.queryKey).toEqual(["ec2", "subnets"])
  })

  it("ec2SubnetQueryOptions includes subnetId", () => {
    expect(ec2SubnetQueryOptions("subnet-1").queryKey).toEqual([
      "ec2",
      "subnets",
      "subnet-1",
    ])
  })

  it("ec2InstanceTypesQueryOptions has correct key", () => {
    expect(ec2InstanceTypesQueryOptions.queryKey).toEqual([
      "ec2",
      "instances",
      "types",
    ])
  })
})

describe("staleTime and refetchInterval", () => {
  it("availability zones use staleTime", () => {
    expect(ec2AvailabilityZonesQueryOptions.staleTime).toBe(300_000)
  })

  it("regions use staleTime", () => {
    expect(ec2RegionsQueryOptions.staleTime).toBe(300_000)
  })

  it("instances refetch on interval", () => {
    expect(ec2InstancesQueryOptions.refetchInterval).toBe(5000)
  })

  it("volumes refetch on interval", () => {
    expect(ec2VolumesQueryOptions.refetchInterval).toBe(5000)
  })

  it("snapshots refetch on interval", () => {
    expect(ec2SnapshotsQueryOptions.refetchInterval).toBe(5000)
  })

  it("instance types refetch on interval", () => {
    expect(ec2InstanceTypesQueryOptions.refetchInterval).toBe(5000)
  })
})

describe("queryFn", () => {
  it("ec2ImageQueryOptions returns empty result for undefined imageId", async () => {
    const options = ec2ImageQueryOptions(undefined)
    const queryFn = options.queryFn as (ctx: never) => Promise<unknown>
    const result = await queryFn({} as never)
    expect(result).toEqual({ Images: [], $metadata: {} })
    expect(mockSend).not.toHaveBeenCalled()
  })
})
