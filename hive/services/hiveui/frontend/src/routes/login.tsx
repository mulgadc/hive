import { DescribeInstancesCommand } from "@aws-sdk/client-ec2"
import { zodResolver } from "@hookform/resolvers/zod"
import { createFileRoute, redirect, useNavigate } from "@tanstack/react-router"
import { useState } from "react"
import { useForm } from "react-hook-form"

import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
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
  clearCredentials,
  getCredentials,
  setCredentials,
} from "@/lib/auth"
import { clearClients, getEc2Client } from "@/lib/awsClient"

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
  const [authError, setAuthError] = useState<string | null>(null)
  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm({
    resolver: zodResolver(awsCredentialsSchema),
  })

  async function onSubmit(data: AwsCredentials) {
    setAuthError(null)
    // Clear cached clients so the new creds are picked up
    clearClients()
    setCredentials(data)
    try {
      await getEc2Client().send(new DescribeInstancesCommand({}))
      navigate({ to: "/" })
    } catch {
      clearCredentials()
      clearClients()
      setAuthError(
        "Invalid credentials. Please check your Access Key ID and Secret Access Key.",
      )
    }
  }

  return (
    <div className="flex flex-1 items-center justify-center">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>AWS Credentials</CardTitle>
        </CardHeader>
        <CardContent>
          {authError && (
            <p className="mb-4 rounded-md bg-destructive/10 p-3 text-sm text-destructive">
              {authError}
            </p>
          )}
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
                {isSubmitting ? "Signing in..." : "Sign In"}
              </Button>
            </FieldGroup>
          </form>
        </CardContent>
      </Card>
    </div>
  )
}
