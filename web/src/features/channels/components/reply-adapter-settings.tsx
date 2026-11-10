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
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'

import { presetRecord as record } from '../lib/preset-script-preparation'
import {
  migrateReplyAdapter,
  replySampleFields,
  validReplyAdapter,
  type ReplyContainer,
} from '../lib/reply-adapter'

export function ReplyAdapterSettings(props: {
  config: Record<string, unknown>
  onChange: (config: Record<string, unknown>) => void
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const id = useId()
  const [sample, setSample] = useState('')
  const [reset, setReset] = useState(false)
  const [rowKeys, setRowKeys] = useState(() =>
    Array.from({ length: 32 }, () => crypto.randomUUID())
  )
  const adapter = record(props.config.reply_adapter)
  const containers = Array.isArray(adapter.containers)
    ? (adapter.containers as ReplyContainer[])
    : []
  const legacy = record(props.config.tool_protocol).enabled === true
  const migration = migrateReplyAdapter(props.config)
  const fields = replySampleFields(sample)
  const save = (items: ReplyContainer[]) => {
    const next = {
      ...props.config,
      reply_adapter: {
        ...adapter,
        version: 2,
        containers: items,
        ...(items.length === 0 ? { structured_output: false } : {}),
      },
    }
    props.onChange(next)
  }
  const update = (index: number, value: ReplyContainer) =>
    save(containers.map((item, i) => (i === index ? value : item)))
  const backup = () => {
    const url = URL.createObjectURL(
      new Blob([JSON.stringify(props.config, null, 2)], {
        type: 'application/json',
      })
    )
    const link = document.createElement('a')
    link.href = url
    link.download = 'preset-before-reply-migration.json'
    link.click()
    URL.revokeObjectURL(url)
  }
  return (
    <FieldSet
      disabled={props.disabled}
      className='min-w-0 rounded-lg border p-4'
    >
      <FieldLegend variant='label'>{t('Reply source')}</FieldLegend>
      <FieldDescription>
        {t(
          'Most presets reply with text and need no setup here. Only add a source when the preset returns the reply inside its own tool arguments. Client tools are never executed here.'
        )}
      </FieldDescription>
      {!validReplyAdapter(props.config) && (
        <p role='alert' className='text-destructive text-sm'>
          {t(
            'Check reply sources: use unique tool names, a valid reply field or formatter, and the correct reply format.'
          )}
        </p>
      )}
      {legacy && (
        <Alert>
          <AlertTitle>{t('Old tool settings need review')}</AlertTitle>
          <AlertDescription>
            {t(
              'Business-tool coordination has been retired. Keep a backup, then declare only the tool that carries the reply. Requests using unsafe old settings will stop before contacting the provider.'
            )}
          </AlertDescription>
          <div className='flex flex-wrap gap-2 pt-2'>
            <Button type='button' variant='outline' onClick={backup}>
              {t('Download original settings')}
            </Button>
            {migration.status === 'ready' && (
              <Button
                type='button'
                disabled={props.disabled}
                onClick={() => {
                  const next: Record<string, unknown> = {
                    ...props.config,
                    reply_adapter: migration.adapter,
                  }
                  delete next.tool_protocol
                  props.onChange(next)
                }}
              >
                {t('Migrate reply source')}
              </Button>
            )}
            <Button
              type='button'
              variant='outline'
              disabled={props.disabled}
              onClick={() => setReset(true)}
            >
              {t('Remove old coordination')}
            </Button>
          </div>
        </Alert>
      )}
      <FieldGroup className='gap-4'>
        {containers.map((item, index) => (
          <FieldGroup
            key={rowKeys[index]}
            className='min-w-0 rounded-lg border p-3'
          >
            <Field>
              <FieldLabel htmlFor={`${id}name${index}`}>
                {t('Reply tool name')}
              </FieldLabel>
              <Input
                id={`${id}name${index}`}
                value={item.name}
                maxLength={64}
                disabled={props.disabled}
                onChange={(e) =>
                  update(index, { ...item, name: e.target.value })
                }
              />
            </Field>
            <Field orientation='horizontal'>
              <FieldLabel htmlFor={`${id}formatter${index}`}>
                {t('Use the preset reply formatter')}
              </FieldLabel>
              <Switch
                id={`${id}formatter${index}`}
                checked={item.formatter === true}
                disabled={props.disabled}
                onCheckedChange={(value) =>
                  update(
                    index,
                    value
                      ? {
                          name: item.name,
                          output_kind: item.output_kind,
                          formatter: true,
                        }
                      : {
                          name: item.name,
                          output_kind: item.output_kind,
                          argument_pointer: '',
                        }
                  )
                }
              />
            </Field>
            {!item.formatter && (
              <Field>
                <FieldLabel htmlFor={`${id}pointer${index}`}>
                  {t('Reply field')}
                </FieldLabel>
                <Input
                  id={`${id}pointer${index}`}
                  value={item.argument_pointer ?? ''}
                  disabled={props.disabled}
                  placeholder='/content'
                  onChange={(e) =>
                    update(index, { ...item, argument_pointer: e.target.value })
                  }
                />
                <FieldDescription>
                  {t(
                    'Choose a field from the sample below, or enter a JSON Pointer. Empty means the complete argument object.'
                  )}
                </FieldDescription>
              </Field>
            )}
            <Field>
              <FieldLabel htmlFor={`${id}kind${index}`}>
                {t('Reply format')}
              </FieldLabel>
              <Select
                value={item.output_kind}
                disabled={props.disabled}
                onValueChange={(value) => {
                  if (value === 'text' || value === 'json_object') {
                    update(index, { ...item, output_kind: value })
                  }
                }}
              >
                <SelectTrigger id={`${id}kind${index}`}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value='text'>{t('Text')}</SelectItem>
                    <SelectItem value='json_object'>
                      {t('JSON object')}
                    </SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
            </Field>
            {!item.formatter && fields.length > 0 && (
              <Field>
                <FieldLabel htmlFor={`${id}samplefield${index}`}>
                  {t('Choose from sample')}
                </FieldLabel>
                <Select
                  disabled={props.disabled}
                  value={item.argument_pointer || 'root-object'}
                  onValueChange={(value) => {
                    const field = fields.find(
                      (field) => (field.pointer || 'root-object') === value
                    )
                    if (field) {
                      update(index, {
                        ...item,
                        argument_pointer: field.pointer,
                        output_kind: field.kind,
                      })
                    }
                  }}
                >
                  <SelectTrigger
                    id={`${id}samplefield${index}`}
                    className='w-full min-w-0'
                  >
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      {fields.map((field) => (
                        <SelectItem
                          key={field.pointer}
                          value={field.pointer || 'root-object'}
                        >
                          <span className='truncate'>
                            {field.pointer || t('Complete object')} ·{' '}
                            {field.kind} · {field.preview}
                          </span>
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </Field>
            )}
            <Button
              type='button'
              variant='outline'
              disabled={props.disabled}
              onClick={() => {
                setRowKeys((keys) => [
                  ...keys.filter((_, i) => i !== index),
                  crypto.randomUUID(),
                ])
                save(containers.filter((_, i) => i !== index))
              }}
            >
              {t('Remove source')}
            </Button>
          </FieldGroup>
        ))}
        <Button
          type='button'
          variant='outline'
          disabled={props.disabled || containers.length >= 32}
          onClick={() =>
            save([
              ...containers,
              { name: '', argument_pointer: '/content', output_kind: 'text' },
            ])
          }
        >
          {t('Add reply source')}
        </Button>
        {containers.length > 0 && (
          <Field orientation='horizontal' data-disabled={props.disabled}>
            <FieldContent>
              <FieldLabel htmlFor={`${id}structured`}>
                {t("Convert preset replies to the client's JSON format")}
              </FieldLabel>
              <FieldDescription id={`${id}structured-help`}>
                {t(
                  "Enable only after testing this preset with JSON requests. The formatter, output script and text cleanup still run; the final JSON is checked against the client's requirements. Invalid replies are not repaired or retried."
                )}
              </FieldDescription>
            </FieldContent>
            <Switch
              id={`${id}structured`}
              aria-describedby={`${id}structured-help`}
              checked={adapter.structured_output === true}
              disabled={props.disabled}
              onCheckedChange={(value) =>
                props.onChange({
                  ...props.config,
                  reply_adapter: {
                    ...adapter,
                    version: 2,
                    containers,
                    structured_output: value,
                  },
                })
              }
            />
          </Field>
        )}
        {containers.length > 0 && (
          <Field>
            <FieldLabel htmlFor={`${id}sample`}>
              {t('Tool argument sample')}
            </FieldLabel>
            <Textarea
              id={`${id}sample`}
              value={sample}
              disabled={props.disabled}
              className='min-h-28 w-full min-w-0'
              maxLength={262144}
              onChange={(e) => setSample(e.target.value)}
            />
            <FieldDescription>
              {t(
                'Paste a JSON object to choose the reply field. This does not execute scripts or call a model.'
              )}
            </FieldDescription>
            {sample && !fields.length && (
              <p role='alert'>{t('Enter a valid JSON object.')}</p>
            )}
          </Field>
        )}
      </FieldGroup>
      <ConfirmDialog
        open={reset}
        onOpenChange={setReset}
        title={t('Remove old coordination')}
        desc={t(
          'Only old tool coordination is removed. The preset and scripts are kept. Download the original settings first if you need a rollback.'
        )}
        confirmText={t('Confirm')}
        handleConfirm={() => {
          const next = { ...props.config }
          delete next.tool_protocol
          props.onChange(next)
          setReset(false)
        }}
      />
    </FieldSet>
  )
}
