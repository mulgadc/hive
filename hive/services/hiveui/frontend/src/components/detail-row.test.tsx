import { render, screen } from "@testing-library/react"

import { DetailRow } from "./detail-row"

describe("DetailRow", () => {
  it("renders label and value", () => {
    render(<DetailRow label="Instance ID" value="i-abc123" />)
    expect(screen.getByText("Instance ID")).toBeInTheDocument()
    expect(screen.getByText("i-abc123")).toBeInTheDocument()
  })

  it("renders dash when value is undefined", () => {
    render(<DetailRow label="Subnet" />)
    expect(screen.getByText("—")).toBeInTheDocument()
  })

  it("renders empty string as value", () => {
    render(<DetailRow label="Subnet" value="" />)
    // With nullish coalescing, empty string is a valid value
    expect(screen.queryByText("—")).not.toBeInTheDocument()
  })

  it("renders ReactNode as value", () => {
    render(<DetailRow label="Status" value={<span>running</span>} />)
    expect(screen.getByText("running")).toBeInTheDocument()
  })
})
