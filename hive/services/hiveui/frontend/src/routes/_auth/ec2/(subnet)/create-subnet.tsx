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
import { useCreateSubnet } from "@/mutations/ec2"
import {
  ec2AvailabilityZonesQueryOptions,
  ec2VpcsQueryOptions,
} from "@/queries/ec2"
import { type CreateSubnetFormData, createSubnetSchema } from "@/types/ec2"

export const Route = createFileRoute("/_auth/ec2/(subnet)/create-subnet")({
  loader: async ({ context }) => {
    await Promise.all([
      context.queryClient.ensureQueryData(ec2VpcsQueryOptions),
      context.queryClient.ensureQueryData(ec2AvailabilityZonesQueryOptions),
    ])
  },
  head: () => ({
    meta: [
      {
        title: "Create Subnet | EC2 | Mulga",
      },
    ],
  }),
  component: CreateSubnet,
})

function CreateSubnet() {
  const navigate = useNavigate()
  const { data: vpcsData } = useSuspenseQuery(ec2VpcsQueryOptions)
  const { data: azsData } = useSuspenseQuery(ec2AvailabilityZonesQueryOptions)
  const createMutation = useCreateSubnet()

  const vpcs = vpcsData.Vpcs ?? []
  const azs = azsData.AvailabilityZones ?? []

  const {
    control,
    handleSubmit,
    register,
    formState: { errors, isSubmitting },
  } = useForm<CreateSubnetFormData>({
    resolver: zodResolver(createSubnetSchema),
    defaultValues: {
      vpcId: vpcs[0]?.VpcId ?? "",
      cidrBlock: "10.0.1.0/24",
      availabilityZone: "",
    },
  })

  const onSubmit = async (data: CreateSubnetFormData) => {
    const result = await createMutation.mutateAsync(data)
    const subnetId = result.Subnet?.SubnetId
    if (subnetId) {
      navigate({ to: "/ec2/describe-subnets/$id", params: { id: subnetId } })
    } else {
      navigate({ to: "/ec2/describe-subnets" })
    }
  }

  return (
    <>
      <BackLink to="/ec2/describe-subnets">Back to Subnets</BackLink>

      <PageHeading title="Create Subnet" />

      {createMutation.error && (
        <ErrorBanner
          error={createMutation.error}
          msg="Failed to create subnet"
        />
      )}

      <form className="max-w-4xl space-y-6" onSubmit={handleSubmit(onSubmit)}>
        <Field>
          <FieldTitle>
            <label htmlFor="vpcId">VPC</label>
          </FieldTitle>
          <Controller
            control={control}
            name="vpcId"
            render={({ field }) => (
              <Select
                onValueChange={(value) => field.onChange(value)}
                value={field.value ?? ""}
              >
                <SelectTrigger
                  aria-invalid={!!errors.vpcId}
                  className="w-full"
                  id="vpcId"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {vpcs.map((vpc) => {
                    const name = vpc.Tags?.find((t) => t.Key === "Name")?.Value
                    return (
                      <SelectItem key={vpc.VpcId} value={vpc.VpcId ?? ""}>
                        {name
                          ? `${vpc.VpcId} (${name})`
                          : `${vpc.VpcId} (${vpc.CidrBlock})`}
                      </SelectItem>
                    )
                  })}
                </SelectContent>
              </Select>
            )}
          />
          <FieldError errors={[errors.vpcId]} />
        </Field>

        <Field>
          <FieldTitle>
            <label htmlFor="cidrBlock">CIDR Block</label>
          </FieldTitle>
          <Input
            aria-invalid={!!errors.cidrBlock}
            id="cidrBlock"
            placeholder="10.0.1.0/24"
            {...register("cidrBlock")}
          />
          <FieldError errors={[errors.cidrBlock]} />
        </Field>

        <Field>
          <FieldTitle>
            <label htmlFor="availabilityZone">
              Availability Zone (optional)
            </label>
          </FieldTitle>
          <Controller
            control={control}
            name="availabilityZone"
            render={({ field }) => (
              <Select
                onValueChange={(value) =>
                  field.onChange(value === "none" ? "" : value)
                }
                value={field.value || "none"}
              >
                <SelectTrigger className="w-full" id="availabilityZone">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="none">none</SelectItem>
                  {azs.map((az) => (
                    <SelectItem key={az.ZoneName} value={az.ZoneName ?? ""}>
                      {az.ZoneName}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          />
        </Field>

        <div className="flex gap-2">
          <Button
            disabled={isSubmitting || createMutation.isPending}
            onClick={() => navigate({ to: "/ec2/describe-subnets" })}
            type="button"
            variant="outline"
          >
            Cancel
          </Button>
          <Button
            disabled={isSubmitting || createMutation.isPending}
            type="submit"
          >
            {isSubmitting || createMutation.isPending
              ? "Creating\u2026"
              : "Create Subnet"}
          </Button>
        </div>
      </form>
    </>
  )
}
