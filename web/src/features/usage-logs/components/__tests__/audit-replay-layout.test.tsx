import { render, screen } from '@testing-library/react'
import { describe, expect, test, vi } from 'vitest'

import { AuditReplayDialog } from '../dialogs/audit-replay-dialog'

const { previewReplay } = vi.hoisted(() => ({
  previewReplay: vi.fn(),
}))

vi.mock('../../api', () => ({
  executeBodyAuditReplay: vi.fn(),
  previewBodyAuditReplay: previewReplay,
}))

describe('audit replay dialog layout', () => {
  test('uses a wide bounded dialog with one internal scroll area and fixed footer', async () => {
    previewReplay.mockImplementation(
      async (_requestId: string, _attemptId: number, mode: string) => ({
        mode,
        available: false,
        unavailable_reason: 'client_level_replay_not_supported',
      })
    )

    render(
      <AuditReplayDialog
        open
        onOpenChange={() => undefined}
        requestId='request-1'
        attemptId={7}
      />
    )

    const dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveClass('!max-w-[56rem]')
    expect(dialog).toHaveClass('overflow-hidden')
    expect(dialog).not.toHaveClass('overflow-y-auto')

    const scrollArea = dialog.querySelector(
      '[data-slot="audit-replay-scroll-area"]'
    )
    expect(scrollArea).toHaveClass('overflow-y-auto')
    expect(scrollArea).toHaveClass('overflow-x-hidden')

    const footer = dialog.querySelector('[data-slot="alert-dialog-footer"]')
    expect(footer).toHaveClass('shrink-0')
    expect(footer).toHaveClass('flex-wrap')
  })
})
