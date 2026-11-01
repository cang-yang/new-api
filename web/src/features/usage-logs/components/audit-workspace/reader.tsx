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
import { ArrowLeft, ChevronRight } from 'lucide-react'
import { useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Button } from '@/components/ui/button'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { cn } from '@/lib/utils'

import {
  displayValue,
  objectValue,
  valueAtPath,
} from '../../lib/audit-workspace'

export function TextBlock(props: {
  text: string
  code?: boolean
  compact?: boolean
}) {
  const { t } = useTranslation()
  const step = props.compact ? 4096 : 32768
  const [limit, setLimit] = useState(step)
  return (
    <div className='min-w-0'>
      <pre
        className={
          props.code
            ? 'font-mono text-xs leading-6 font-normal [overflow-wrap:anywhere] whitespace-pre-wrap'
            : 'font-sans text-sm leading-6 font-normal [overflow-wrap:anywhere] whitespace-pre-wrap'
        }
      >
        {props.text.slice(0, limit) || '""'}
      </pre>
      {props.text.length > limit && (
        <Button
          className='mt-2'
          variant='outline'
          size='sm'
          onClick={() => setLimit(limit + step)}
        >
          {t('Load more')}
        </Button>
      )}
    </div>
  )
}

export function ReadingViewport(props: { children: ReactNode }) {
  const { t } = useTranslation()
  return (
    <div
      data-audit-scroll='content'
      role='region'
      aria-label={t('Reading content')}
      tabIndex={0}
      className='min-h-0 min-w-0 flex-1 overflow-x-hidden overflow-y-auto overscroll-contain p-4 outline-offset-[-2px] sm:px-8 sm:py-5 [@media(max-height:500px)]:py-2'
    >
      <div className='mx-auto max-w-3xl min-w-0'>{props.children}</div>
    </div>
  )
}

export function ValueExplorer(props: { value: unknown; notice?: ReactNode }) {
  const { t } = useTranslation()
  const [path, setPath] = useState<string[]>([])
  const [page, setPage] = useState(0)
  const locationRef = useRef<HTMLSelectElement>(null)
  const value = valueAtPath(props.value, path)
  const structured = value !== null && typeof value === 'object'
  const entries = structured ? Object.entries(value) : []
  const hasNavigation =
    path.length > 0 ||
    entries.some(([, child]) => child !== null && typeof child === 'object')
  const pageSize = 40
  const pages = Math.max(1, Math.ceil(entries.length / pageSize))
  const navigate = (next: string[]) => {
    setPath(next)
    setPage(0)
    locationRef.current?.focus()
  }

  return (
    <div className='flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden'>
      {hasNavigation && (
        <div
          data-audit-chrome='path'
          className='bg-muted/20 flex shrink-0 items-center gap-2 border-b px-3 py-2'
        >
          <Button
            variant='ghost'
            size='icon-sm'
            aria-label={t('Back')}
            disabled={!path.length}
            onClick={() => navigate(path.slice(0, -1))}
          >
            <ArrowLeft className='size-4' />
          </Button>
          <NativeSelect
            ref={locationRef}
            className='min-w-0 flex-1'
            aria-label={t('Data location')}
            value={String(path.length)}
            onChange={(event) =>
              navigate(path.slice(0, Number(event.target.value)))
            }
          >
            <NativeSelectOption value='0'>{t('Data root')}</NativeSelectOption>
            {path.map((_, index) => (
              <NativeSelectOption
                key={JSON.stringify(path.slice(0, index + 1))}
                value={String(index + 1)}
              >
                {path
                  .slice(0, index + 1)
                  .map((part) => JSON.stringify(part))
                  .join(' / ')}
              </NativeSelectOption>
            ))}
          </NativeSelect>
          <CopyButton
            value={displayValue(value)}
            size='sm'
            tooltip={t('Copy current content')}
          />
        </div>
      )}
      <ReadingViewport key={`${JSON.stringify(path)}:${page}`}>
        {props.notice}
        {!structured && (
          <TextBlock
            text={displayValue(value)}
            code={typeof value !== 'string'}
          />
        )}
        {structured && !entries.length && (
          <code className='text-sm'>{Array.isArray(value) ? '[]' : '{}'}</code>
        )}
        {entries
          .slice(page * pageSize, (page + 1) * pageSize)
          .map(([key, child]) => {
            const nested = child !== null && typeof child === 'object'
            const label = Array.isArray(value)
              ? `#${Number(key) + 1}`
              : key || '""'
            const record = objectValue(child)
            const title =
              record &&
              [record.title, record.name, record.id].find(
                (item) => typeof item === 'string'
              )
            if (nested) {
              return (
                <Button
                  key={key}
                  variant='ghost'
                  className='h-auto w-full justify-start gap-3 rounded-none border-b px-0 py-4 text-left whitespace-normal'
                  onClick={() => navigate([...path, key])}
                  aria-label={`${t('Open')} ${label}`}
                >
                  <span className='min-w-0 flex-1'>
                    <span className='block truncate font-medium'>{label}</span>
                    {typeof title === 'string' && (
                      <span className='text-muted-foreground mt-1 block truncate text-xs'>
                        {title.slice(0, 120)}
                      </span>
                    )}
                  </span>
                  <span className='text-muted-foreground shrink-0 text-xs'>
                    {Array.isArray(child) ? t('Array') : t('Object')} ·{' '}
                    {Object.keys(child).length}
                  </span>
                  <ChevronRight
                    className='size-4 shrink-0'
                    aria-hidden='true'
                  />
                </Button>
              )
            }
            const prose =
              typeof child === 'string' &&
              (child.length > 160 || /[\r\n]/.test(child))
            return (
              <div
                key={key}
                data-field-layout={prose ? 'prose' : 'compact'}
                className={cn(
                  'grid min-w-0 border-b last:border-b-0',
                  prose
                    ? 'gap-2 py-3 first:pt-0'
                    : 'grid-cols-[minmax(0,8rem)_minmax(0,1fr)] items-start gap-3 py-2.5'
                )}
              >
                <div className='text-muted-foreground text-xs leading-6 [overflow-wrap:anywhere]'>
                  {label}
                </div>
                <TextBlock
                  compact
                  text={
                    typeof child === 'string' &&
                    /^(?:null|true|false|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)$/.test(
                      child
                    )
                      ? JSON.stringify(child)
                      : displayValue(child)
                  }
                  code={typeof child !== 'string'}
                />
              </div>
            )
          })}
      </ReadingViewport>
      {pages > 1 && (
        <div className='flex shrink-0 items-center justify-between gap-2 border-t px-3 py-2'>
          <Button
            variant='outline'
            size='sm'
            disabled={page === 0}
            onClick={() => setPage(page - 1)}
          >
            {t('Previous')}
          </Button>
          <span className='text-muted-foreground text-xs'>
            {page + 1} / {pages}
          </span>
          <Button
            variant='outline'
            size='sm'
            disabled={page + 1 === pages}
            onClick={() => setPage(page + 1)}
          >
            {t('Next')}
          </Button>
        </div>
      )}
    </div>
  )
}
