import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"

import { renderWithProviders } from "@/test/utils"
import { InstanceActions } from "./instance-actions"

const TRANSITIONING_RE = /Actions will be available/
const TERMINATED_RE = /terminated and cannot be managed/

// Mock mutation hooks
const mockMutate = vi.fn()
const defaultMutation = { mutate: mockMutate, isPending: false, error: null }

vi.mock("@/mutations/ec2", () => ({
  useStartInstance: () => ({ ...defaultMutation }),
  useStopInstance: () => ({ ...defaultMutation }),
  useRebootInstance: () => ({ ...defaultMutation }),
  useTerminateInstance: () => ({ ...defaultMutation }),
}))

describe("InstanceActions", () => {
  describe("running instance", () => {
    it("shows Stop, Reboot, and Terminate buttons", () => {
      renderWithProviders(
        <InstanceActions instanceId="i-123" state="running" />,
      )
      expect(screen.getByText("Stop")).toBeInTheDocument()
      expect(screen.getByText("Reboot")).toBeInTheDocument()
      expect(screen.getByText("Terminate")).toBeInTheDocument()
      expect(screen.queryByText("Start")).not.toBeInTheDocument()
    })

    it("calls stop mutation on click", async () => {
      const user = userEvent.setup()
      renderWithProviders(
        <InstanceActions instanceId="i-123" state="running" />,
      )
      await user.click(screen.getByText("Stop"))
      expect(mockMutate).toHaveBeenCalledWith("i-123")
    })
  })

  describe("stopped instance", () => {
    it("shows Start and Terminate buttons", () => {
      renderWithProviders(
        <InstanceActions instanceId="i-123" state="stopped" />,
      )
      expect(screen.getByText("Start")).toBeInTheDocument()
      expect(screen.getByText("Terminate")).toBeInTheDocument()
      expect(screen.queryByText("Stop")).not.toBeInTheDocument()
      expect(screen.queryByText("Reboot")).not.toBeInTheDocument()
    })
  })

  describe("transitioning states", () => {
    it.each([
      "pending",
      "stopping",
      "shutting-down",
    ])("shows transitioning message for '%s' state", (state) => {
      renderWithProviders(<InstanceActions instanceId="i-123" state={state} />)
      expect(screen.getByText(TRANSITIONING_RE)).toBeInTheDocument()
      expect(screen.queryByText("Start")).not.toBeInTheDocument()
      expect(screen.queryByText("Stop")).not.toBeInTheDocument()
    })
  })

  describe("terminated instance", () => {
    it("shows terminated message", () => {
      renderWithProviders(
        <InstanceActions instanceId="i-123" state="terminated" />,
      )
      expect(screen.getByText(TERMINATED_RE)).toBeInTheDocument()
    })
  })
})
