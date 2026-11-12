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
import { FileText, PanelLeft } from 'lucide-react'
import { useEffect, useId, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { EmptyState } from '@/components/empty-state'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'

import {
  displayValue,
  documentTitle,
  type AuditDocument,
} from '../../lib/audit-workspace'
import { ReadingViewport, TextBlock, ValueExplorer } from './reader'

function DocumentBody(props: {
  document: AuditDocument
  notice?: ReactNode
  showHeading?: boolean
}) {
  const { t } = useTranslation()
  const doc = props.document
  const hasMetadata = !!doc.metadata && Object.keys(doc.metadata).length > 0
  const notice = (
    <div className='mb-3 space-y-2 empty:hidden'>
      {props.showHeading && (
        <h3 className='text-sm font-medium [overflow-wrap:anywhere]'>
          {documentTitle(doc, t)}
        </h3>
      )}
      {props.notice}
      {doc.category === 'tool' && !hasMetadata && (
        <p className='text-muted-foreground text-xs leading-5'>
          {t('Tool call arguments, not an execution result.')}
        </p>
      )}
      {doc.invalid && (
        <p role='status' className='mb-4 text-sm text-amber-600'>
          {doc.category === 'tool'
            ? t('Tool arguments are not valid JSON')
            : t('This payload is not valid JSON. Showing the captured text.')}
        </p>
      )}
      {hasMetadata && (
        <details className='text-xs'>
          <summary className='text-muted-foreground cursor-pointer py-1'>
            {doc.category === 'tool'
              ? t('Tool call arguments, not an execution result.')
              : t('Message metadata')}
          </summary>
          <TextBlock code text={displayValue(doc.metadata)} />
        </details>
      )}
    </div>
  )
  if (!doc.media && !(typeof doc.value === 'string' && !doc.value.trim())) {
    return <ValueExplorer value={doc.value} notice={notice} />
  }
  return (
    <ReadingViewport>
      {notice}
      {doc.media?.kind === 'image' && (
        <img
          loading='lazy'
          src={doc.media.source}
          alt={doc.media.label || t('Generated image')}
          className='max-h-full max-w-full object-contain'
        />
      )}
      {doc.media?.kind === 'audio' && (
        <audio
          controls
          preload='none'
          src={doc.media.source}
          className='w-full'
        />
      )}
      {doc.media?.kind === 'video' && (
        <video
          controls
          preload='none'
          src={doc.media.source}
          className='max-h-full w-full'
        />
      )}
      {!doc.media && typeof doc.value === 'string' && !doc.value.trim() ? (
        <EmptyState
          icon={FileText}
          title={t('This message has no text content.')}
          description={t(
            'The original value is preserved. Select another section to continue reading.'
          )}
          className='min-h-0 py-8'
        />
      ) : (
        !doc.media && (
          <TextBlock
            text={displayValue(doc.value)}
            code={doc.invalid || typeof doc.value !== 'string'}
          />
        )
      )}
    </ReadingViewport>
  )
}

export function DocumentBrowser(props: {
  documents: AuditDocument[]
  notice?: ReactNode
}) {
  const { t } = useTranslation()
  const indexId = useId()
  const indexRef = useRef<HTMLElement>(null)
  const [indexOpen, setIndexOpen] = useState(false)
  const [selectedId, setSelectedId] = useState(() => {
    const readable = props.documents.filter((doc) => {
      if (typeof doc.value === 'string') return doc.value.trim().length > 0
      if (doc.value === null || doc.value === undefined) return false
      if (typeof doc.value === 'object') {
        return Object.keys(doc.value).length > 0
      }
      return true
    })
    const message = readable.find(
      (doc) => doc.category === 'message' || doc.category === 'context'
    )
    return (message ?? readable[0] ?? props.documents[0])?.id
  })
  const [visible, setVisible] = useState(() =>
    Math.max(60, props.documents.findIndex((doc) => doc.id === selectedId) + 1)
  )
  const selectDocument = (id: string) => {
    setSelectedId(id)
    setVisible((count) =>
      Math.max(count, props.documents.findIndex((doc) => doc.id === id) + 1)
    )
  }
  useEffect(() => {
    const index = indexRef.current
    const selected = index?.querySelector<HTMLElement>('[aria-current="true"]')
    if (!index || !selected) return
    const viewport = index.getBoundingClientRect()
    const item = selected.getBoundingClientRect()
    if (item.top < viewport.top) index.scrollTop += item.top - viewport.top
    else if (item.bottom > viewport.bottom) {
      index.scrollTop += item.bottom - viewport.bottom
    }
  }, [selectedId, indexOpen])
  const active =
    props.documents.find((doc) => doc.id === selectedId) ?? props.documents[0]
  if (!active) {
    return (
      <ReadingViewport>
        {props.notice}
        <p className='text-muted-foreground text-sm'>
          {t('No readable content')}
        </p>
      </ReadingViewport>
    )
  }
  return (
    <div className='flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden'>
      {props.documents.length > 1 && (
        <div
          data-audit-chrome='document'
          className='bg-muted/15 flex shrink-0 items-center gap-2 border-b px-3 py-1.5'
        >
          <Button
            variant={indexOpen ? 'secondary' : 'ghost'}
            size='sm'
            className='hidden lg:inline-flex'
            aria-label={t('Content index')}
            aria-controls={indexId}
            aria-expanded={indexOpen}
            onClick={() => setIndexOpen(!indexOpen)}
          >
            <PanelLeft className='size-4' aria-hidden='true' />
            {t('Content index')}
          </Button>
          <Combobox
            className='min-w-0 flex-1 sm:max-w-md'
            aria-label={t('Reading section')}
            value={active.id}
            onValueChange={(value) => value && selectDocument(value)}
            searchPlaceholder={t('Search sections...')}
            emptyText={t('No sections found')}
            options={props.documents.map((doc) => ({
              value: doc.id,
              label: documentTitle(doc, t),
            }))}
          />
          <span className='text-muted-foreground ml-auto shrink-0 text-xs tabular-nums'>
            {props.documents.indexOf(active) + 1} / {props.documents.length}
          </span>
          <CopyButton
            size='sm'
            value={displayValue(active.value)}
            tooltip={t('Copy document')}
          />
        </div>
      )}
      <div className='flex min-h-0 min-w-0 flex-1 overflow-hidden'>
        {props.documents.length > 1 && indexOpen && (
          <nav
            ref={indexRef}
            id={indexId}
            aria-label={t('Content index')}
            className='bg-muted/15 hidden w-52 shrink-0 overflow-y-auto overscroll-contain border-r p-2 lg:block'
          >
            <p className='text-muted-foreground px-2 py-2 text-xs'>
              {t('Content index')} · {props.documents.length}
            </p>
            {props.documents.slice(0, visible).map((doc) => (
              <Button
                key={doc.id}
                variant={doc.id === active.id ? 'secondary' : 'ghost'}
                aria-current={doc.id === active.id ? 'true' : undefined}
                onClick={() => selectDocument(doc.id)}
                title={documentTitle(doc, t)}
                className='mb-1 h-auto w-full justify-start px-2 py-2 text-left'
              >
                <span className='min-w-0 text-xs leading-5 [overflow-wrap:anywhere] whitespace-normal'>
                  {documentTitle(doc, t)}
                </span>
              </Button>
            ))}
            {visible < props.documents.length && (
              <Button
                variant='ghost'
                size='sm'
                onClick={() => setVisible(visible + 60)}
              >
                {t('Load more')}
              </Button>
            )}
          </nav>
        )}
        <div className='flex min-h-0 min-w-0 flex-1 flex-col'>
          <DocumentBody
            key={active.id}
            document={active}
            notice={props.notice}
            showHeading={props.documents.length === 1}
          />
        </div>
      </div>
    </div>
  )
}
