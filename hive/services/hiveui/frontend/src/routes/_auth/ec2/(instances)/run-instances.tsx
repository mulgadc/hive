import { zodResolver } from "@hookform/resolvers/zod"
import { useSuspenseQuery } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { Controller, useForm } from "react-hook-form"

import { BackLink } from "@/components/back-link"
import { ErrorBanner } from "@/components/error-banner"
import { PageHeading } from "@/components/page-heading"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { useCreateInstance } from "@/mutations/ec2"
import {
  ec2ImagesQueryOptions,
  ec2InstanceTypesQueryOptions,
  ec2KeyPairsQueryOptions,
} from "@/queries/ec2"
import { type CreateInstanceFormData, createInstanceSchema } from "@/types/ec2"

// Hardcoded values - will be replaced when IAM is implemented
const SECURITY_GROUP_IDS = ["sg-placeholder"]
const SUBNET_ID = "subnet-placeholder"

export const Route = createFileRoute("/_auth/ec2/(instances)/run-instances")({
  loader: async ({ context }) => {
    await Promise.all([
      context.queryClient.ensureQueryData(ec2ImagesQueryOptions),
      context.queryClient.ensureQueryData(ec2KeyPairsQueryOptions),
      context.queryClient.ensureQueryData(ec2InstanceTypesQueryOptions),
    ])
  },
  head: () => ({
    meta: [
      {
        title: "Run Instances | EC2 | Mulga",
      },
    ],
  }),
  component: CreateInstance,
})

function CreateInstance() {
  const navigate = useNavigate()
  const { data: imagesData } = useSuspenseQuery(ec2ImagesQueryOptions)
  const { data: keyPairsData } = useSuspenseQuery(ec2KeyPairsQueryOptions)
  const { data: instanceTypesData } = useSuspenseQuery(
    ec2InstanceTypesQueryOptions,
  )
  const createMutation = useCreateInstance()
  const images = imagesData.Images ?? []
  const keyPairs = keyPairsData.KeyPairs ?? []
  const instanceTypes =
    instanceTypesData.InstanceTypes?.flatMap((type) =>
      type.InstanceType ? [type.InstanceType] : [],
    ) ?? []

  const instanceTypeCounts =
    instanceTypesData.InstanceTypes?.reduce(
      (acc, type) => {
        const typeName = type.InstanceType
        if (typeName) {
          acc[typeName] = (acc[typeName] || 0) + 1
        }
        return acc
      },
      {} as Record<string, number>,
    ) ?? {}

  const uniqueInstanceTypes = Object.keys(instanceTypeCounts).sort()

  // Compute default values from loaded data
  const defaultImageId = images[0]?.ImageId
  const defaultKeyName = keyPairs[0]?.KeyName
  const defaultInstanceType =
    uniqueInstanceTypes.find((type) => type.endsWith(".nano")) ??
    uniqueInstanceTypes[0]

  const {
    control,
    handleSubmit,
    register,
    watch,
    formState: { errors, isSubmitting },
  } = useForm({
    resolver: zodResolver(
      createInstanceSchema.refine(
        (data) => data.count <= (instanceTypeCounts[data.instanceType] ?? 1),
        {
          message: "Cannot exceed available capacity",
          path: ["count"],
        },
      ),
    ),
    defaultValues: {
      count: 1,
      imageId: defaultImageId,
      keyName: defaultKeyName,
      instanceType: defaultInstanceType,
    },
  })

  const selectedInstanceType = watch("instanceType")
  const maxCount = selectedInstanceType
    ? (instanceTypeCounts[selectedInstanceType] ?? 1)
    : 1

  const onSubmit = async (data: CreateInstanceFormData) => {
    await createMutation.mutateAsync({
      ...data,
      securityGroupIds: SECURITY_GROUP_IDS,
      subnetId: SUBNET_ID,
    })

    navigate({ to: "/ec2/describe-instances" })
  }

  return (
    <>
      <BackLink to="/ec2/describe-instances">Back to instances</BackLink>
      <PageHeading title="Run EC2 Instances" />

      {/* Handle error when no instance types available */}
      {instanceTypes.length === 0 && (
        <ErrorBanner msg="No compute available. No new instances can be created until compute is available." />
      )}

      {/* Handle error after submission */}
      {createMutation.error && (
        <ErrorBanner
          error={createMutation.error}
          msg="Failed to create instance"
        />
      )}

      <form className="max-w-4xl space-y-6" onSubmit={handleSubmit(onSubmit)}>
        {/* ImageSelection */}
        <div className="space-y-2">
          <Label htmlFor="imageId">Image</Label>
          <Controller
            control={control}
            name="imageId"
            render={({ field }) => {
              const selectedImage = images.find(
                (img) => img.ImageId === field.value,
              )
              return (
                <Select
                  onValueChange={(value) => field.onChange(value)}
                  value={field.value ?? ""}
                >
                  <SelectTrigger className="w-full" id="imageId">
                    <SelectValue>
                      {selectedImage
                        ? `${selectedImage.Name || "Unnamed"} (${selectedImage.Architecture})`
                        : ""}
                    </SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    {images.map((image) => (
                      <SelectItem
                        key={image.ImageId}
                        value={image.ImageId ?? ""}
                      >
                        {image.Name || "Unnamed"} ({image.Architecture})
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )
            }}
          />
          {errors.imageId && (
            <p className="text-destructive text-sm">{errors.imageId.message}</p>
          )}
        </div>

        {/* Instance Type */}
        <div className="space-y-2">
          <Label htmlFor="instanceType">Instance Type</Label>
          <Controller
            control={control}
            name="instanceType"
            render={({ field }) => (
              <Select
                onValueChange={(value) => field.onChange(value)}
                value={field.value ? field.value : ""}
              >
                <SelectTrigger className="w-full" id="instanceType">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {uniqueInstanceTypes.map((type) => (
                    <SelectItem key={type} value={type}>
                      {type} ({instanceTypeCounts[type]} available)
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          />
          {errors.instanceType && (
            <p className="text-destructive text-sm">
              {errors.instanceType.message}
            </p>
          )}
        </div>

        {/* Key Pair */}
        <div className="space-y-2">
          <Label htmlFor="keyName">Key Pair</Label>
          <Controller
            control={control}
            name="keyName"
            render={({ field }) => (
              <Select
                onValueChange={(value) => field.onChange(value)}
                value={field.value ? field.value : ""}
              >
                <SelectTrigger className="w-full" id="keyName">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {keyPairs.map((keyPair) => (
                    <SelectItem
                      key={keyPair.KeyPairId}
                      value={keyPair.KeyName ?? ""}
                    >
                      {keyPair.KeyName}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          />
          {errors.keyName && (
            <p className="text-destructive text-sm">{errors.keyName.message}</p>
          )}
        </div>

        {/* Instance Count */}
        <div className="space-y-2">
          <Label htmlFor="count">Number of Instances</Label>
          <Input
            id="count"
            type="number"
            {...register("count", { valueAsNumber: true })}
          />
          <p className="text-muted-foreground text-xs" id="count-description">
            {selectedInstanceType &&
              `Available capacity for ${selectedInstanceType}: ${maxCount}`}
          </p>
          {errors.count && (
            <p className="text-destructive text-sm">{errors.count.message}</p>
          )}
        </div>

        {/* Actions */}
        <div className="flex gap-2">
          <Button
            disabled={isSubmitting || createMutation.isPending}
            onClick={() => navigate({ to: "/ec2/describe-instances" })}
            type="button"
            variant="outline"
          >
            Cancel
          </Button>
          <Button
            disabled={
              isSubmitting ||
              createMutation.isPending ||
              instanceTypes.length === 0
            }
            type="submit"
          >
            {isSubmitting || createMutation.isPending
              ? "Creating…"
              : "Create Instance"}
          </Button>
        </div>
      </form>
    </>
  )
}
