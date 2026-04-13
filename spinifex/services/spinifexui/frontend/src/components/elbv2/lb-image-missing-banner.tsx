import { LB_IMAGE_NAME } from "@/lib/system-managed"

export function LbImageMissingBanner() {
  return (
    <div className="mb-6 max-w-4xl rounded-md border border-amber-500/50 bg-amber-500/10 p-4">
      <h2 className="text-sm font-semibold">
        Load balancer image not imported
      </h2>
      <p className="mt-1 text-sm text-muted-foreground">
        The load balancer system image has not been imported on this cluster. A
        cluster administrator must run the following command before load
        balancers can be created:
      </p>
      <pre className="mt-2 overflow-x-auto rounded bg-muted px-3 py-2 font-mono text-xs">
        spx admin images import --name {LB_IMAGE_NAME}
      </pre>
    </div>
  )
}
