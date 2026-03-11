import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { renderHook, waitFor } from "@testing-library/react"
import type { ReactNode } from "react"
import { afterEach, describe, expect, it, vi } from "vitest"

const mockSend = vi.fn().mockResolvedValue({})

vi.mock("@/lib/awsClient", () => ({
  getEc2Client: () => ({ send: mockSend }),
}))

import {
  useCreateInstance,
  useCreateVpc,
  useImportKeyPair,
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
