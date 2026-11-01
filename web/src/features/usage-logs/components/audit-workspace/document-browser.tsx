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
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Button } from '@/components/ui/button'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

import {
  displayValue,
  documentTitle,
  type AuditDocument,
} from '../../lib/audit-workspace'
import { ReadingViewport, TextBlock, ValueExplorer } from './reader'

function DocumentBody(props: { document: AuditDocument; notice?: ReactNode }) {
  const { t } = useTranslation()
  const doc = props.document
  const notice = (
    <div className='space-y-3 empty:hidden'>
      {props.notice}
      {doc.category === 'tool' && (
        <p className='text-muted-foreground mb-4 text-xs'>
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
      {doc.metadata && Object.keys(doc.metadata).length > 0 && (
        <details className='mb-4 text-xs'>
          <summary className='text-muted-foreground cursor-pointer py-1'>
            {t('Message metadata')}
          </summary>
          <TextBlock code text={displayValue(doc.metadata)} />
        </details>
      )}
    </div>
  )
  if (!doc.media && doc.value !== null && typeof doc.value === 'object') {
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
      {!doc.media && (
        <TextBlock
          text={displayValue(doc.value)}
          code={doc.invalid || typeof doc.value !== 'string'}
        />
      )}
    </ReadingViewport>
  )
}

export function DocumentBrowser(props: {
  documents: AuditDocument[]
  notice?: ReactNode
}) {
  const { t } = useTranslation()
  const [selectedId, setSelectedId] = useState(props.documents[0]?.id)
  const [visible, setVisible] = useState(60)
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
    <div className='flex min-h-0 min-w-0 flex-1 overflow-hidden'>
      {props.documents.length > 1 && (
        <nav
          aria-label={t('Content index')}
          className='bg-muted/15 hidden w-48 shrink-0 overflow-y-auto overscroll-contain border-r p-2 lg:block'
        >
          <p className='text-muted-foreground px-2 py-2 text-xs'>
            {t('Content index')} · {props.documents.length}
          </p>
          {props.documents.slice(0, visible).map((doc) => (
            <Button
              key={doc.id}
              variant={doc.id === active.id ? 'secondary' : 'ghost'}
              aria-current={doc.id === active.id ? 'true' : undefined}
              onClick={() => setSelectedId(doc.id)}
              title={documentTitle(doc, t)}
              className='mb-1 h-auto w-full justify-start px-2 py-2 text-left'
            >
              <span className='truncate text-xs'>{documentTitle(doc, t)}</span>
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
        <div
          data-audit-chrome='document'
          className='flex shrink-0 items-center gap-2 border-b px-3 py-2'
        >
          <NativeSelect
            className='min-w-0 flex-1 lg:hidden'
            aria-label={t('Reading section')}
            value={active.id}
            onChange={(event) => setSelectedId(event.target.value)}
          >
            {props.documents.map((doc) => (
              <NativeSelectOption key={doc.id} value={doc.id}>
                {documentTitle(doc, t)}
              </NativeSelectOption>
            ))}
          </NativeSelect>
          <h3
            title={documentTitle(active, t)}
            className='hidden min-w-0 flex-1 truncate text-sm font-medium lg:block'
          >
            {documentTitle(active, t)}
          </h3>
          <CopyButton
            size='sm'
            value={displayValue(active.value)}
            tooltip={t('Copy document')}
          />
        </div>
        <DocumentBody key={active.id} document={active} notice={props.notice} />
      </div>
    </div>
  )
}
