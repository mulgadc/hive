import {
  type _InstanceType,
  CreateKeyPairCommand,
  CreateVolumeCommand,
  DeleteKeyPairCommand,
  DeleteVolumeCommand,
  ImportKeyPairCommand,
  ModifyVolumeCommand,
  RebootInstancesCommand,
  RunInstancesCommand,
  StartInstancesCommand,
  StopInstancesCommand,
  TerminateInstancesCommand,
} from "@aws-sdk/client-ec2"
import { useMutation, useQueryClient } from "@tanstack/react-query"

import { getEc2Client } from "@/lib/awsClient"
import {
  ec2InstancesQueryOptions,
  ec2KeyPairsQueryOptions,
} from "@/queries/ec2"
import type {
  CreateInstanceParams,
  CreateKeyPairData,
  CreateVolumeFormData,
  ImportKeyPairData,
  ModifyVolumeParams,
} from "@/types/ec2"

const WHITESPACE_REGEX = /\s+/

export function useStartInstance() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (instanceId: string) => {
      try {
        const command = new StartInstancesCommand({
          InstanceIds: [instanceId],
        })
        return await getEc2Client().send(command)
      } catch {
        throw new Error("Failed to start instance")
      }
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["ec2", "instances"] })
    },
  })
}

export function useStopInstance() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (instanceId: string) => {
      try {
        const command = new StopInstancesCommand({
          InstanceIds: [instanceId],
        })
        return await getEc2Client().send(command)
      } catch {
        throw new Error("Failed to stop instance")
      }
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["ec2", "instances"] })
    },
  })
}

export function useTerminateInstance() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (instanceId: string) => {
      try {
        const command = new TerminateInstancesCommand({
          InstanceIds: [instanceId],
        })
        return await getEc2Client().send(command)
      } catch {
        throw new Error("Failed to terminate instance")
      }
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["ec2", "instances"] })
    },
  })
}

export function useRebootInstance() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (instanceId: string) => {
      try {
        const command = new RebootInstancesCommand({
          InstanceIds: [instanceId],
        })
        return await getEc2Client().send(command)
      } catch {
        throw new Error("Failed to reboot instance")
      }
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["ec2", "instances"] })
    },
  })
}

export function useCreateInstance() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (params: CreateInstanceParams) => {
      try {
        const command = new RunInstancesCommand({
          ImageId: params.imageId,
          InstanceType: params.instanceType as _InstanceType,
          KeyName: params.keyName,
          MinCount: params.count,
          MaxCount: params.count,
          SecurityGroupIds: params.securityGroupIds,
          SubnetId: params.subnetId,
        })
        return await getEc2Client().send(command)
      } catch {
        throw new Error("Failed to create instance")
      }
    },
    onSuccess: async (data) => {
      await queryClient.cancelQueries(ec2InstancesQueryOptions)

      const previousData = queryClient.getQueryData(
        ec2InstancesQueryOptions.queryKey,
      )

      if (previousData && data.Instances) {
        const newReservation = {
          Instances: data.Instances,
          OwnerId: data.OwnerId,
          RequesterId: data.RequesterId,
          ReservationId: data.ReservationId,
          Groups: [],
        }

        queryClient.setQueryData(ec2InstancesQueryOptions.queryKey, {
          ...previousData,
          Reservations: [...(previousData.Reservations || []), newReservation],
        })
      }

      queryClient.invalidateQueries({ queryKey: ["ec2", "instances"] })
    },
  })
}

export function useCreateKeyPair() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (params: CreateKeyPairData) => {
      try {
        const command = new CreateKeyPairCommand({
          KeyName: params.keyName,
          KeyType: "rsa",
        })
        return await getEc2Client().send(command)
      } catch {
        throw new Error("Failed to create key pair")
      }
    },
    onSuccess: () => {
      queryClient.invalidateQueries(ec2KeyPairsQueryOptions)
    },
  })
}

export function useImportKeyPair() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (params: ImportKeyPairData) => {
      try {
        // remove optional comment from ssh key as it breaks the import
        const keyParts = params.publicKeyMaterial.trim().split(WHITESPACE_REGEX)
        const cleanedKey = keyParts.slice(0, 2).join(" ")

        const command = new ImportKeyPairCommand({
          KeyName: params.keyName,
          PublicKeyMaterial: new TextEncoder().encode(cleanedKey),
        })
        return await getEc2Client().send(command)
      } catch {
        throw new Error("Failed to import key pair")
      }
    },
    onSuccess: () => {
      queryClient.invalidateQueries(ec2KeyPairsQueryOptions)
    },
  })
}

export function useDeleteKeyPair() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (keyPairId: string) => {
      try {
        const command = new DeleteKeyPairCommand({
          KeyPairId: keyPairId,
        })
        return await getEc2Client().send(command)
      } catch {
        throw new Error("Failed to delete key pair")
      }
    },
    onSuccess: () => {
      queryClient.invalidateQueries(ec2KeyPairsQueryOptions)
    },
  })
}

export function useCreateVolume() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (params: CreateVolumeFormData) => {
      try {
        const command = new CreateVolumeCommand({
          Size: params.size,
          AvailabilityZone: params.availabilityZone,
          VolumeType: "gp3",
        })
        return await getEc2Client().send(command)
      } catch {
        throw new Error("Failed to create volume")
      }
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["ec2", "volumes"] })
    },
  })
}

export function useModifyVolume() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (params: ModifyVolumeParams) => {
      try {
        const command = new ModifyVolumeCommand({
          VolumeId: params.volumeId,
          Size: params.size,
        })
        return await getEc2Client().send(command)
      } catch {
        throw new Error("Failed to modify volume")
      }
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["ec2", "volumes"] })
    },
  })
}

export function useDeleteVolume() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (volumeId: string) => {
      try {
        const command = new DeleteVolumeCommand({
          VolumeId: volumeId,
        })
        return await getEc2Client().send(command)
      } catch {
        throw new Error("Failed to delete volume")
      }
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["ec2", "volumes"] })
    },
  })
}
