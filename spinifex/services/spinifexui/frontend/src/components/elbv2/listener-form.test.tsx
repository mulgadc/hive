import type { TargetGroup } from "@aws-sdk/client-elastic-load-balancing-v2"
import { render, screen } from "@testing-library/react"
import { useForm } from "react-hook-form"
import { describe, expect, it } from "vitest"

import type { CreateListenerFormData } from "@/types/elbv2"

import { ListenerForm } from "./listener-form"

const TGS: TargetGroup[] = [
  {
    TargetGroupArn: "arn:tg/a",
    TargetGroupName: "tg-a",
    Protocol: "HTTP",
    Port: 80,
    VpcId: "vpc-aaa",
  },
  {
    TargetGroupArn: "arn:tg/b",
    TargetGroupName: "tg-b",
    Protocol: "HTTP",
    Port: 8080,
    VpcId: "vpc-aaa",
  },
]

function Harness({ targetGroups }: { targetGroups: TargetGroup[] }) {
  const form = useForm<CreateListenerFormData>({
    defaultValues: {
      protocol: "HTTP",
      port: 80,
      defaultTargetGroupArn: "",
    },
  })
  return <ListenerForm form={form} targetGroups={targetGroups} />
}

describe("ListenerForm", () => {
  it("renders protocol, port, and target-group selectors", () => {
    render(<Harness targetGroups={TGS} />)
    expect(screen.getByLabelText("Protocol")).toBeInTheDocument()
    expect(screen.getByLabelText("Port")).toBeInTheDocument()
    expect(screen.getByLabelText("Default target group")).toBeInTheDocument()
  })

  it("shows empty-state message when no target groups available", () => {
    render(<Harness targetGroups={[]} />)
    expect(
      screen.getByText(/no target groups available in this vpc/i),
    ).toBeInTheDocument()
  })
})
