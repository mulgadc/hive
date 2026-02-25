import { zodResolver } from "@hookform/resolvers/zod"
import { useSuspenseQuery } from "@tanstack/react-query"
import {
  createFileRoute,
  type SearchSchemaInput,
  useNavigate,
} from "@tanstack/react-router"
import { Controller, useForm } from "react-hook-form"

import { BackLink } from "@/components/back-link"
import { ErrorBanner } from "@/components/error-banner"
import { PageHeading } from "@/components/page-heading"
import { Button } from "@/components/ui/button"
import { Field, FieldError, FieldTitle } from "@/components/ui/field"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import { useCreateSnapshot } from "@/mutations/ec2"
import { ec2VolumesQueryOptions } from "@/queries/ec2"
import { type CreateSnapshotFormData, createSnapshotSchema } from "@/types/ec2"

export const Route = createFileRoute("/_auth/ec2/(snapshots)/create-snapshot")({
  validateSearch: (search: { volumeId?: string } & SearchSchemaInput) => ({
    volumeId: typeof search.volumeId === "string" ? search.volumeId : undefined,
  }),
  loader: async ({ context }) => {
    await context.queryClient.ensureQueryData(ec2VolumesQueryOptions)
  },
  head: () => ({
    meta: [
      {
        title: "Create Snapshot | EC2 | Mulga",
      },
    ],
  }),
  component: CreateSnapshot,
})

function CreateSnapshot() {
  const navigate = useNavigate()
  const { data: volumesData } = useSuspenseQuery(ec2VolumesQueryOptions)
  const createMutation = useCreateSnapshot()
  const volumes = volumesData.Volumes ?? []

  const { volumeId: searchVolumeId } = Route.useSearch()
  const defaultVolumeId = searchVolumeId ?? volumes[0]?.VolumeId ?? ""

  const {
    control,
    handleSubmit,
    register,
    formState: { errors, isSubmitting },
  } = useForm<CreateSnapshotFormData>({
    resolver: zodResolver(createSnapshotSchema),
    defaultValues: {
      volumeId: defaultVolumeId,
      description: "",
    },
  })

  const onSubmit = async (data: CreateSnapshotFormData) => {
    await createMutation.mutateAsync(data)
    navigate({ to: "/ec2/describe-snapshots" })
  }

  return (
    <>
      <BackLink to="/ec2/describe-snapshots">Back to snapshots</BackLink>

      <PageHeading title="Create Snapshot" />

      {createMutation.error && (
        <ErrorBanner
          error={createMutation.error}
          msg="Failed to create snapshot"
        />
      )}

      <form className="max-w-4xl space-y-6" onSubmit={handleSubmit(onSubmit)}>
        <Field>
          <FieldTitle>
            <label htmlFor="volumeId">Volume</label>
          </FieldTitle>
          <Controller
            control={control}
            name="volumeId"
            render={({ field }) => (
              <Select
                onValueChange={(value) => field.onChange(value)}
                value={field.value ?? ""}
              >
                <SelectTrigger
                  aria-invalid={!!errors.volumeId}
                  className="w-full"
                  id="volumeId"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {volumes.map((volume) => (
                    <SelectItem
                      key={volume.VolumeId}
                      value={volume.VolumeId ?? ""}
                    >
                      {volume.VolumeId} ({volume.Size} GiB)
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          />
          <FieldError errors={[errors.volumeId]} />
        </Field>

        <Field>
          <FieldTitle>
            <label htmlFor="description">Description (optional)</label>
          </FieldTitle>
          <Textarea
            id="description"
            placeholder="Snapshot description"
            {...register("description")}
          />
        </Field>

        <div className="flex gap-2">
          <Button
            disabled={isSubmitting || createMutation.isPending}
            onClick={() => navigate({ to: "/ec2/describe-snapshots" })}
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
              : "Create Snapshot"}
          </Button>
        </div>
      </form>
    </>
  )
}
