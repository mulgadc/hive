import { zodResolver } from "@hookform/resolvers/zod"
import { useSuspenseQuery } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { Controller, useForm } from "react-hook-form"

import { BackLink } from "@/components/back-link"
import { ErrorBanner } from "@/components/error-banner"
import { PageHeading } from "@/components/page-heading"
import { Button } from "@/components/ui/button"
import { Field, FieldError, FieldTitle } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
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
  ec2PlacementGroupsQueryOptions,
  ec2SubnetsQueryOptions,
} from "@/queries/ec2"
import { type CreateInstanceFormData, createInstanceSchema } from "@/types/ec2"

export const Route = createFileRoute("/_auth/ec2/(instances)/run-instances")({
  loader: async ({ context }) => {
    await Promise.all([
      context.queryClient.ensureQueryData(ec2ImagesQueryOptions),
      context.queryClient.ensureQueryData(ec2KeyPairsQueryOptions),
      context.queryClient.ensureQueryData(ec2InstanceTypesQueryOptions),
      context.queryClient.ensureQueryData(ec2SubnetsQueryOptions),
      context.queryClient.ensureQueryData(ec2PlacementGroupsQueryOptions),
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
  const { data: subnetsData } = useSuspenseQuery(ec2SubnetsQueryOptions)
  const { data: pgData } = useSuspenseQuery(ec2PlacementGroupsQueryOptions)
  const createMutation = useCreateInstance()
  const images = imagesData.Images ?? []
  const keyPairs = keyPairsData.KeyPairs ?? []
  const subnets = subnetsData.Subnets ?? []
  const placementGroups = pgData.PlacementGroups ?? []
  const instanceTypeCounts: Record<string, number> = {}
  for (const type of instanceTypesData.InstanceTypes ?? []) {
    const typeName = type.InstanceType
    if (typeName) {
      instanceTypeCounts[typeName] = (instanceTypeCounts[typeName] ?? 0) + 1
    }
  }

  const uniqueInstanceTypes = Object.keys(instanceTypeCounts).toSorted()

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
      imageId: defaultImageId ?? "",
      keyName: defaultKeyName ?? "",
      instanceType: defaultInstanceType ?? "",
    },
  })

  const selectedInstanceType = watch("instanceType")
  const maxCount = selectedInstanceType
    ? (instanceTypeCounts[selectedInstanceType] ?? 1)
    : 1

  const onSubmit = async (data: CreateInstanceFormData) => {
    await createMutation.mutateAsync(data)

    navigate({ to: "/ec2/describe-instances" })
  }

  return (
    <>
      <BackLink to="/ec2/describe-instances">Back to instances</BackLink>
      <PageHeading title="Run EC2 Instances" />

      {/* Handle error when no instance types available */}
      {uniqueInstanceTypes.length === 0 && (
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
        <Field>
          <FieldTitle>
            <label htmlFor="imageId">Image</label>
          </FieldTitle>
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
                  <SelectTrigger
                    aria-invalid={!!errors.imageId}
                    className="w-full"
                    id="imageId"
                  >
                    <SelectValue>
                      {selectedImage
                        ? `${selectedImage.Name ?? "Unnamed"} (${selectedImage.Architecture})`
                        : ""}
                    </SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    {images.map((image) => (
                      <SelectItem
                        key={image.ImageId}
                        value={image.ImageId ?? ""}
                      >
                        {image.Name ?? "Unnamed"} ({image.Architecture})
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )
            }}
          />
          <FieldError errors={[errors.imageId]} />
        </Field>

        {/* Instance Type */}
        <Field>
          <FieldTitle>
            <label htmlFor="instanceType">Instance Type</label>
          </FieldTitle>
          <Controller
            control={control}
            name="instanceType"
            render={({ field }) => (
              <Select
                onValueChange={(value) => field.onChange(value)}
                value={field.value || ""}
              >
                <SelectTrigger
                  aria-invalid={!!errors.instanceType}
                  className="w-full"
                  id="instanceType"
                >
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
          <FieldError errors={[errors.instanceType]} />
        </Field>

        {/* Key Pair */}
        <Field>
          <FieldTitle>
            <label htmlFor="keyName">Key Pair</label>
          </FieldTitle>
          <Controller
            control={control}
            name="keyName"
            render={({ field }) => (
              <Select
                onValueChange={(value) => field.onChange(value)}
                value={field.value || ""}
              >
                <SelectTrigger
                  aria-invalid={!!errors.keyName}
                  className="w-full"
                  id="keyName"
                >
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
          <FieldError errors={[errors.keyName]} />
        </Field>

        {/* Subnet */}
        <Field>
          <FieldTitle>
            <label htmlFor="subnetId">Subnet</label>
          </FieldTitle>
          <Controller
            control={control}
            name="subnetId"
            render={({ field }) => (
              <Select
                onValueChange={(value) =>
                  field.onChange(value === "none" ? undefined : value)
                }
                value={field.value ?? "none"}
              >
                <SelectTrigger className="w-full" id="subnetId">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="none">none</SelectItem>
                  {subnets.map((subnet) => (
                    <SelectItem
                      key={subnet.SubnetId}
                      value={subnet.SubnetId ?? ""}
                    >
                      {subnet.SubnetId}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          />
        </Field>

        {/* Placement Group */}
        <Field>
          <FieldTitle>
            <label htmlFor="placementGroupName">Placement Group</label>
          </FieldTitle>
          <Controller
            control={control}
            name="placementGroupName"
            render={({ field }) => (
              <Select
                onValueChange={(value) =>
                  field.onChange(value === "none" ? undefined : value)
                }
                value={field.value ?? "none"}
              >
                <SelectTrigger className="w-full" id="placementGroupName">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="none">none</SelectItem>
                  {placementGroups.map((pg) => (
                    <SelectItem key={pg.GroupId} value={pg.GroupName ?? ""}>
                      {pg.GroupName} ({pg.Strategy})
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          />
        </Field>

        {/* Instance Count */}
        <Field>
          <FieldTitle>
            <label htmlFor="count">Number of Instances</label>
          </FieldTitle>
          <Input
            aria-describedby="count-description"
            aria-invalid={!!errors.count}
            id="count"
            type="number"
            {...register("count", { valueAsNumber: true })}
          />
          <p className="text-xs text-muted-foreground" id="count-description">
            {selectedInstanceType &&
              `Available capacity for ${selectedInstanceType}: ${maxCount}`}
          </p>
          <FieldError errors={[errors.count]} />
        </Field>

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
              uniqueInstanceTypes.length === 0
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
