import { zodResolver } from "@hookform/resolvers/zod"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { useForm } from "react-hook-form"

import { BackLink } from "@/components/back-link"
import { ErrorBanner } from "@/components/error-banner"
import { PageHeading } from "@/components/page-heading"
import { Button } from "@/components/ui/button"
import { Field, FieldError, FieldTitle } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { useCreateBucket } from "@/mutations/s3"
import { type CreateBucketFormData, createBucketSchema } from "@/types/s3"

export const Route = createFileRoute("/_auth/s3/(buckets)/create-bucket")({
  head: () => ({
    meta: [
      {
        title: "Create Bucket | S3 | Mulga",
      },
    ],
  }),
  component: CreateBucket,
})

function CreateBucket() {
  const navigate = useNavigate()
  const createMutation = useCreateBucket()

  const {
    handleSubmit,
    register,
    formState: { errors, isSubmitting },
  } = useForm({
    resolver: zodResolver(createBucketSchema),
  })

  const onSubmit = async (data: CreateBucketFormData) => {
    await createMutation.mutateAsync(data)
    navigate({ to: "/s3/ls" })
  }

  return (
    <>
      <BackLink to="/s3/ls">Back to buckets</BackLink>

      <PageHeading title="Create Bucket" />

      {createMutation.error && (
        <ErrorBanner
          error={createMutation.error}
          msg="Failed to create bucket"
        />
      )}

      <form className="max-w-4xl space-y-6" onSubmit={handleSubmit(onSubmit)}>
        <Field>
          <FieldTitle>
            <label htmlFor="bucketName">Bucket Name</label>
          </FieldTitle>
          <Input
            aria-invalid={!!errors.bucketName}
            id="bucketName"
            placeholder="my-bucket"
            type="text"
            {...register("bucketName")}
          />
          <FieldError errors={[errors.bucketName]} />
        </Field>

        <div className="flex gap-2">
          <Button
            disabled={isSubmitting || createMutation.isPending}
            onClick={() => navigate({ to: "/s3/ls" })}
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
              : "Create Bucket"}
          </Button>
        </div>
      </form>
    </>
  )
}
