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

import { Button } from '@/components/ui/button'

// Strings are already decoded. Never recursively parse/unescape model text.
export function AuditText(props: { text: string; code?: boolean }) {
  const { t } = useTranslation()
  const [limit, setLimit] = useState(128 * 1024)
  return (
    <div className='min-w-0'>
      <pre
        className={
          props.code
            ? 'font-mono text-xs leading-6 [overflow-wrap:anywhere] whitespace-pre-wrap'
            : 'max-w-[80ch] font-sans text-sm leading-7 [overflow-wrap:anywhere] whitespace-pre-wrap'
        }
      >
        {props.text.slice(0, limit) || '""'}
      </pre>
      {props.text.length > limit && (
        <Button
          variant='outline'
          size='sm'
          className='mt-3'
          onClick={() => setLimit((count) => count + 128 * 1024)}
        >
          {t('Load more')}
        </Button>
      )}
    </div>
  )
}

function StructuredValue(props: {
  value: unknown
  depth: number
  budget: number
}) {
  const { t } = useTranslation()
  const [visible, setVisible] = useState(50)
  const [expanded, setExpanded] = useState(false)
  const value = props.value
  if (value === null || typeof value !== 'object') {
    if (typeof value !== 'string') {
      return (
        <code className='bg-muted rounded px-1 py-0.5 text-xs'>
          {JSON.stringify(value) ?? 'null'}
        </code>
      )
    }
    return (
      <AuditText
        text={
          /^(?:true|false|null|-?\d+(?:\.\d+)?(?:e[+-]?\d+)?|\{\}|\[\])$/i.test(
            value
          )
            ? JSON.stringify(value)
            : value
        }
      />
    )
  }
  const entries = Object.entries(value)
  const array = Array.isArray(value)
  if (!entries.length) {
    return <span className='font-mono text-sm'>{array ? '[]' : '{}'}</span>
  }
  // Bound breadth and depth; deferred subtrees remain independently accessible.
  if (!expanded && (props.depth >= 5 || props.budget < 2)) {
    return (
      <Button variant='outline' size='sm' onClick={() => setExpanded(true)}>
        {t('View nested content')}
      </Button>
    )
  }
  const depth = expanded ? 0 : props.depth
  const budget = expanded ? 400 : props.budget
  const childBudget = Math.max(
    1,
    Math.floor((budget - 1) / Math.min(visible, entries.length))
  )
  return (
    <div className='min-w-0 space-y-2'>
      {entries.slice(0, visible).map(([key, nested]) => {
        const compact =
          !array &&
          (nested === null || typeof nested !== 'object') &&
          (typeof nested !== 'string' ||
            (nested.length < 160 && !nested.includes('\n')))
        return (
          <section
            key={key}
            className={
              compact
                ? 'grid min-w-0 grid-cols-1 gap-x-4 sm:grid-cols-[minmax(7rem,10rem)_minmax(0,1fr)]'
                : 'min-w-0 space-y-2 border-t pt-3 first:border-t-0 first:pt-0'
            }
          >
            <h5 className='text-muted-foreground min-w-0 text-xs leading-7 font-medium [overflow-wrap:anywhere]'>
              {array ? `#${Number(key) + 1}` : key}
            </h5>
            <div
              className={
                !compact && nested !== null && typeof nested === 'object'
                  ? 'min-w-0 border-l pl-3'
                  : 'min-w-0'
              }
            >
              <StructuredValue
                value={nested}
                depth={depth + 1}
                budget={childBudget}
              />
            </div>
          </section>
        )
      })}
      {entries.length > visible && (
        <Button
          variant='outline'
          size='sm'
          onClick={() => setVisible((count) => count + 50)}
        >
          {t('Load more')}
        </Button>
      )}
    </div>
  )
}

export function BodyAuditJsonContent(props: { value: unknown }) {
  return <StructuredValue value={props.value} depth={0} budget={400} />
}
