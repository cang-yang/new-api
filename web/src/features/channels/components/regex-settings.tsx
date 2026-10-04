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
import { SlidersHorizontal } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

import { channelFormSchema } from '../lib/channel-form'
import {
  editableRegexRulesSchema,
  materializeRegexRuleSwitches,
} from '../lib/regex-rules'
import { RegexPlayground } from './regex-playground'
import { RegexRulesEditor } from './regex-rules-editor'

type Props = {
  scopeKey: string | number
  value: string
  onChange: (value: string) => void
  presetValue?: string
  presetModels?: string
  disabled?: boolean
}

export function RegexSettings(props: Props) {
  return <ScopedRegexSettings key={props.scopeKey} {...props} />
}

function ScopedRegexSettings(props: Props) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [draft, setDraft] = useState('')
  const [baseline, setBaseline] = useState('')
  const [discard, setDiscard] = useState(false)
  const [tab, setTab] = useState('rules')
  let parsed: unknown
  try {
    parsed = JSON.parse(props.value)
  } catch {
    // Empty and advanced configurations remain editable without conversion.
  }
  const result = editableRegexRulesSchema.safeParse(parsed)
  const rules = result.success
    ? materializeRegexRuleSwitches(result.data).rules
    : []
  const enabled = rules.filter((rule) => !rule.disabled)
  const conflict = open && props.value !== baseline
  const locked = props.disabled || conflict
  const valid =
    channelFormSchema.shape.response_text_filter.safeParse(draft).success
  const close = () => {
    if (draft !== baseline) setDiscard(true)
    else setOpen(false)
  }
  return (
    <section
      aria-label={t('Regex settings')}
      className='bg-muted/20 flex min-w-0 flex-wrap items-center gap-3 rounded-xl border p-4'
    >
      <div className='bg-background text-muted-foreground flex size-10 shrink-0 items-center justify-center rounded-lg border'>
        <SlidersHorizontal className='size-5' aria-hidden='true' />
      </div>
      <div className='flex min-w-0 flex-1 flex-col gap-2'>
        <p className='text-sm font-semibold'>{t('Regex settings')}</p>
        <div className='text-muted-foreground flex flex-wrap gap-2 text-xs'>
          {!props.value.trim() && <span>{t('No regex rules')}</span>}
          {result.success && (
            <>
              <span>
                {t('{{enabled}} of {{total}} rules enabled', {
                  enabled: enabled.length,
                  total: rules.length,
                })}
              </span>
              <Badge variant='outline'>
                {t('Receive')}:{' '}
                {enabled.filter((rule) => rule.stage === 'receive').length}
              </Badge>
              <Badge variant='outline'>
                {t('Send')}:{' '}
                {enabled.filter((rule) => rule.stage === 'send').length}
              </Badge>
            </>
          )}
          {props.value.trim() && !result.success && (
            <Badge variant='outline'>{t('Advanced configuration')}</Badge>
          )}
        </div>
        <p className='text-muted-foreground text-xs'>
          {t('Edit rules, import scripts and test text in a separate editor.')}
        </p>
      </div>
      <Dialog
        open={open}
        onOpenChange={(next) => {
          if (next) setOpen(true)
          else close()
        }}
        trigger={
          <Button
            type='button'
            variant='outline'
            disabled={props.disabled}
            onClick={() => {
              setDraft(props.value)
              setBaseline(props.value)
              setTab('rules')
              setDiscard(false)
            }}
          >
            {t('Edit regex')}
          </Button>
        }
        title={t('Edit regex')}
        description={t(
          'Edit the channel rules. Apply your draft, then save the channel.'
        )}
        contentHeight='100%'
        contentClassName='h-[85dvh] max-h-[calc(100dvh-1rem)] gap-0 overflow-hidden p-0 sm:max-w-[960px] sm:p-0'
        headerClassName='border-b px-5 py-4 pr-12'
        bodyClassName='flex h-full min-h-0 flex-col py-0'
        bodyViewportClassName='mx-0 min-w-0 flex-1 overflow-hidden'
        footerClassName='mx-0 mb-0 flex-row flex-wrap items-center justify-end px-5 py-3 sm:mx-0 sm:mb-0 sm:p-4'
        footer={
          <>
            <p className='text-muted-foreground mr-auto text-xs'>
              {t('Save the channel to make these changes take effect.')}
            </p>
            <Button type='button' variant='outline' onClick={close}>
              {t('Cancel')}
            </Button>
            <Button
              type='button'
              disabled={locked || !valid}
              onClick={() => {
                if (locked || !valid || props.value !== baseline) return
                props.onChange(draft)
                setOpen(false)
              }}
            >
              {t('Apply to channel')}
            </Button>
          </>
        }
      >
        {conflict && (
          <Alert className='shrink-0'>
            <AlertDescription>
              {t(
                'The channel rules changed while this editor was open. Reopen it to load the latest configuration before applying changes.'
              )}
            </AlertDescription>
          </Alert>
        )}
        <Tabs
          value={tab}
          onValueChange={(value) => setTab(String(value))}
          className='min-h-0 min-w-0 flex-1 gap-0'
        >
          <div className='flex shrink-0 flex-wrap items-center justify-between gap-2 border-b px-4 py-3'>
            <TabsList aria-label={t('Regex settings')} className='max-w-full'>
              <TabsTrigger value='rules'>{t('Regex rules')}</TabsTrigger>
              <TabsTrigger value='test'>{t('Regex playground')}</TabsTrigger>
            </TabsList>
            <Button
              type='button'
              variant='ghost'
              size='sm'
              disabled={locked || !draft.trim()}
              onClick={() => setDraft('')}
            >
              {t('Clear')}
            </Button>
          </div>
          <TabsContent
            value='rules'
            keepMounted
            className='relative min-h-0 min-w-0 overflow-x-hidden overflow-y-auto p-4'
          >
            {!valid && (
              <Alert variant='destructive' className='mb-4'>
                <AlertDescription>
                  {t('Enter a valid response text filter')}
                </AlertDescription>
              </Alert>
            )}
            <RegexRulesEditor
              scopeKey={props.scopeKey}
              value={draft}
              onChange={setDraft}
              disabled={locked}
              compact
            />
          </TabsContent>
          <TabsContent
            value='test'
            keepMounted
            className='relative min-h-0 min-w-0 overflow-x-hidden overflow-y-auto p-4'
          >
            <RegexPlayground
              scopeKey={props.scopeKey}
              value={draft}
              presetValue={props.presetValue}
              presetModels={props.presetModels}
              disabled={locked}
            />
          </TabsContent>
        </Tabs>
        <ConfirmDialog
          open={discard}
          onOpenChange={setDiscard}
          title={t('Discard regex changes?')}
          desc={t(
            'Your changes in this editor have not been applied to the channel.'
          )}
          cancelBtnText={t('Keep editing')}
          confirmText={t('Discard changes')}
          destructive
          handleConfirm={() => {
            setDiscard(false)
            setOpen(false)
          }}
        />
      </Dialog>
    </section>
  )
}
