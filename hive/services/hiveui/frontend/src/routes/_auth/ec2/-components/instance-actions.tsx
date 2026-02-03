import { Pause, Play, RotateCw, Trash2 } from "lucide-react"

import { ErrorBanner } from "@/components/error-banner"
import { Button } from "@/components/ui/button"
import {
  useRebootInstance,
  useStartInstance,
  useStopInstance,
  useTerminateInstance,
} from "@/mutations/ec2"

const TRANSITIONING_STATES = [
  "pending",
  "stopping",
  "shutting-down",
  "terminated",
]

interface InstanceActionsProps {
  instanceId: string
  state?: string
}

export function InstanceActions({ instanceId, state }: InstanceActionsProps) {
  const startMutation = useStartInstance()
  const stopMutation = useStopInstance()
  const rebootMutation = useRebootInstance()
  const terminateMutation = useTerminateInstance()

  const isTransitioning = TRANSITIONING_STATES.includes(state || "")

  if (isTransitioning && state !== "terminated") {
    return (
      <div className="rounded-lg border bg-muted/50 p-4">
        <p className="text-center text-muted-foreground text-sm">
          Instance is {state}. Actions will be available once the operation
          completes.
        </p>
      </div>
    )
  }

  if (state === "terminated") {
    return (
      <div className="rounded-lg border bg-muted/50 p-4">
        <p className="text-center text-muted-foreground text-sm">
          This instance has been terminated and cannot be managed.
        </p>
      </div>
    )
  }

  return (
    <div className="rounded-lg border bg-card p-4">
      <h2 className="mb-4 font-semibold">Instance Actions</h2>
      {startMutation.error && (
        <ErrorBanner
          error={startMutation.error}
          msg="Failed to start instance"
        />
      )}
      {stopMutation.error && (
        <ErrorBanner error={stopMutation.error} msg="Failed to stop instance" />
      )}
      {rebootMutation.error && (
        <ErrorBanner
          error={rebootMutation.error}
          msg="Failed to reboot instance"
        />
      )}
      {terminateMutation.error && (
        <ErrorBanner
          error={terminateMutation.error}
          msg="Failed to terminate instance"
        />
      )}
      <div className="flex flex-wrap gap-2">
        {state === "stopped" && (
          <Button
            disabled={startMutation.isPending}
            onClick={() => startMutation.mutate(instanceId)}
            size="sm"
            variant="default"
          >
            <Play className="size-4" />
            {startMutation.isPending ? "Starting" : "Start"}
          </Button>
        )}
        {state === "running" && (
          <>
            <Button
              disabled={stopMutation.isPending}
              onClick={() => stopMutation.mutate(instanceId)}
              size="sm"
              variant="outline"
            >
              <Pause className="size-4" />
              {stopMutation.isPending ? "Stopping" : "Stop"}
            </Button>
            <Button
              disabled={rebootMutation.isPending}
              onClick={() => rebootMutation.mutate(instanceId)}
              size="sm"
              variant="outline"
            >
              <RotateCw className="size-4" />
              {rebootMutation.isPending ? "Rebooting" : "Reboot"}
            </Button>
          </>
        )}
        {(state === "stopped" || state === "running") && (
          <Button
            disabled={terminateMutation.isPending}
            onClick={() => terminateMutation.mutate(instanceId)}
            size="sm"
            variant="destructive"
          >
            <Trash2 className="size-4" />
            {terminateMutation.isPending ? "Terminating" : "Terminate"}
          </Button>
        )}
      </div>
    </div>
  )
}
