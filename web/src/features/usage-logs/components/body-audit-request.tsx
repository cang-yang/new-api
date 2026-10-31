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
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

import { auditRecord, auditRequestSections } from '../lib/body-audit-reading'
import { AuditText, BodyAuditJsonContent } from './body-audit-json-content'

export function BodyAuditRequestContent(props: { value: unknown }) {
  const { t } = useTranslation()
  const [selected, setSection] = useState('')
  const [visible, setVisible] = useState(30)
  const data = useMemo(() => auditRequestSections(props.value), [props.value])
  if (!data) return <BodyAuditJsonContent value={props.value} />
  let section = selected
  if (!section) {
    section = 'parameters'
    if (data.messages.length || Object.keys(data.context).length) {
      section = 'messages'
    } else if (Object.keys(data.tools).length) {
      section = 'tools'
    }
  }
  const roleLabels: Record<string, string> = {
    system: t('System message'),
    developer: t('Developer message'),
    user: t('User message'),
    assistant: t('Assistant message'),
    model: t('Assistant message'),
    tool: t('Tool message'),
    function: t('Tool message'),
    message: t('Message'),
  }
  return (
    <div className='min-w-0 space-y-4'>
      <NativeSelect
        aria-label={t('Reading section')}
        value={section}
        onChange={(event) => setSection(event.target.value)}
        className='max-w-full'
      >
        <NativeSelectOption value='messages'>
          {t('Messages and context')}
        </NativeSelectOption>
        <NativeSelectOption value='tools'>
          {t('Tool definitions')}
        </NativeSelectOption>
        <NativeSelectOption value='parameters'>
          {t('Request parameters')}
        </NativeSelectOption>
      </NativeSelect>
      {section === 'messages' && (
        <div className='min-w-0 space-y-5'>
          {Object.keys(data.context).length > 0 && (
            <section className='min-w-0 border-b pb-4'>
              <h4 className='mb-3 text-sm font-semibold'>
                {t('System message')}
              </h4>
              <BodyAuditJsonContent value={data.context} />
            </section>
          )}
          {data.messages.slice(0, visible).map((message, index) => {
            const record = auditRecord(message.value)
            const contentKey =
              record && ['content', 'parts'].find((key) => key in record)
            const content =
              contentKey && record ? record[contentKey] : message.value
            const metadata =
              record && contentKey
                ? Object.fromEntries(
                    Object.entries(record).filter(
                      ([key]) =>
                        key !== contentKey &&
                        (key !== 'role' || typeof record.role !== 'string')
                    )
                  )
                : null
            return (
              <section
                key={message.key}
                className='min-w-0 border-b pb-5 last:border-b-0'
              >
                <div className='mb-3 flex items-center justify-between gap-3'>
                  <h4 className='min-w-0 text-sm font-semibold [overflow-wrap:anywhere]'>
                    {Object.hasOwn(roleLabels, message.role)
                      ? roleLabels[message.role]
                      : message.role}{' '}
                    · {index + 1}
                  </h4>
                  <CopyButton
                    value={
                      typeof content === 'string'
                        ? content
                        : JSON.stringify(message.value, null, 2)
                    }
                    size='sm'
                    tooltip={t('Copy message')}
                  />
                </div>
                {typeof content === 'string' ? (
                  <AuditText text={content} />
                ) : (
                  <BodyAuditJsonContent value={content} />
                )}
                {metadata && Object.keys(metadata).length > 0 && (
                  <div className='mt-3 border-t pt-3'>
                    <BodyAuditJsonContent value={metadata} />
                  </div>
                )}
              </section>
            )
          })}
          {data.messages.length > visible && (
            <Button
              variant='outline'
              size='sm'
              onClick={() => setVisible((count) => count + 30)}
            >
              {t('Load more')}
            </Button>
          )}
          {!data.messages.length && !Object.keys(data.context).length && (
            <p className='text-muted-foreground text-sm'>
              {t('No readable text was extracted')}
            </p>
          )}
        </div>
      )}
      {section === 'tools' && (
        <BodyAuditJsonContent key='tools' value={data.tools} />
      )}
      {section === 'parameters' && (
        <BodyAuditJsonContent key='parameters' value={data.parameters} />
      )}
    </div>
  )
}
