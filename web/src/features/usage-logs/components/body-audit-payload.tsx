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
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

import {
  auditReadableCopy,
  auditResultDocument,
  formatAuditStream,
} from '../lib/body-audit-reading'
import {
  parseBodyAuditResponse,
  type ParsedBodyAuditResponse,
} from '../lib/body-audit-response'
import { AuditText } from './body-audit-json-content'
import { BodyAuditRequestContent } from './body-audit-request'
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
  request?: boolean
  finalResult?: ParsedBodyAuditResponse
  notice?: ReactNode
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

export function PayloadPanel(props: PayloadPanelProps) {
  const { t } = useTranslation()
  const [view, setView] = useState(
    props.finalResult || props.request ? 'readable' : 'formatted'
  )
  const hasViews = props.encoding === 'utf-8' || !!props.finalResult
  const activeView = hasViews ? view : 'raw'
  const parsed = useMemo(() => {
    if (props.encoding !== 'utf-8') {
      return { formatted: props.body, request: null }
    }
    try {
      const json: unknown = JSON.parse(props.body)
      return {
        formatted: JSON.stringify(json, null, 2),
        request: props.request ? json : null,
      }
    } catch {
      return { formatted: formatAuditStream(props.body), request: null }
    }
  }, [props.body, props.encoding, props.request])
  const result = useMemo(
    () =>
      props.finalResult ??
      (activeView === 'readable' && !props.request
        ? parseBodyAuditResponse({
            body: props.body,
            encoding: props.encoding,
            contentType: props.contentType,
          })
        : null),
    [
      props.finalResult,
      activeView,
      props.request,
      props.body,
      props.encoding,
      props.contentType,
    ]
  )
  let warning: string | undefined
  if (props.truncated) {
    warning = t('The body exceeded the audit size limit and was truncated')
  } else if (props.incomplete) {
    warning = t(
      'Client response write was interrupted; only the bytes written before the failure were captured, possibly ending inside a character.'
    )
  } else if (props.encoding === 'base64' && activeView === 'raw') {
    warning = t('Binary body is displayed as Base64')
  }
  const formatted = useMemo(
    () =>
      props.finalResult
        ? (JSON.stringify(auditResultDocument(props.finalResult), null, 2) ??
          '')
        : parsed.formatted,
    [props.finalResult, parsed.formatted]
  )
  let copyText = props.body
  let copyLabel = t('Copy original')
  if (props.finalResult && activeView !== 'raw') {
    copyText =
      activeView === 'formatted'
        ? formatted
        : auditReadableCopy(props.finalResult)
    copyLabel =
      activeView === 'formatted'
        ? t('Copy formatted data')
        : t('Copy readable content')
  }

  return (
    <div className='bg-background max-w-full min-w-0 overflow-hidden rounded-lg border'>
      <div className='bg-muted/35 flex flex-wrap items-center justify-between gap-2 border-b px-3 py-2'>
        <div className='flex min-w-0 flex-1 items-center gap-2 text-xs font-medium'>
          <span className='text-muted-foreground shrink-0'>{props.icon}</span>
          <span className='min-w-0 [overflow-wrap:anywhere] break-words'>
            {props.title}
          </span>
          <StatusBadge
            label={formatBytes(props.size)}
            variant='neutral'
            size='sm'
            copyable={false}
          />
        </div>
        <CopyButton
          value={copyText}
          size='sm'
          aria-label={copyLabel}
          tooltip={copyLabel}
        >
          {copyLabel}
        </CopyButton>
      </div>
      {warning && (
        <Alert className='rounded-none border-x-0 border-t-0'>
          <AlertDescription>{warning}</AlertDescription>
        </Alert>
      )}
      {props.notice}
      {hasViews ? (
        <Tabs
          value={activeView}
          onValueChange={setView}
          className='min-w-0 gap-0'
        >
          <TabsList className='mx-3 mt-2 h-auto max-w-full flex-wrap'>
            <TabsTrigger value='readable'>{t('Readable content')}</TabsTrigger>
            <TabsTrigger value='formatted'>{t('Formatted JSON')}</TabsTrigger>
            <TabsTrigger value='raw'>{t('Original payload')}</TabsTrigger>
          </TabsList>
          <TabsContent value='readable' className='min-w-0'>
            <div className='max-h-[min(56dvh,640px)] min-h-44 min-w-0 overflow-auto p-4 [overflow-wrap:anywhere]'>
              {props.request && parsed.request !== null && (
                <BodyAuditRequestContent value={parsed.request} />
              )}
              {props.request && parsed.request === null && (
                <AuditText text={props.body || t('Empty body')} code />
              )}
              {result && (
                <BodyAuditResultContent
                  result={result}
                  emptyLabel={t('No readable text was extracted')}
                  imageAlt={t('Generated image')}
                />
              )}
            </div>
          </TabsContent>
          <TabsContent value='formatted' className='min-w-0'>
            {props.finalResult && (
              <p className='text-muted-foreground px-4 pt-3 text-xs'>
                {t(
                  'Reconstructed result. Original payload preserves the captured response.'
                )}
              </p>
            )}
            <div className='max-h-[min(56dvh,640px)] min-h-44 min-w-0 overflow-auto p-4'>
              <AuditText text={formatted || t('Empty body')} code />
            </div>
          </TabsContent>
          <TabsContent value='raw' className='min-w-0'>
            <div className='max-h-[min(56dvh,640px)] min-h-44 min-w-0 overflow-auto p-4'>
              <AuditText text={props.body || t('Empty body')} code />
            </div>
          </TabsContent>
        </Tabs>
      ) : (
        <div className='max-h-[min(56dvh,640px)] min-h-44 min-w-0 overflow-auto p-4'>
          <AuditText text={props.body || t('Empty body')} code />
        </div>
      )}
    </div>
  )
}
