import { zodResolver } from "@hookform/resolvers/zod"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { useForm } from "react-hook-form"

import { BackLink } from "@/components/back-link"
import { ErrorBanner } from "@/components/error-banner"
import { PageHeading } from "@/components/page-heading"
import { Button } from "@/components/ui/button"
import { Field, FieldError, FieldTitle } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { useCreateVpc } from "@/mutations/ec2"
import { type CreateVpcFormData, createVpcSchema } from "@/types/ec2"

export const Route = createFileRoute("/_auth/ec2/(vpc)/create-vpc")({
  head: () => ({
    meta: [
      {
        title: "Create VPC | EC2 | Mulga",
      },
    ],
  }),
  component: CreateVpc,
})

function CreateVpc() {
  const navigate = useNavigate()
  const createMutation = useCreateVpc()

  const {
    handleSubmit,
    register,
    formState: { errors, isSubmitting },
  } = useForm<CreateVpcFormData>({
    resolver: zodResolver(createVpcSchema),
    defaultValues: {
      cidrBlock: "10.0.0.0/16",
      name: "",
    },
  })

  const onSubmit = async (data: CreateVpcFormData) => {
    const result = await createMutation.mutateAsync(data)
    const vpcId = result.Vpc?.VpcId
    if (vpcId) {
      navigate({ to: "/ec2/describe-vpcs/$id", params: { id: vpcId } })
    } else {
      navigate({ to: "/ec2/describe-vpcs" })
    }
  }

  return (
    <>
      <BackLink to="/ec2/describe-vpcs">Back to VPCs</BackLink>

      <PageHeading title="Create VPC" />

      {createMutation.error && (
        <ErrorBanner error={createMutation.error} msg="Failed to create VPC" />
      )}

      <form className="max-w-4xl space-y-6" onSubmit={handleSubmit(onSubmit)}>
        <Field>
          <FieldTitle>
            <label htmlFor="cidrBlock">CIDR Block</label>
          </FieldTitle>
          <Input
            aria-invalid={!!errors.cidrBlock}
            id="cidrBlock"
            placeholder="10.0.0.0/16"
            {...register("cidrBlock")}
          />
          <FieldError errors={[errors.cidrBlock]} />
        </Field>

        <Field>
          <FieldTitle>
            <label htmlFor="name">Name (optional)</label>
          </FieldTitle>
          <Input id="name" placeholder="my-vpc" {...register("name")} />
        </Field>

        <div className="flex gap-2">
          <Button
            disabled={isSubmitting || createMutation.isPending}
            onClick={() => navigate({ to: "/ec2/describe-vpcs" })}
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
              : "Create VPC"}
          </Button>
        </div>
      </form>
    </>
  )
}
