import { zodResolver } from "@hookform/resolvers/zod"
import { createFileRoute, redirect, useNavigate } from "@tanstack/react-router"
import { useForm } from "react-hook-form"

import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Field,
  FieldError,
  FieldGroup,
  FieldTitle,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  type AwsCredentials,
  awsCredentialsSchema,
  getCredentials,
  setCredentials,
} from "@/lib/auth"

export const Route = createFileRoute("/login")({
  beforeLoad: () => {
    if (getCredentials()) {
      throw redirect({ to: "/" })
    }
  },
  component: LoginPage,
})

function LoginPage() {
  const navigate = useNavigate()
  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<AwsCredentials>({
    resolver: zodResolver(awsCredentialsSchema),
  })

  function onSubmit(data: AwsCredentials) {
    setCredentials(data)
    navigate({ to: "/" })
  }

  return (
    <div className="flex flex-1 items-center justify-center">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>AWS Credentials</CardTitle>
          <CardDescription>
            Run <code>cat ~/hive/config/hive.toml</code> to get your credentials
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleSubmit(onSubmit)}>
            <FieldGroup>
              <Field>
                <FieldTitle>
                  <label htmlFor="accessKeyId">Access Key ID</label>
                </FieldTitle>
                <Input
                  aria-invalid={!!errors.accessKeyId}
                  autoComplete="username"
                  id="accessKeyId"
                  placeholder="AKIAIOSFODNN7EXAMPLE"
                  {...register("accessKeyId")}
                />
                <FieldError errors={[errors.accessKeyId]} />
              </Field>
              <Field>
                <FieldTitle>
                  <label htmlFor="secretAccessKey">Secret Access Key</label>
                </FieldTitle>
                <Input
                  aria-invalid={!!errors.secretAccessKey}
                  autoComplete="current-password"
                  id="secretAccessKey"
                  placeholder="wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
                  type="password"
                  {...register("secretAccessKey")}
                />
                <FieldError errors={[errors.secretAccessKey]} />
              </Field>
              <Button className="w-full" disabled={isSubmitting} type="submit">
                Sign In
              </Button>
            </FieldGroup>
          </form>
        </CardContent>
      </Card>
    </div>
  )
}
