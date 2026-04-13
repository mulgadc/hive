import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { renderHook, waitFor } from "@testing-library/react"
import type { ReactNode } from "react"
import { describe, expect, it } from "vitest"

import {
  useCreateListener,
  useCreateLoadBalancer,
  useCreateTargetGroup,
  useDeleteListener,
  useDeleteLoadBalancer,
  useDeleteTargetGroup,
  useDeregisterTargets,
  useModifyLoadBalancerAttributes,
  useModifyTargetGroupAttributes,
  useRegisterTargets,
} from "./elbv2"

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
  return function Wrapper({ children }: { children: ReactNode }) {
    return (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    )
  }
}

const stubs = [
  ["useCreateLoadBalancer", useCreateLoadBalancer],
  ["useDeleteLoadBalancer", useDeleteLoadBalancer],
  ["useModifyLoadBalancerAttributes", useModifyLoadBalancerAttributes],
  ["useCreateTargetGroup", useCreateTargetGroup],
  ["useDeleteTargetGroup", useDeleteTargetGroup],
  ["useModifyTargetGroupAttributes", useModifyTargetGroupAttributes],
  ["useCreateListener", useCreateListener],
  ["useDeleteListener", useDeleteListener],
  ["useRegisterTargets", useRegisterTargets],
  ["useDeregisterTargets", useDeregisterTargets],
] as const

describe("elbv2 mutation stubs throw until implemented", () => {
  for (const [name, useMutationHook] of stubs) {
    it(`${name} fires 'not implemented'`, async () => {
      const wrapper = createWrapper()
      const { result } = renderHook(() => useMutationHook(), { wrapper })

      result.current.mutate(undefined as never)

      await waitFor(() => expect(result.current.isError).toBe(true))
      expect(result.current.error?.message).toMatch(/not implemented/)
    })
  }
})
