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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'

import {
  worldbookConfirmEntry,
  worldbookEntryBlocked,
  worldbookFieldLabel,
  worldbookIssueLabel,
  worldbookNeedsReview,
  worldbookRiskFields,
  type Worldbook,
  type WorldbookEntry,
} from '../lib/worldbook'
import { worldbookPositionLabels } from '../lib/worldbook-placement'
import { WorldbookImportSummary } from './worldbook-import-summary'

export function WorldbookEntryOverview(props: {
  book: Worldbook
  active: boolean
  disabled?: boolean
  onChange: (entry: WorldbookEntry) => void
  onEnableAll?: (entries: WorldbookEntry[]) => void
  onEdit: (id: string) => void
}) {
  const { t } = useTranslation()
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState('all')
  const [pending, setPending] = useState<WorldbookEntry | null>(null)
  const [blocked, setBlocked] = useState<WorldbookEntry | null>(null)
  const enabled = props.book.entries.filter((entry) => entry.enabled).length
  const attention = props.book.entries.filter(worldbookNeedsReview).length
  const entries = props.book.entries.filter((entry) => {
    if (filter === 'enabled' && !entry.enabled) return false
    if (filter === 'disabled' && entry.enabled) return false
    if (filter === 'attention' && !worldbookNeedsReview(entry)) return false
    const text = [entry.name, ...entry.keys].join('\n').toLowerCase()
    return text.includes(query.trim().toLowerCase())
  })
  const columns: StaticDataTableColumn<WorldbookEntry>[] = [
    {
      id: 'name',
      header: t('Entry name'),
      className: 'w-[40%]',
      cell: (entry) => (
        <div className='min-w-0 space-y-1'>
          <Button
            type='button'
            variant='link'
            className='h-auto max-w-full justify-start p-0 text-left [overflow-wrap:anywhere] whitespace-normal'
            onClick={() => props.onEdit(entry.id)}
            aria-label={t('Edit {{name}}', { name: entry.name || entry.id })}
          >
            {entry.name || entry.id}
          </Button>
          {entry.issues?.length ? (
            <p className='text-destructive text-xs' data-table-text='secondary'>
              {t('Needs attention')}
            </p>
          ) : (
            worldbookRiskFields(entry).length > 0 && (
              <p
                className='text-xs text-amber-600 dark:text-amber-500'
                data-table-text='secondary'
              >
                {t('Needs review')}
              </p>
            )
          )}
        </div>
      ),
    },
    {
      id: 'status',
      header: t('Status'),
      className: 'w-32',
      cell: (entry) => (
        <div className='flex items-center gap-2'>
          <Switch
            aria-label={t('Enable {{name}}', { name: entry.name || entry.id })}
            checked={entry.enabled}
            disabled={props.disabled}
            onCheckedChange={(value) => {
              if (value) {
                if (worldbookEntryBlocked(entry)) {
                  setBlocked(entry)
                  return
                }
                if (worldbookRiskFields(entry).length) {
                  setPending(entry)
                  return
                }
              }
              props.onChange({ ...entry, enabled: value })
            }}
          />
          <span className='text-xs' data-table-text='secondary'>
            {entry.enabled ? t('Enabled') : t('Disabled')}
          </span>
        </div>
      ),
    },
    {
      id: 'trigger',
      header: t('When to include'),
      className: 'hidden sm:table-cell',
      cellClassName: 'hidden sm:table-cell',
      cell: (entry) => (
        <span className='whitespace-normal'>
          {entry.constant ? t('Always include') : t('When keywords match')}
        </span>
      ),
    },
    {
      id: 'placement',
      header: t('Insertion position'),
      cell: (entry) => (
        <span
          className='block text-xs whitespace-normal'
          data-table-text='secondary'
        >
          {t(worldbookPositionLabels[entry.injection_mode || 'follow_global'])}
          {entry.injection_mode === 'at_depth'
            ? ` · ${entry.injection_depth ?? 0}`
            : ''}
        </span>
      ),
    },
  ]
  return (
    <section
      aria-label={t('Entry overview')}
      className='flex min-h-0 flex-1 flex-col gap-3'
    >
      <WorldbookImportSummary
        book={props.book}
        onReview={() => setFilter('attention')}
        onEnableAll={
          props.onEnableAll
            ? () => {
                const review = props.book.entries.filter(
                  (entry) =>
                    !worldbookEntryBlocked(entry) &&
                    worldbookRiskFields(entry).length > 0
                )
                if (review.length) props.onEnableAll?.(review)
              }
            : undefined
        }
      />
      <div className='flex shrink-0 flex-wrap items-center gap-2'>
        <p className='mr-auto text-sm font-medium'>
          {t('{{enabled}} enabled · {{disabled}} disabled', {
            enabled,
            disabled: props.book.entries.length - enabled,
          })}
        </p>
        <p className='text-muted-foreground text-xs'>
          {t('Enabled entries still need to match their trigger conditions.')}
        </p>
      </div>
      {!props.active && (
        <p role='status' className='bg-muted rounded-md px-3 py-2 text-sm'>
          {t(
            'Worldbooks or this book are turned off. Entry switches are preserved but no entries will run.'
          )}
        </p>
      )}
      <div className='flex shrink-0 flex-wrap gap-2'>
        <Input
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          aria-label={t('Search entries...')}
          placeholder={t('Search entries...')}
          className='min-w-0 flex-1 basis-40'
        />
        <Select
          value={filter}
          onValueChange={(value) => {
            if (value) setFilter(value)
          }}
        >
          <SelectTrigger aria-label={t('Filter entries')} className='w-36'>
            <SelectValue />
          </SelectTrigger>
          <SelectContent alignItemWithTrigger={false}>
            <SelectItem value='all'>{t('All entries')}</SelectItem>
            <SelectItem value='enabled'>{t('Enabled')}</SelectItem>
            <SelectItem value='disabled'>{t('Disabled')}</SelectItem>
            <SelectItem value='attention'>
              {t('Needs attention ({{count}})', { count: attention })}
            </SelectItem>
          </SelectContent>
        </Select>
      </div>
      <div
        className='min-h-0 flex-1 overflow-y-auto rounded-md border'
        tabIndex={0}
        aria-label={t('Worldbook entry list')}
      >
        <StaticDataTable
          columns={columns}
          data={entries}
          getRowKey={(entry) => entry.id}
          className='overflow-visible rounded-none border-0'
          tableClassName='table-fixed [&_thead]:sticky [&_thead]:top-0 [&_thead]:z-10 [&_thead]:bg-background'
          tableProps={{ withContainer: false }}
          emptyContent={t('No entries found')}
        />
      </div>
      <ConfirmDialog
        open={pending !== null}
        onOpenChange={(next) => {
          if (!next) setPending(null)
        }}
        title={t('Enable {{name}}?', {
          name: pending?.name || pending?.id || '',
        })}
        desc={t(
          'This entry uses options the gateway cannot reproduce: {{fields}}. Enabling it keeps only the supported parts and may widen when it triggers. The original import is kept.',
          {
            fields: pending
              ? worldbookRiskFields(pending)
                  .map((field) => t(worldbookFieldLabel(field)))
                  .join('、')
              : '',
          }
        )}
        confirmText={t('Confirm and enable')}
        handleConfirm={() => {
          if (pending) props.onChange(worldbookConfirmEntry(pending))
          setPending(null)
        }}
      />
      <ConfirmDialog
        open={blocked !== null}
        onOpenChange={(next) => {
          if (!next) setBlocked(null)
        }}
        title={t('{{name}} cannot be enabled yet', {
          name: blocked?.name || blocked?.id || '',
        })}
        desc={t(
          'This entry is missing required data, so it can never run: {{issues}}. Fix the entry before enabling it. It stays disabled and no trigger fields are discarded.',
          {
            issues:
              blocked?.issues
                ?.map((issue) => t(worldbookIssueLabel(issue)))
                .join(' · ') ?? '',
          }
        )}
        confirmText={t('Close')}
        handleConfirm={() => setBlocked(null)}
      />
    </section>
  )
}
