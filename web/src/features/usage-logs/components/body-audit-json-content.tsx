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
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Button } from '@/components/ui/button'

import { auditReadingFields } from '../lib/body-audit-reading'

const previewLimit = 128 * 1024

function ReadingField(props: { path: string; text: string }) {
  const { t } = useTranslation()
  const [expanded, setExpanded] = useState(false)
  const long = props.text.length > 4000
  return (
    <section className='min-w-0'>
      <div className='mb-1 flex items-start justify-between gap-2'>
        <h5 className='text-muted-foreground min-w-0 font-mono text-xs [overflow-wrap:anywhere]'>
          {props.path}
        </h5>
        <CopyButton
          value={props.text}
          size='sm'
          tooltip={t('Copy readable field')}
        />
      </div>
      {long && !expanded && (
        <pre className='font-sans text-sm leading-7 [overflow-wrap:anywhere] whitespace-pre-wrap'>
          {props.text.slice(0, 320)}…
        </pre>
      )}
      {long ? (
        <details onToggle={(event) => setExpanded(event.currentTarget.open)}>
          <summary className='cursor-pointer text-xs'>
            {t('Expand long text')}
          </summary>
          {expanded && (
            <pre className='mt-2 font-sans text-sm leading-7 [overflow-wrap:anywhere] whitespace-pre-wrap'>
              {props.text.slice(0, previewLimit)}
            </pre>
          )}
        </details>
      ) : (
        <pre className='font-sans text-sm leading-7 [overflow-wrap:anywhere] whitespace-pre-wrap'>
          {props.text || '""'}
        </pre>
      )}
      {expanded && props.text.length > previewLimit && (
        <p className='text-muted-foreground text-xs'>
          {t(
            'Preview shortened for performance. Copy original retains the entire captured body.'
          )}
        </p>
      )}
    </section>
  )
}

// Shared by request context and tool arguments. React renders all text literally;
// HTML, Markdown directives and tool definitions are never executed here.
export function BodyAuditJsonContent(props: { value: unknown }) {
  const { t } = useTranslation()
  const fields = useMemo(() => auditReadingFields(props.value), [props.value])
  const [showJson, setShowJson] = useState(false)
  const [visibleFields, setVisibleFields] = useState(256)
  return (
    <div className='flex min-w-0 flex-col gap-4'>
      {fields.slice(0, visibleFields).map((field) => (
        <ReadingField key={field.path} {...field} />
      ))}
      {fields.length > visibleFields && (
        <Button
          variant='outline'
          size='sm'
          onClick={() => setVisibleFields((count) => count + 256)}
        >
          {t('Expand')} · {visibleFields} / {fields.length}
        </Button>
      )}
      <details onToggle={(event) => setShowJson(event.currentTarget.open)}>
        <summary className='cursor-pointer text-xs font-medium'>
          {t('Formatted JSON')}
        </summary>
        {showJson && (
          <pre className='mt-2 font-mono text-xs leading-6 [overflow-wrap:anywhere] whitespace-pre-wrap'>
            {JSON.stringify(props.value, null, 2).slice(0, previewLimit)}
          </pre>
        )}
      </details>
    </div>
  )
}
