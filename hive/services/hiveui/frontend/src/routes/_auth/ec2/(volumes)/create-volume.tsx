import { zodResolver } from "@hookform/resolvers/zod"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { useForm } from "react-hook-form"

import { BackLink } from "@/components/back-link"
import { ErrorBanner } from "@/components/error-banner"
import { PageHeading } from "@/components/page-heading"
import { Button } from "@/components/ui/button"
import { Field, FieldError, FieldTitle } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { useCreateVolume } from "@/mutations/ec2"
import { type CreateVolumeFormData, createVolumeSchema } from "@/types/ec2"

export const Route = createFileRoute("/_auth/ec2/(volumes)/create-volume")({
  head: () => ({
    meta: [
      {
        title: "Create Volume | EC2 | Mulga",
      },
    ],
  }),
  component: CreateVolume,
})

function CreateVolume() {
  const navigate = useNavigate()
  const createMutation = useCreateVolume()

  const {
    handleSubmit,
    register,
    formState: { errors, isSubmitting },
  } = useForm<CreateVolumeFormData>({
    resolver: zodResolver(createVolumeSchema),
    defaultValues: {
      size: 1,
      availabilityZone: "",
    },
  })

  const onSubmit = async (data: CreateVolumeFormData) => {
    await createMutation.mutateAsync(data)
    navigate({ to: "/ec2/describe-volumes" })
  }

  return (
    <>
      <BackLink to="/ec2/describe-volumes">Back to volumes</BackLink>

      <PageHeading title="Create Volume" />

      {createMutation.error && (
        <ErrorBanner
          error={createMutation.error}
          msg="Failed to create volume"
        />
      )}

      <form className="max-w-4xl space-y-6" onSubmit={handleSubmit(onSubmit)}>
        <Field>
          <FieldTitle>
            <label htmlFor="size">Size (GiB)</label>
          </FieldTitle>
          <Input
            aria-invalid={!!errors.size}
            id="size"
            max={16_384}
            min={1}
            type="number"
            {...register("size", { valueAsNumber: true })}
          />
          <FieldError errors={[errors.size]} />
        </Field>

        <Field>
          <FieldTitle>
            <label htmlFor="availabilityZone">Availability Zone</label>
          </FieldTitle>
          <Input
            aria-invalid={!!errors.availabilityZone}
            id="availabilityZone"
            placeholder="ap-southeast-2a"
            type="text"
            {...register("availabilityZone")}
          />
          <FieldError errors={[errors.availabilityZone]} />
        </Field>

        <div className="flex gap-2">
          <Button
            disabled={isSubmitting || createMutation.isPending}
            onClick={() => navigate({ to: "/ec2/describe-volumes" })}
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
              : "Create Volume"}
          </Button>
        </div>
      </form>
    </>
  )
}
