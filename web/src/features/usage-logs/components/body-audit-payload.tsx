/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { StatusBadge } from '@/components/status-badge'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'

import { parseBodyAuditResponse } from '../lib/body-audit-response'
import { BodyAuditResultContent } from './body-audit-result'

interface PayloadPanelProps {
  title: string
  body: string
  encoding: 'utf-8' | 'base64'
  size: number
  truncated: boolean
  incomplete?: boolean
  contentType?: string
  icon: ReactNode
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

function requestReading(value: unknown): string | null {
  if (!value || typeof value !== 'object') return null
  const data = value as Record<string, unknown>
  const messages = data.messages ?? data.contents ?? data.input
  if (!Array.isArray(messages)) return null
  return messages.map((value, i) => {
    const message = value as Record<string, unknown>
    if (!message || typeof message !== 'object') return String(value)
    const content = message.content ?? message.parts ?? message.text
    let body = ''
    if (typeof content === 'string') body = content
    else if (Array.isArray(content)) {
      body = content.map(part => {
        if (typeof part === 'string') return part
        if (part && typeof part === 'object' && typeof part.text === 'string') return part.text
        return JSON.stringify(part, null, 2)
      }).join('\n\n')
    } else body = JSON.stringify(message, null, 2)
    if (message.tool_calls) body += `\n\n${JSON.stringify(message.tool_calls, null, 2)}`
    return `[${i + 1} · ${message.role ?? message.type ?? 'message'}]\n${body}`
  }).join('\n\n')
}

export function PayloadPanel(props: PayloadPanelProps) {
  const { t } = useTranslation()
  const [view, setView] = useState('formatted')
  const parsed = useMemo(() => {
    if (props.encoding !== 'utf-8') return { formatted: props.body, request: null }
    try {
      const json: unknown = JSON.parse(props.body)
      return { formatted: JSON.stringify(json, null, 2), request: requestReading(json) }
    } catch {
      return { formatted: props.body, request: null }
    }
  }, [props.body, props.encoding])
  const result = useMemo(
    () => view === 'readable' && !parsed.request ? parseBodyAuditResponse({
      body: props.body, encoding: props.encoding, contentType: props.contentType,
    }) : null,
    [view, parsed.request, props.body, props.encoding, props.contentType]
  )
  let warning: string | undefined
  if (props.truncated) warning = t('The body exceeded the audit size limit and was truncated')
  else if (props.incomplete) warning = t('Client response write was interrupted; only the bytes written before the failure were captured, possibly ending inside a character.')
  else if (props.encoding === 'base64') warning = t('Binary body is displayed as Base64')
  let visibleText = parsed.formatted
  if (view === 'raw') visibleText = props.body
  if (view === 'readable' && parsed.request !== null) visibleText = parsed.request
  const previewLimit = 128 * 1024
  const previewLimited = visibleText.length > previewLimit

  return (
    <div className='bg-background min-w-0 max-w-full overflow-hidden rounded-lg border'>
      <div className='bg-muted/35 flex flex-wrap items-center justify-between gap-2 border-b px-3 py-2'>
        <div className='flex min-w-0 flex-1 items-center gap-2 text-xs font-medium'>
          <span className='text-muted-foreground shrink-0'>{props.icon}</span>
          <span className='min-w-0 break-words [overflow-wrap:anywhere]'>{props.title}</span>
          <StatusBadge label={formatBytes(props.size)} variant='neutral' size='sm' copyable={false} />
        </div>
        <CopyButton value={props.body} size='sm' aria-label={t('Copy original')} tooltip={t('Copy original')}>
          {t('Copy original')}
        </CopyButton>
      </div>
      {warning && <Alert className='rounded-none border-x-0 border-t-0'><AlertDescription>{warning}</AlertDescription></Alert>}
      {props.encoding === 'utf-8' && (
        <Tabs value={view} onValueChange={setView} className='min-w-0 px-3 pt-2'>
          <TabsList className='h-auto max-w-full flex-wrap'>
            <TabsTrigger value='formatted'>{t('Formatted JSON')}</TabsTrigger>
            <TabsTrigger value='readable'>{t('Readable content')}</TabsTrigger>
            <TabsTrigger value='raw'>{t('Original payload')}</TabsTrigger>
          </TabsList>
        </Tabs>
      )}
      <div className='scrollbar-thin min-h-44 max-h-[min(44dvh,440px)] min-w-0 overflow-auto p-4 [overflow-wrap:anywhere]'>
        {view === 'readable' && result ? (
          <BodyAuditResultContent result={result} emptyLabel={t('No readable text was extracted')} imageAlt={t('Generated image')} />
        ) : (
          <pre className='font-mono text-xs leading-relaxed whitespace-pre-wrap [overflow-wrap:anywhere]'>
            {visibleText.slice(0, previewLimit) || t('Empty body')}
          </pre>
        )}
      </div>
      {previewLimited && !result && (
        <p className='text-muted-foreground border-t px-3 py-2 text-xs'>
          {t('Preview shortened for performance. Copy original retains the entire captured body.')}
        </p>
      )}
    </div>
  )
}
