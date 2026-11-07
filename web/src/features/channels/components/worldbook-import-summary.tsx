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
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

import type { Worldbook, WorldbookEntry } from '../lib/worldbook'

export function WorldbookImportSummary(props: {
  book: Worldbook
  onReview?: () => void
}) {
  const { t } = useTranslation()
  const invalid = props.book.entries.filter(
    (entry) => !!entry.issues?.length
  ).length
  const normalized = props.book.entries.filter(
    (entry) => !!entry.normalized_rules?.length
  ).length
  return (
    <div
      className='bg-muted/30 space-y-1 rounded-lg border p-3 text-sm'
      role='status'
    >
      <p>
        {t('Imported {{count}} entries.', { count: props.book.entries.length })}{' '}
        {!!invalid && (
          <span className='text-destructive'>
            {t('{{count}} need attention.', { count: invalid })}
          </span>
        )}
      </p>
      {!!invalid && props.onReview && (
        <Button
          type='button'
          variant='link'
          className='h-auto p-0'
          onClick={props.onReview}
        >
          {t('Review entries needing attention')}
        </Button>
      )}
      {!!normalized && (
        <p className='text-muted-foreground'>
          {t(
            'Some advanced Tavern options are not used. Keyword matching remains available.'
          )}
        </p>
      )}
    </div>
  )
}

export function WorldbookEntryNotes(props: { entry: WorldbookEntry }) {
  const { t } = useTranslation()
  const rules = props.entry.normalized_rules ?? []
  if (!rules.length) return null
  const fields = rules.map((rule) => rule.field)
  const placement = fields.some((field) => /position|depth|role/.test(field))
  const probability = fields.some((field) => /probability/i.test(field))
  return (
    <details className='bg-muted/30 rounded-lg border p-3 text-sm'>
      <summary className='cursor-pointer font-medium'>
        {t('How this entry is handled')}
      </summary>
      <div className='mt-2 space-y-2 text-sm'>
        {placement && (
          <p>
            {t(
              'The original Tavern position is not used. Choose the insertion position below.'
            )}
          </p>
        )}
        {probability && (
          <p>
            {t(
              'Random activation is not used. This entry is eligible whenever its keywords match.'
            )}
          </p>
        )}
        <p className='text-muted-foreground'>
          {t(
            'Other advanced Tavern options do not affect keyword matching here.'
          )}
        </p>
        <details className='text-muted-foreground text-xs'>
          <summary className='cursor-pointer'>{t('Technical details')}</summary>
          <ul className='mt-2 space-y-1 break-words'>
            {rules.map((rule) => (
              <li key={`${rule.field}:${rule.action}:${rule.detail}`}>
                {rule.field}
                {rule.detail ? ` · ${rule.detail}` : ''}
              </li>
            ))}
          </ul>
        </details>
      </div>
    </details>
  )
}
