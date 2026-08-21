/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { AlertTriangle, Loader2, RotateCcw } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogMedia,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

import { executeBodyAuditReplay, previewBodyAuditReplay } from '../../api'
import { summarizeReplayBody } from '../../lib/audit-replay'
import type {
  AuditReplayMode,
  AuditReplayPreview,
  AuditReplayResult,
} from '../../types'

function readableReason(reason?: string): string {
  return reason || 'unknown_reason'
}

function requestErrorMessage(reason: unknown): string {
  if (reason instanceof Error) {
    const responseMessage = (
      reason as Error & { response?: { data?: { message?: unknown } } }
    ).response?.data?.message
    if (typeof responseMessage === 'string' && responseMessage) {
      return responseMessage
    }
    return reason.message
  }
  return String(reason)
}

export function ReplayPreviewDetails(props: { preview: AuditReplayPreview }) {
  const { t } = useTranslation()
  const bodySummary = useMemo(
    () => summarizeReplayBody(props.preview.body),
    [props.preview.body]
  )

  if (!props.preview.available) {
    return (
      <div className='rounded-md border border-dashed px-3 py-3 text-xs'>
        <div className='font-medium'>
          {t('This replay mode is unavailable')}
        </div>
        <div className='text-muted-foreground mt-1 font-mono'>
          {t(readableReason(props.preview.unavailable_reason))}
        </div>
      </div>
    )
  }

  return (
    <div className='min-w-0 space-y-3 text-xs'>
      <div className='grid min-w-0 gap-1 rounded-md border p-3 font-mono'>
        <span>{props.preview.method || 'POST'}</span>
        <span className='text-muted-foreground break-all'>
          {props.preview.historical_target || '—'}
        </span>
        <span className='break-all'>→ {props.preview.target || '—'}</span>
      </div>
      {(props.preview.differences?.length ?? 0) > 0 && (
        <div>
          <div className='mb-1.5 font-medium'>{t('Replay differences')}</div>
          <ul className='text-muted-foreground list-disc space-y-1 pl-5'>
            {props.preview.differences?.map((difference) => (
              <li key={`${difference.path}-${difference.change}`}>
                <span className='text-foreground font-mono'>
                  {difference.path}
                </span>{' '}
                · {t(difference.change)}
              </li>
            ))}
          </ul>
        </div>
      )}
      {bodySummary && (
        <div>
          <div className='mb-1.5 font-medium'>{t('Request body summary')}</div>
          <pre className='bg-muted/35 max-h-44 max-w-full overflow-auto rounded-md border p-3 font-mono text-[11px] break-words whitespace-pre-wrap'>
            {bodySummary}
          </pre>
        </div>
      )}
      <div className='border-destructive/30 bg-destructive/5 rounded-md border p-3'>
        <div className='text-destructive mb-1.5 flex items-center gap-1.5 font-medium'>
          <AlertTriangle className='size-3.5' aria-hidden='true' />
          {t('Replay risks')}
        </div>
        <ul className='text-muted-foreground list-disc space-y-1 pl-5'>
          {props.preview.risks?.map((risk) => (
            <li key={risk}>{t(risk)}</li>
          ))}
        </ul>
      </div>
    </div>
  )
}

function ResultPanel(props: { result: AuditReplayResult }) {
  const { t } = useTranslation()
  return (
    <div className='space-y-2 rounded-md border p-3 text-xs'>
      <div className='flex flex-wrap gap-x-4 gap-y-1 font-mono'>
        <span>
          {t('State')}: {props.result.state}
        </span>
        <span>HTTP {props.result.response_status || '—'}</span>
      </div>
      {props.result.replay_request_id && (
        <div>
          {t('Replay request ID')}:{' '}
          <code className='break-all'>{props.result.replay_request_id}</code>
        </div>
      )}
      {props.result.idempotent_replay && (
        <div className='text-amber-700 dark:text-amber-300'>
          {t(
            'This confirmation was already used; the existing result was returned without sending again'
          )}
        </div>
      )}
      {props.result.error && (
        <div className='text-destructive break-words'>{props.result.error}</div>
      )}
      {props.result.response_body && (
        <div>
          <div className='mb-1 font-medium'>{t('Replay response summary')}</div>
          <pre className='bg-muted/35 max-h-40 max-w-full overflow-auto rounded-md border p-2 font-mono text-[11px] break-words whitespace-pre-wrap'>
            {summarizeReplayBody(props.result.response_body)}
          </pre>
          {props.result.response_truncated && (
            <div className='mt-1 text-amber-700 dark:text-amber-300'>
              {t('Replay response was truncated during capture')}
            </div>
          )}
        </div>
      )}
    </div>
  )
}

export function AuditReplayDialog(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
  requestId: string
  attemptId: number
}) {
  const { t } = useTranslation()
  const [previews, setPreviews] = useState<
    Partial<Record<AuditReplayMode, AuditReplayPreview>>
  >({})
  const [selectedMode, setSelectedMode] =
    useState<AuditReplayMode>('exact_upstream')
  const [loading, setLoading] = useState(false)
  const [executing, setExecuting] = useState(false)
  const [error, setError] = useState('')
  const [result, setResult] = useState<AuditReplayResult | null>(null)
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    if (!props.open) return
    let active = true
    setLoading(true)
    setError('')
    setResult(null)
    setPreviews({})
    const load = async () => {
      try {
        const entries = await Promise.all(
          (['exact_upstream', 'client_level'] as const).map(
            async (mode) =>
              [
                mode,
                await previewBodyAuditReplay(
                  props.requestId,
                  props.attemptId,
                  mode
                ),
              ] as const
          )
        )
        if (active) setPreviews(Object.fromEntries(entries))
      } catch (reason) {
        if (active) {
          setError(requestErrorMessage(reason))
        }
      } finally {
        if (active) setLoading(false)
      }
    }
    void load()
    return () => {
      active = false
    }
  }, [props.attemptId, props.open, props.requestId])

  useEffect(() => {
    if (!props.open) return
    setNow(Date.now())
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [props.open])

  const selected = previews[selectedMode]
  const expiresInSeconds = selected?.expires_at
    ? Math.max(0, Math.floor((selected.expires_at - now) / 1000))
    : null
  const expired = expiresInSeconds === 0

  const execute = async () => {
    if (!selected?.confirmation_token || expired) return
    setExecuting(true)
    setError('')
    try {
      setResult(
        await executeBodyAuditReplay(
          props.requestId,
          selected.confirmation_token
        )
      )
    } catch (reason) {
      setError(requestErrorMessage(reason))
    } finally {
      setExecuting(false)
    }
  }

  let previewContent: React.ReactNode = null
  if (loading) {
    previewContent = (
      <div className='text-muted-foreground flex min-h-32 items-center justify-center gap-2 text-sm'>
        <Loader2 className='size-4 animate-spin' />{' '}
        {t('Preparing replay preview')}
      </div>
    )
  } else if (selected) {
    previewContent = <ReplayPreviewDetails preview={selected} />
  }

  return (
    <AlertDialog open={props.open} onOpenChange={props.onOpenChange}>
      <AlertDialogContent className='flex max-h-[min(88dvh,760px)] w-[calc(100vw-1.5rem)] !max-w-[56rem] grid-rows-none flex-col gap-0 overflow-hidden !p-0 sm:w-[min(56rem,calc(100vw-3rem))]'>
        <AlertDialogHeader className='shrink-0 px-4 pt-4 pb-3 text-left sm:px-6 sm:pt-6'>
          <AlertDialogMedia>
            <RotateCcw aria-hidden='true' />
          </AlertDialogMedia>
          <AlertDialogTitle>{t('Replay upstream attempt')}</AlertDialogTitle>
          <AlertDialogDescription>
            {t('Preview the exact operation before sending a real request')}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <div
          data-slot='audit-replay-scroll-area'
          className='min-h-0 min-w-0 flex-1 space-y-4 overflow-x-hidden overflow-y-auto px-4 pb-4 sm:px-6'
        >
          <div className='flex flex-wrap gap-2'>
            {(['exact_upstream', 'client_level'] as const).map((mode) => (
              <Button
                key={mode}
                type='button'
                size='sm'
                variant={selectedMode === mode ? 'default' : 'outline'}
                onClick={() => setSelectedMode(mode)}
              >
                {t(
                  mode === 'exact_upstream' ? 'Exact upstream' : 'Client level'
                )}
              </Button>
            ))}
          </div>
          {previewContent}
          {selected?.available && expiresInSeconds != null && !result && (
            <div
              className={cn(
                'text-xs',
                expired ? 'text-destructive' : 'text-muted-foreground'
              )}
            >
              {expired
                ? t('Confirmation expired; close and preview again')
                : t('One-time confirmation expires in {{seconds}} seconds', {
                    seconds: expiresInSeconds,
                  })}
            </div>
          )}
          {result && <ResultPanel result={result} />}
          {error && <div className='text-destructive text-xs'>{error}</div>}
        </div>
        <AlertDialogFooter className='mx-0 mb-0 shrink-0 flex-wrap rounded-b-xl border-t p-3 sm:p-4'>
          <AlertDialogCancel>{t('Close')}</AlertDialogCancel>
          {!result && (
            <AlertDialogAction
              variant='destructive'
              disabled={
                !selected?.available ||
                !selected.confirmation_token ||
                expired ||
                executing
              }
              onClick={execute}
            >
              {executing && <Loader2 className='size-4 animate-spin' />}
              {t('Confirm and send real upstream request')}
            </AlertDialogAction>
          )}
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
