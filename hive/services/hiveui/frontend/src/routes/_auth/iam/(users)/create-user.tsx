import { zodResolver } from "@hookform/resolvers/zod"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { useForm } from "react-hook-form"

import { BackLink } from "@/components/back-link"
import { ErrorBanner } from "@/components/error-banner"
import { PageHeading } from "@/components/page-heading"
import { Button } from "@/components/ui/button"
import { Field, FieldError, FieldTitle } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { useCreateUser } from "@/mutations/iam"
import { type CreateUserFormData, createUserSchema } from "@/types/iam"

export const Route = createFileRoute("/_auth/iam/(users)/create-user")({
  head: () => ({
    meta: [{ title: "Create User | IAM | Mulga" }],
  }),
  component: CreateUser,
})

function CreateUser() {
  const navigate = useNavigate()
  const createMutation = useCreateUser()

  const {
    handleSubmit,
    register,
    formState: { errors, isSubmitting },
  } = useForm({
    resolver: zodResolver(createUserSchema),
  })

  const onSubmit = async (data: CreateUserFormData) => {
    await createMutation.mutateAsync(data)
    navigate({ to: "/iam/list-users" })
  }

  return (
    <>
      <BackLink to="/iam/list-users">Back to users</BackLink>
      <PageHeading title="Create User" />

      {createMutation.error && (
        <ErrorBanner error={createMutation.error} msg="Failed to create user" />
      )}

      <form className="max-w-4xl space-y-6" onSubmit={handleSubmit(onSubmit)}>
        <Field>
          <FieldTitle>
            <label htmlFor="userName">User Name</label>
          </FieldTitle>
          <Input
            aria-invalid={!!errors.userName}
            id="userName"
            placeholder="my-user..."
            {...register("userName")}
          />
          <FieldError errors={[errors.userName]} />
        </Field>

        <Field>
          <FieldTitle>
            <label htmlFor="path">Path (optional)</label>
          </FieldTitle>
          <Input id="path" placeholder="/" {...register("path")} />
          <FieldError errors={[errors.path]} />
        </Field>

        <div className="flex gap-2">
          <Button
            disabled={isSubmitting || createMutation.isPending}
            onClick={() => navigate({ to: "/iam/list-users" })}
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
              ? "Creating..."
              : "Create User"}
          </Button>
        </div>
      </form>
    </>
  )
}
