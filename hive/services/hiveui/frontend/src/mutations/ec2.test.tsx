import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { renderHook, waitFor } from "@testing-library/react"
import type { ReactNode } from "react"
import { afterEach, describe, expect, it, vi } from "vitest"

const mockSend = vi.fn().mockResolvedValue({})

vi.mock("@/lib/awsClient", () => ({
  getEc2Client: () => ({ send: mockSend }),
}))

import {
  useAttachVolume,
  useCopySnapshot,
  useCreateImage,
  useCreateInstance,
  useCreateKeyPair,
  useCreateSnapshot,
  useCreateSubnet,
  useCreateVolume,
  useCreateVpc,
  useDeleteKeyPair,
  useDeleteSnapshot,
  useDeleteSubnet,
  useDeleteVolume,
  useDeleteVpc,
  useDetachVolume,
  useGetConsoleOutput,
  useImportKeyPair,
  useModifyInstanceAttribute,
  useModifyVolume,
  useRebootInstance,
  useStartInstance,
  useStopInstance,
  useTerminateInstance,
} from "./ec2"

let queryClient: QueryClient

function wrapper({ children }: { children: ReactNode }) {
  return (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
}

afterEach(() => {
  mockSend.mockClear()
})

function createQueryClient() {
  queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
  return queryClient
}

describe("useStartInstance", () => {
  it("sends StartInstancesCommand with the instance ID", async () => {
    createQueryClient()
    const { result } = renderHook(() => useStartInstance(), { wrapper })

    result.current.mutate("i-abc123")

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend).toHaveBeenCalledOnce()

    const command = mockSend.mock.calls[0]?.[0]
    expect(command.input).toEqual({ InstanceIds: ["i-abc123"] })
  })

  it("invalidates instances query on success", async () => {
    createQueryClient()
    const spy = vi.spyOn(queryClient, "invalidateQueries")
    const { result } = renderHook(() => useStartInstance(), { wrapper })

    result.current.mutate("i-abc123")

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(spy).toHaveBeenCalledWith({ queryKey: ["ec2", "instances"] })
  })
})

describe("useStopInstance", () => {
  it("sends StopInstancesCommand with the instance ID", async () => {
    createQueryClient()
    const { result } = renderHook(() => useStopInstance(), { wrapper })

    result.current.mutate("i-abc123")

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      InstanceIds: ["i-abc123"],
    })
  })
})

describe("useTerminateInstance", () => {
  it("sends TerminateInstancesCommand with the instance ID", async () => {
    createQueryClient()
    const { result } = renderHook(() => useTerminateInstance(), { wrapper })

    result.current.mutate("i-abc123")

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      InstanceIds: ["i-abc123"],
    })
  })
})

describe("useCreateInstance", () => {
  it("sends RunInstancesCommand with form data", async () => {
    createQueryClient()
    const { result } = renderHook(() => useCreateInstance(), { wrapper })

    result.current.mutate({
      imageId: "ami-123",
      instanceType: "t2.micro",
      keyName: "my-key",
      count: 2,
      subnetId: "subnet-1",
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      ImageId: "ami-123",
      InstanceType: "t2.micro",
      KeyName: "my-key",
      MinCount: 2,
      MaxCount: 2,
      SubnetId: "subnet-1",
    })
  })

  it("omits SubnetId when empty string", async () => {
    createQueryClient()
    const { result } = renderHook(() => useCreateInstance(), { wrapper })

    result.current.mutate({
      imageId: "ami-123",
      instanceType: "t2.micro",
      keyName: "my-key",
      count: 1,
      subnetId: "",
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input.SubnetId).toBeUndefined()
  })
})

describe("useImportKeyPair", () => {
  it("strips the comment from an SSH public key", async () => {
    createQueryClient()
    const { result } = renderHook(() => useImportKeyPair(), { wrapper })

    result.current.mutate({
      keyName: "my-key",
      publicKeyMaterial: "ssh-rsa AAAAB3Nza... user@host",
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    const input = mockSend.mock.calls[0]?.[0].input
    expect(input.KeyName).toBe("my-key")

    const decoded = new TextDecoder().decode(input.PublicKeyMaterial)
    expect(decoded).toBe("ssh-rsa AAAAB3Nza...")
    expect(decoded).not.toContain("user@host")
  })

  it("handles keys without comments", async () => {
    createQueryClient()
    const { result } = renderHook(() => useImportKeyPair(), { wrapper })

    result.current.mutate({
      keyName: "my-key",
      publicKeyMaterial: "ssh-rsa AAAAB3Nza...",
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    const decoded = new TextDecoder().decode(
      mockSend.mock.calls[0]?.[0].input.PublicKeyMaterial,
    )
    expect(decoded).toBe("ssh-rsa AAAAB3Nza...")
  })

  it("handles extra whitespace in key", async () => {
    createQueryClient()
    const { result } = renderHook(() => useImportKeyPair(), { wrapper })

    result.current.mutate({
      keyName: "my-key",
      publicKeyMaterial: "  ssh-rsa   AAAAB3Nza...   user@host  ",
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    const decoded = new TextDecoder().decode(
      mockSend.mock.calls[0]?.[0].input.PublicKeyMaterial,
    )
    expect(decoded).toBe("ssh-rsa AAAAB3Nza...")
  })
})

describe("useRebootInstance", () => {
  it("sends RebootInstancesCommand with the instance ID", async () => {
    createQueryClient()
    const { result } = renderHook(() => useRebootInstance(), { wrapper })

    result.current.mutate("i-abc123")

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      InstanceIds: ["i-abc123"],
    })
  })
})

describe("useCreateKeyPair", () => {
  it("sends CreateKeyPairCommand with key name and rsa type", async () => {
    createQueryClient()
    const { result } = renderHook(() => useCreateKeyPair(), { wrapper })

    result.current.mutate({ keyName: "my-key" })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      KeyName: "my-key",
      KeyType: "rsa",
    })
  })
})

describe("useDeleteKeyPair", () => {
  it("sends DeleteKeyPairCommand with key pair ID", async () => {
    createQueryClient()
    const { result } = renderHook(() => useDeleteKeyPair(), { wrapper })

    result.current.mutate("kp-abc123")

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      KeyPairId: "kp-abc123",
    })
  })
})

describe("useCreateVolume", () => {
  it("sends CreateVolumeCommand with size and availability zone", async () => {
    createQueryClient()
    const { result } = renderHook(() => useCreateVolume(), { wrapper })

    result.current.mutate({ size: 100, availabilityZone: "us-east-1a" })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      Size: 100,
      AvailabilityZone: "us-east-1a",
      VolumeType: "gp3",
    })
  })
})

describe("useModifyVolume", () => {
  it("sends ModifyVolumeCommand with volume ID and new size", async () => {
    createQueryClient()
    const { result } = renderHook(() => useModifyVolume(), { wrapper })

    result.current.mutate({ volumeId: "vol-123", size: 200 })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      VolumeId: "vol-123",
      Size: 200,
    })
  })
})

describe("useDeleteVolume", () => {
  it("sends DeleteVolumeCommand with volume ID", async () => {
    createQueryClient()
    const { result } = renderHook(() => useDeleteVolume(), { wrapper })

    result.current.mutate("vol-123")

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      VolumeId: "vol-123",
    })
  })
})

describe("useCreateSnapshot", () => {
  it("sends CreateSnapshotCommand with volume ID and description", async () => {
    createQueryClient()
    const { result } = renderHook(() => useCreateSnapshot(), { wrapper })

    result.current.mutate({ volumeId: "vol-123", description: "backup" })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      VolumeId: "vol-123",
      Description: "backup",
    })
  })

  it("omits Description when empty", async () => {
    createQueryClient()
    const { result } = renderHook(() => useCreateSnapshot(), { wrapper })

    result.current.mutate({ volumeId: "vol-123", description: "" })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input.Description).toBeUndefined()
  })
})

describe("useDeleteSnapshot", () => {
  it("sends DeleteSnapshotCommand with snapshot ID", async () => {
    createQueryClient()
    const { result } = renderHook(() => useDeleteSnapshot(), { wrapper })

    result.current.mutate("snap-123")

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      SnapshotId: "snap-123",
    })
  })
})

describe("useCopySnapshot", () => {
  it("sends CopySnapshotCommand with source details", async () => {
    createQueryClient()
    const { result } = renderHook(() => useCopySnapshot(), { wrapper })

    result.current.mutate({
      sourceSnapshotId: "snap-123",
      sourceRegion: "us-east-1",
      description: "copy",
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      SourceSnapshotId: "snap-123",
      SourceRegion: "us-east-1",
      Description: "copy",
    })
  })
})

describe("useAttachVolume", () => {
  it("sends AttachVolumeCommand with volume, instance, and device", async () => {
    createQueryClient()
    const { result } = renderHook(() => useAttachVolume(), { wrapper })

    result.current.mutate({
      volumeId: "vol-123",
      instanceId: "i-abc",
      device: "/dev/sdf",
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      VolumeId: "vol-123",
      InstanceId: "i-abc",
      Device: "/dev/sdf",
    })
  })
})

describe("useDetachVolume", () => {
  it("sends DetachVolumeCommand with volume, instance, and force", async () => {
    createQueryClient()
    const { result } = renderHook(() => useDetachVolume(), { wrapper })

    result.current.mutate({
      volumeId: "vol-123",
      instanceId: "i-abc",
      force: true,
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      VolumeId: "vol-123",
      InstanceId: "i-abc",
      Force: true,
    })
  })
})

describe("useModifyInstanceAttribute", () => {
  it("sends ModifyInstanceAttributeCommand with instance type", async () => {
    createQueryClient()
    const { result } = renderHook(() => useModifyInstanceAttribute(), {
      wrapper,
    })

    result.current.mutate({
      instanceId: "i-abc",
      instanceType: "t3.large",
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      InstanceId: "i-abc",
      InstanceType: { Value: "t3.large" },
    })
  })
})

describe("useGetConsoleOutput", () => {
  it("sends GetConsoleOutputCommand with instance ID", async () => {
    createQueryClient()
    const { result } = renderHook(() => useGetConsoleOutput(), { wrapper })

    result.current.mutate("i-abc123")

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      InstanceId: "i-abc123",
    })
  })
})

describe("useCreateImage", () => {
  it("sends CreateImageCommand with instance ID and name", async () => {
    createQueryClient()
    const { result } = renderHook(() => useCreateImage(), { wrapper })

    result.current.mutate({
      instanceId: "i-abc",
      name: "my-image",
      description: "test image",
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      InstanceId: "i-abc",
      Name: "my-image",
      Description: "test image",
    })
  })
})

describe("useCreateVpc", () => {
  it("includes TagSpecifications when name is provided", async () => {
    createQueryClient()
    const { result } = renderHook(() => useCreateVpc(), { wrapper })

    result.current.mutate({ cidrBlock: "10.0.0.0/16", name: "my-vpc" })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      CidrBlock: "10.0.0.0/16",
      TagSpecifications: [
        {
          ResourceType: "vpc",
          Tags: [{ Key: "Name", Value: "my-vpc" }],
        },
      ],
    })
  })

  it("omits TagSpecifications when name is empty", async () => {
    createQueryClient()
    const { result } = renderHook(() => useCreateVpc(), { wrapper })

    result.current.mutate({ cidrBlock: "10.0.0.0/16", name: "" })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input.TagSpecifications).toBeUndefined()
  })
})

describe("useDeleteVpc", () => {
  it("sends DeleteVpcCommand with VPC ID", async () => {
    createQueryClient()
    const { result } = renderHook(() => useDeleteVpc(), { wrapper })

    result.current.mutate("vpc-123")

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      VpcId: "vpc-123",
    })
  })
})

describe("useCreateSubnet", () => {
  it("sends CreateSubnetCommand with VPC ID and CIDR block", async () => {
    createQueryClient()
    const { result } = renderHook(() => useCreateSubnet(), { wrapper })

    result.current.mutate({
      vpcId: "vpc-123",
      cidrBlock: "10.0.1.0/24",
      availabilityZone: "us-east-1a",
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      VpcId: "vpc-123",
      CidrBlock: "10.0.1.0/24",
      AvailabilityZone: "us-east-1a",
    })
  })
})

describe("useDeleteSubnet", () => {
  it("sends DeleteSubnetCommand with subnet ID", async () => {
    createQueryClient()
    const { result } = renderHook(() => useDeleteSubnet(), { wrapper })

    result.current.mutate("subnet-123")

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(mockSend.mock.calls[0]?.[0].input).toEqual({
      SubnetId: "subnet-123",
    })
  })
})
