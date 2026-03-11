import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"

import { FormActions } from "./form-actions"

describe("FormActions", () => {
  it("renders submit and cancel buttons", () => {
    render(
      <FormActions
        isSubmitting={false}
        isPending={false}
        onCancel={vi.fn()}
        submitLabel="Create"
        pendingLabel="Creating..."
      />,
    )
    expect(screen.getByRole("button", { name: "Create" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Cancel" })).toBeInTheDocument()
  })

  it("shows pending label when submitting", () => {
    render(
      <FormActions
        isSubmitting={true}
        isPending={false}
        onCancel={vi.fn()}
        submitLabel="Create"
        pendingLabel="Creating..."
      />,
    )
    expect(
      screen.getByRole("button", { name: "Creating..." }),
    ).toBeInTheDocument()
  })

  it("shows pending label when isPending", () => {
    render(
      <FormActions
        isSubmitting={false}
        isPending={true}
        onCancel={vi.fn()}
        submitLabel="Create"
        pendingLabel="Creating..."
      />,
    )
    expect(
      screen.getByRole("button", { name: "Creating..." }),
    ).toBeInTheDocument()
  })

  it("disables both buttons when submitting", () => {
    render(
      <FormActions
        isSubmitting={true}
        isPending={false}
        onCancel={vi.fn()}
        submitLabel="Create"
        pendingLabel="Creating..."
      />,
    )
    expect(screen.getByRole("button", { name: "Creating..." })).toBeDisabled()
    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled()
  })

  it("calls onCancel when cancel is clicked", async () => {
    const onCancel = vi.fn()
    const user = userEvent.setup()
    render(
      <FormActions
        isSubmitting={false}
        isPending={false}
        onCancel={onCancel}
        submitLabel="Create"
        pendingLabel="Creating..."
      />,
    )
    await user.click(screen.getByRole("button", { name: "Cancel" }))
    expect(onCancel).toHaveBeenCalledOnce()
  })
})
