import type { TargetGroup } from "@aws-sdk/client-elastic-load-balancing-v2"
import type { UseFormReturn } from "react-hook-form"
import { Controller } from "react-hook-form"

import { Field, FieldError, FieldTitle } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import type { CreateListenerFormData } from "@/types/elbv2"

interface ListenerFormProps {
  form: UseFormReturn<CreateListenerFormData>
  targetGroups: TargetGroup[]
}

export function ListenerForm({ form, targetGroups }: ListenerFormProps) {
  const {
    control,
    register,
    formState: { errors },
  } = form

  return (
    <>
      <Field>
        <FieldTitle>
          <label htmlFor="listener-protocol">Protocol</label>
        </FieldTitle>
        <Controller
          control={control}
          name="protocol"
          render={({ field }) => (
            <Select onValueChange={field.onChange} value={field.value}>
              <SelectTrigger className="w-full" id="listener-protocol">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="HTTP">HTTP</SelectItem>
              </SelectContent>
            </Select>
          )}
        />
      </Field>

      <Field>
        <FieldTitle>
          <label htmlFor="listener-port">Port</label>
        </FieldTitle>
        <Input
          aria-invalid={!!errors.port}
          id="listener-port"
          inputMode="numeric"
          placeholder="80"
          type="number"
          {...register("port", { valueAsNumber: true })}
        />
        <FieldError errors={[errors.port]} />
      </Field>

      <Field>
        <FieldTitle>
          <label htmlFor="listener-default-tg">Default target group</label>
        </FieldTitle>
        <Controller
          control={control}
          name="defaultTargetGroupArn"
          render={({ field }) => (
            <Select onValueChange={field.onChange} value={field.value ?? ""}>
              <SelectTrigger
                aria-invalid={!!errors.defaultTargetGroupArn}
                className="w-full"
                id="listener-default-tg"
              >
                <SelectValue placeholder="Select target group" />
              </SelectTrigger>
              <SelectContent>
                {targetGroups.map((tg) => (
                  <SelectItem
                    key={tg.TargetGroupArn}
                    value={tg.TargetGroupArn ?? ""}
                  >
                    {tg.TargetGroupName} · {tg.Protocol}:{tg.Port}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        />
        <FieldError errors={[errors.defaultTargetGroupArn]} />
        {targetGroups.length === 0 && (
          <p className="mt-1 text-xs text-muted-foreground">
            No target groups available in this VPC. Create one first.
          </p>
        )}
      </Field>
    </>
  )
}
