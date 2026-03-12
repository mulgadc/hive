import {
  type _InstanceType,
  AttachVolumeCommand,
  CopySnapshotCommand,
  CreateImageCommand,
  CreateKeyPairCommand,
  CreateSnapshotCommand,
  CreateSubnetCommand,
  CreateVolumeCommand,
  CreateVpcCommand,
  DeleteKeyPairCommand,
  DeleteSnapshotCommand,
  DeleteSubnetCommand,
  DeleteVolumeCommand,
  DeleteVpcCommand,
  DetachVolumeCommand,
  GetConsoleOutputCommand,
  ImportKeyPairCommand,
  ModifyInstanceAttributeCommand,
  ModifyVolumeCommand,
  RebootInstancesCommand,
  RunInstancesCommand,
  StartInstancesCommand,
  StopInstancesCommand,
  TerminateInstancesCommand,
} from "@aws-sdk/client-ec2"
import { useMutation, useQueryClient } from "@tanstack/react-query"

import { getEc2Client } from "@/lib/awsClient"
import { ec2KeyPairsQueryOptions } from "@/queries/ec2"
import type {
  AttachVolumeFormData,
  CopySnapshotFormData,
  CreateImageParams,
  CreateInstanceParams,
  CreateKeyPairData,
  CreateSnapshotFormData,
  CreateSubnetFormData,
  CreateVolumeFormData,
  CreateVpcFormData,
  DetachVolumeFormData,
  ImportKeyPairData,
  ModifyInstanceTypeFormData,
  ModifyVolumeParams,
} from "@/types/ec2"

const WHITESPACE_REGEX = /\s+/

export function useStartInstance() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (instanceId: string) => {
      const command = new StartInstancesCommand({
        InstanceIds: [instanceId],
      })
      return getEc2Client().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["ec2", "instances"] })
    },
  })
}

export function useStopInstance() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (instanceId: string) => {
      const command = new StopInstancesCommand({
        InstanceIds: [instanceId],
      })
      return getEc2Client().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["ec2", "instances"] })
    },
  })
}

export function useTerminateInstance() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (instanceId: string) => {
      const command = new TerminateInstancesCommand({
        InstanceIds: [instanceId],
      })
      return getEc2Client().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["ec2", "instances"] })
    },
  })
}

export function useRebootInstance() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (instanceId: string) => {
      const command = new RebootInstancesCommand({
        InstanceIds: [instanceId],
      })
      return getEc2Client().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["ec2", "instances"] })
    },
  })
}

export function useCreateInstance() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (params: CreateInstanceParams) => {
      const command = new RunInstancesCommand({
        ImageId: params.imageId,
        InstanceType: params.instanceType as _InstanceType,
        KeyName: params.keyName,
        MinCount: params.count,
        MaxCount: params.count,
        SubnetId: params.subnetId || undefined,
      })
      return getEc2Client().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["ec2", "instances"] })
    },
  })
}

export function useCreateKeyPair() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (params: CreateKeyPairData) => {
      const command = new CreateKeyPairCommand({
        KeyName: params.keyName,
        KeyType: "rsa",
      })
      return getEc2Client().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries(ec2KeyPairsQueryOptions)
    },
  })
}

export function useImportKeyPair() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (params: ImportKeyPairData) => {
      // remove optional comment from ssh key as it breaks the import
      const keyParts = params.publicKeyMaterial.trim().split(WHITESPACE_REGEX)
      const cleanedKey = keyParts.slice(0, 2).join(" ")

      const command = new ImportKeyPairCommand({
        KeyName: params.keyName,
        PublicKeyMaterial: new TextEncoder().encode(cleanedKey),
      })
      return getEc2Client().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries(ec2KeyPairsQueryOptions)
    },
  })
}

export function useDeleteKeyPair() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (keyPairId: string) => {
      const command = new DeleteKeyPairCommand({
        KeyPairId: keyPairId,
      })
      return getEc2Client().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries(ec2KeyPairsQueryOptions)
    },
  })
}

export function useCreateVolume() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (params: CreateVolumeFormData) => {
      const command = new CreateVolumeCommand({
        Size: params.size,
        AvailabilityZone: params.availabilityZone,
        VolumeType: "gp3",
      })
      return getEc2Client().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["ec2", "volumes"] })
    },
  })
}

export function useModifyVolume() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (params: ModifyVolumeParams) => {
      const command = new ModifyVolumeCommand({
        VolumeId: params.volumeId,
        Size: params.size,
      })
      return getEc2Client().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["ec2", "volumes"] })
    },
  })
}

export function useDeleteVolume() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (volumeId: string) => {
      const command = new DeleteVolumeCommand({
        VolumeId: volumeId,
      })
      return getEc2Client().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["ec2", "volumes"] })
    },
  })
}

export function useCreateSnapshot() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (params: CreateSnapshotFormData) => {
      const command = new CreateSnapshotCommand({
        VolumeId: params.volumeId,
        Description: params.description || undefined,
      })
      return getEc2Client().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["ec2", "snapshots"] })
    },
  })
}

export function useDeleteSnapshot() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (snapshotId: string) => {
      const command = new DeleteSnapshotCommand({
        SnapshotId: snapshotId,
      })
      return getEc2Client().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["ec2", "snapshots"] })
    },
  })
}

export function useCopySnapshot() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (params: CopySnapshotFormData) => {
      const command = new CopySnapshotCommand({
        SourceSnapshotId: params.sourceSnapshotId,
        SourceRegion: params.sourceRegion,
        Description: params.description || undefined,
      })
      return getEc2Client().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["ec2", "snapshots"] })
    },
  })
}

export function useAttachVolume() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (params: AttachVolumeFormData) => {
      const command = new AttachVolumeCommand({
        VolumeId: params.volumeId,
        InstanceId: params.instanceId,
        Device: params.device || undefined,
      })
      return getEc2Client().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["ec2", "volumes"] })
      queryClient.invalidateQueries({ queryKey: ["ec2", "instances"] })
    },
  })
}

export function useDetachVolume() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (params: DetachVolumeFormData) => {
      const command = new DetachVolumeCommand({
        VolumeId: params.volumeId,
        InstanceId: params.instanceId || undefined,
        Force: params.force,
      })
      return getEc2Client().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["ec2", "volumes"] })
      queryClient.invalidateQueries({ queryKey: ["ec2", "instances"] })
    },
  })
}

export function useModifyInstanceAttribute() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({
      instanceId,
      ...params
    }: ModifyInstanceTypeFormData & { instanceId: string }) => {
      const command = new ModifyInstanceAttributeCommand({
        InstanceId: instanceId,
        InstanceType: { Value: params.instanceType },
      })
      return getEc2Client().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["ec2", "instances"] })
    },
  })
}

export function useGetConsoleOutput() {
  return useMutation({
    mutationFn: (instanceId: string) => {
      const command = new GetConsoleOutputCommand({
        InstanceId: instanceId,
      })
      return getEc2Client().send(command)
    },
  })
}

export function useCreateImage() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (params: CreateImageParams) => {
      const command = new CreateImageCommand({
        InstanceId: params.instanceId,
        Name: params.name,
        Description: params.description || undefined,
      })
      return getEc2Client().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["ec2", "images"] })
    },
  })
}

export function useCreateVpc() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (params: CreateVpcFormData) => {
      const command = new CreateVpcCommand({
        CidrBlock: params.cidrBlock,
        TagSpecifications: params.name
          ? [
              {
                ResourceType: "vpc",
                Tags: [{ Key: "Name", Value: params.name }],
              },
            ]
          : undefined,
      })
      return getEc2Client().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["ec2", "vpcs"] })
    },
  })
}

export function useDeleteVpc() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (vpcId: string) => {
      const command = new DeleteVpcCommand({
        VpcId: vpcId,
      })
      return getEc2Client().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["ec2", "vpcs"] })
    },
  })
}

export function useCreateSubnet() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (params: CreateSubnetFormData) => {
      const command = new CreateSubnetCommand({
        VpcId: params.vpcId,
        CidrBlock: params.cidrBlock,
        AvailabilityZone: params.availabilityZone || undefined,
      })
      return getEc2Client().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["ec2", "subnets"] })
    },
  })
}

export function useDeleteSubnet() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (subnetId: string) => {
      const command = new DeleteSubnetCommand({
        SubnetId: subnetId,
      })
      return getEc2Client().send(command)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["ec2", "subnets"] })
    },
  })
}
