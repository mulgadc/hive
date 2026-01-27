import { useNavigate } from "@tanstack/react-router"
import { AlertTriangle, Check, Download } from "lucide-react"
import { useState } from "react"

import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogMedia,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { Textarea } from "@/components/ui/textarea"

const COPY_FEEDBACK_DURATION_MS = 2000

interface PrivateKeyModalProps {
  open: boolean
  keyName: string
  keyMaterial: string
}

export function PrivateKeyModal({
  open,
  keyName,
  keyMaterial,
}: PrivateKeyModalProps) {
  const navigate = useNavigate()
  const [copied, setCopied] = useState(false)

  const handleCopy = async () => {
    await navigator.clipboard.writeText(keyMaterial)
    setCopied(true)
    setTimeout(() => setCopied(false), COPY_FEEDBACK_DURATION_MS)
  }

  const handleDownload = () => {
    const blob = new Blob([keyMaterial], { type: "text/plain" })
    const url = URL.createObjectURL(blob)
    const link = document.createElement("a")
    link.href = url
    link.download = `${keyName}.pem`
    link.click()
    URL.revokeObjectURL(url)
  }

  const handleClose = () => {
    navigate({ to: "/ec2/describe-key-pairs" })
  }

  return (
    <AlertDialog open={open}>
      <AlertDialogContent className="max-w-2xl">
        <AlertDialogHeader>
          <AlertDialogMedia>
            <AlertTriangle className="text-destructive" />
          </AlertDialogMedia>
          <AlertDialogTitle>Save Your Private Key</AlertDialogTitle>
          <AlertDialogDescription>
            This is your only chance to save the private key file for{" "}
            <strong>{keyName}</strong>. Please download or copy it now. You
            won't be able to retrieve it again.
          </AlertDialogDescription>
        </AlertDialogHeader>

        <div className="space-y-3">
          <Textarea
            className="font-mono text-xs"
            readOnly
            rows={12}
            value={keyMaterial}
          />

          <div className="flex gap-2">
            <Button
              className="flex-1"
              onClick={handleCopy}
              type="button"
              variant="outline"
            >
              {copied ? (
                <>
                  <Check className="mr-2 size-4" />
                  Copied!
                </>
              ) : (
                "Copy to Clipboard"
              )}
            </Button>
            <Button
              className="flex-1"
              onClick={handleDownload}
              type="button"
              variant="outline"
            >
              <Download className="mr-2 size-4" />
              Download .pem File
            </Button>
          </div>
        </div>

        <AlertDialogFooter>
          <AlertDialogCancel onClick={handleClose}>
            Close and Continue
          </AlertDialogCancel>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
