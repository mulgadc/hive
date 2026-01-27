import {
  DescribeImagesCommand,
  DescribeInstancesCommand,
  DescribeInstanceTypesCommand,
  DescribeKeyPairsCommand,
  DescribeRegionsCommand,
  DescribeVolumesCommand,
} from "@aws-sdk/client-ec2"
import { queryOptions } from "@tanstack/react-query"

import { getEc2Client } from "@/lib/awsClient"

export const ec2InstancesQueryOptions = queryOptions({
  queryKey: ["ec2", "instances"],
  queryFn: async () => {
    try {
      const command = new DescribeInstancesCommand({})
      return await getEc2Client().send(command)
    } catch {
      throw new Error("Failed to fetch EC2 instances")
    }
  },
  staleTime: 5000,
})

export const ec2InstanceQueryOptions = (instanceId: string) =>
  queryOptions({
    queryKey: ["ec2", "instances", instanceId],
    queryFn: async () => {
      try {
        const command = new DescribeInstancesCommand({
          InstanceIds: [instanceId],
        })
        return await getEc2Client().send(command)
      } catch {
        throw new Error("Failed to fetch EC2 instance")
      }
    },
    staleTime: 5000,
  })

export const ec2InstanceTypesQueryOptions = queryOptions({
  queryKey: ["ec2", "instances", "types"],
  queryFn: async () => {
    try {
      const command = new DescribeInstanceTypesCommand({
        Filters: [
          {
            Name: "capacity",
            Values: ["true"],
          },
        ],
      })
      return await getEc2Client().send(command)
    } catch {
      throw new Error("Failed to fetch EC2 instance types")
    }
  },
  staleTime: 5000,
})

export const ec2ImagesQueryOptions = queryOptions({
  queryKey: ["ec2", "images"],
  queryFn: async () => {
    try {
      const command = new DescribeImagesCommand({})
      return await getEc2Client().send(command)
    } catch {
      throw new Error("Failed to fetch EC2 images")
    }
  },
  staleTime: 300_000,
})

export const ec2ImageQueryOptions = (imageId: string | undefined) =>
  queryOptions({
    queryKey: ["ec2", "images", imageId ?? "none"],
    queryFn: async () => {
      try {
        if (!imageId) {
          return { Images: [], $metadata: {} }
        }
        const command = new DescribeImagesCommand({
          ImageIds: [imageId],
        })
        return await getEc2Client().send(command)
      } catch {
        throw new Error("Failed to fetch EC2 image")
      }
    },
    staleTime: 300_000,
  })

export const ec2KeyPairsQueryOptions = queryOptions({
  queryKey: ["ec2", "keypairs"],
  queryFn: async () => {
    try {
      const command = new DescribeKeyPairsCommand({})
      return await getEc2Client().send(command)
    } catch {
      throw new Error("Failed to fetch key pairs")
    }
  },
  staleTime: 300_000,
})

export const ec2KeyPairQueryOptions = (keyPairId: string) =>
  queryOptions({
    queryKey: ["ec2", "keypairs", keyPairId],
    queryFn: async () => {
      try {
        const command = new DescribeKeyPairsCommand({
          KeyPairIds: [keyPairId],
        })
        return await getEc2Client().send(command)
      } catch {
        throw new Error("Failed to fetch key pair")
      }
    },
    staleTime: 300_000,
  })

export const ec2RegionsQueryOptions = queryOptions({
  queryKey: ["ec2", "regions"],
  queryFn: async () => {
    try {
      const command = new DescribeRegionsCommand({})
      return await getEc2Client().send(command)
    } catch {
      throw new Error("Failed to fetch regions")
    }
  },
  staleTime: 300_000,
})

export const ec2VolumesQueryOptions = queryOptions({
  queryKey: ["ec2", "volumes"],
  queryFn: async () => {
    try {
      const command = new DescribeVolumesCommand({})
      return await getEc2Client().send(command)
    } catch {
      throw new Error("Failed to fetch volumes")
    }
  },
  staleTime: 5000,
})

export const ec2VolumeQueryOptions = (volumeId: string) =>
  queryOptions({
    queryKey: ["ec2", "volumes", volumeId],
    queryFn: async () => {
      try {
        const command = new DescribeVolumesCommand({
          VolumeIds: [volumeId],
        })
        return await getEc2Client().send(command)
      } catch {
        throw new Error("Failed to fetch volume")
      }
    },
    staleTime: 5000,
  })
