import {
  DescribeAvailabilityZonesCommand,
  DescribeImagesCommand,
  DescribeInstancesCommand,
  DescribeInstanceTypesCommand,
  DescribeKeyPairsCommand,
  DescribeRegionsCommand,
  DescribeSnapshotsCommand,
  DescribeSubnetsCommand,
  DescribeVolumesCommand,
  DescribeVpcsCommand,
} from "@aws-sdk/client-ec2"
import { queryOptions } from "@tanstack/react-query"

import { getEc2Client } from "@/lib/awsClient"

export const ec2AvailabilityZonesQueryOptions = queryOptions({
  queryKey: ["ec2", "availabilityZones"],
  queryFn: () => {
    const command = new DescribeAvailabilityZonesCommand({})
    return getEc2Client().send(command)
  },
  staleTime: 300_000,
})

export const ec2InstancesQueryOptions = queryOptions({
  queryKey: ["ec2", "instances"],
  queryFn: () => {
    const command = new DescribeInstancesCommand({})
    return getEc2Client().send(command)
  },
  refetchInterval: 5000,
})

export const ec2InstanceQueryOptions = (instanceId: string) =>
  queryOptions({
    queryKey: ["ec2", "instances", instanceId],
    queryFn: () => {
      const command = new DescribeInstancesCommand({
        InstanceIds: [instanceId],
      })
      return getEc2Client().send(command)
    },
    refetchInterval: 5000,
  })

export const ec2InstanceTypesQueryOptions = queryOptions({
  queryKey: ["ec2", "instances", "types"],
  queryFn: () => {
    const command = new DescribeInstanceTypesCommand({
      Filters: [
        {
          Name: "capacity",
          Values: ["true"],
        },
      ],
    })
    return getEc2Client().send(command)
  },
  refetchInterval: 5000,
})

export const ec2ImagesQueryOptions = queryOptions({
  queryKey: ["ec2", "images"],
  queryFn: () => {
    const command = new DescribeImagesCommand({})
    return getEc2Client().send(command)
  },
})

export const ec2ImageQueryOptions = (imageId: string | undefined) =>
  queryOptions({
    queryKey: ["ec2", "images", imageId ?? "none"],
    queryFn: () => {
      if (!imageId) {
        return { Images: [], $metadata: {} }
      }
      const command = new DescribeImagesCommand({
        ImageIds: [imageId],
      })
      return getEc2Client().send(command)
    },
  })

export const ec2KeyPairsQueryOptions = queryOptions({
  queryKey: ["ec2", "keypairs"],
  queryFn: () => {
    const command = new DescribeKeyPairsCommand({})
    return getEc2Client().send(command)
  },
})

export const ec2KeyPairQueryOptions = (keyPairId: string) =>
  queryOptions({
    queryKey: ["ec2", "keypairs", keyPairId],
    queryFn: () => {
      const command = new DescribeKeyPairsCommand({
        KeyPairIds: [keyPairId],
      })
      return getEc2Client().send(command)
    },
  })

export const ec2RegionsQueryOptions = queryOptions({
  queryKey: ["ec2", "regions"],
  queryFn: () => {
    const command = new DescribeRegionsCommand({})
    return getEc2Client().send(command)
  },
  staleTime: 300_000,
})

export const ec2SubnetsQueryOptions = queryOptions({
  queryKey: ["ec2", "subnets"],
  queryFn: () => {
    const command = new DescribeSubnetsCommand({})
    return getEc2Client().send(command)
  },
})

export const ec2SubnetQueryOptions = (subnetId: string) =>
  queryOptions({
    queryKey: ["ec2", "subnets", subnetId],
    queryFn: () => {
      const command = new DescribeSubnetsCommand({
        SubnetIds: [subnetId],
      })
      return getEc2Client().send(command)
    },
  })

export const ec2SnapshotsQueryOptions = queryOptions({
  queryKey: ["ec2", "snapshots"],
  queryFn: () => {
    const command = new DescribeSnapshotsCommand({})
    return getEc2Client().send(command)
  },
  refetchInterval: 5000,
})

export const ec2SnapshotQueryOptions = (snapshotId: string) =>
  queryOptions({
    queryKey: ["ec2", "snapshots", snapshotId],
    queryFn: () => {
      const command = new DescribeSnapshotsCommand({
        SnapshotIds: [snapshotId],
      })
      return getEc2Client().send(command)
    },
    refetchInterval: 5000,
  })

export const ec2VpcsQueryOptions = queryOptions({
  queryKey: ["ec2", "vpcs"],
  queryFn: () => {
    const command = new DescribeVpcsCommand({})
    return getEc2Client().send(command)
  },
})

export const ec2VpcQueryOptions = (vpcId: string) =>
  queryOptions({
    queryKey: ["ec2", "vpcs", vpcId],
    queryFn: () => {
      const command = new DescribeVpcsCommand({
        VpcIds: [vpcId],
      })
      return getEc2Client().send(command)
    },
  })

export const ec2VolumesQueryOptions = queryOptions({
  queryKey: ["ec2", "volumes"],
  queryFn: () => {
    const command = new DescribeVolumesCommand({})
    return getEc2Client().send(command)
  },
  refetchInterval: 5000,
})

export const ec2VolumeQueryOptions = (volumeId: string) =>
  queryOptions({
    queryKey: ["ec2", "volumes", volumeId],
    queryFn: () => {
      const command = new DescribeVolumesCommand({
        VolumeIds: [volumeId],
      })
      return getEc2Client().send(command)
    },
    refetchInterval: 5000,
  })
