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
import { useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { cn } from '@/lib/utils'

import {
  editPresetConfigEntry,
  migratePresetPatches,
  parsePresetEditor,
  reorderPresetEntries,
} from '../lib/sillytavern-editor'
import { PresetEntryFields } from './preset-entry-fields'
import { PresetEntryList } from './preset-entry-list'
import { PresetRegexSettings } from './preset-regex-settings'
import { SillyTavernMacroEditor } from './sillytavern-macro-editor'

type Props = {
  value: string
  onChange: (value: string) => void
  disabled?: boolean
}

export function SillyTavernPresetEditor(props: Props) {
  const { t } = useTranslation()
  const parsed = useMemo(() => parsePresetEditor(props.value), [props.value])
  const [open, setOpen] = useState(false)
  const [draft, setDraft] = useState<Record<string, unknown> | null>(null)
  const [baseline, setBaseline] = useState('')
  const [baselineConfig, setBaselineConfig] = useState('')
  const [discard, setDiscard] = useState(false)
  const [selected, setSelected] = useState('')
  const [search, setSearch] = useState('')
  const [tab, setTab] = useState('entries')
  const [focusWriting, setFocusWriting] = useState(false)
  const [mobileEditing, setMobileEditing] = useState(false)
  const [migrationError, setMigrationError] = useState('')
  const dragging = useRef(false)
  const editorHeading = useRef<HTMLHeadingElement>(null)
  const listPanel = useRef<HTMLDivElement>(null)
  const draftParsed = useMemo(
    () => (draft ? parsePresetEditor(draft) : null),
    [draft]
  )
  const entry = draftParsed?.entries.find(
    (item) => item.identifier === selected
  )
  const conflict = open && props.value !== baseline
  const locked = props.disabled || conflict
  const legacy = Array.isArray(draft?.patches) && draft.patches.length > 0
  if (!parsed && !open) return null
  const close = () => {
    if (draft && JSON.stringify(draft) !== baselineConfig) setDiscard(true)
    else setOpen(false)
  }
  const enabledCount =
    parsed?.entries.filter((item) => item.enabled).length ?? 0
  return (
    <>
      <div className='flex flex-wrap items-center justify-between gap-3 rounded-lg border p-4'>
        <div className='flex min-w-0 flex-col gap-1'>
          <p className='text-sm font-medium'>{t('Preset entries')}</p>
          <p className='text-muted-foreground text-xs'>
            {t('{{enabled}} of {{total}} entries enabled', {
              enabled: enabledCount,
              total: parsed?.entries.length ?? 0,
            })}
          </p>
        </div>
        <Button
          type='button'
          variant='outline'
          disabled={props.disabled || !parsed}
          onClick={() => {
            if (!parsed) return
            setDraft(parsed.config)
            setBaseline(props.value)
            setBaselineConfig(JSON.stringify(parsed.config))
            setSelected(parsed.entries[0]?.identifier ?? '')
            setSearch('')
            setTab('entries')
            setFocusWriting(false)
            setMobileEditing(false)
            setMigrationError('')
            setDiscard(false)
            setOpen(true)
          }}
        >
          {t('Edit preset')}
        </Button>
      </div>
      <Dialog
        open={open}
        onOpenChange={(next, details) => {
          if (!next) {
            if (details.reason === 'escape-key' && dragging.current) {
              details.cancel()
              details.allowPropagation()
              return
            }
            close()
          }
        }}
        title={t('Edit preset')}
        description={t(
          'Edit the channel preset. Apply your draft, then save the channel.'
        )}
        onContentKeyDown={(event) => {
          // Let the sortable sensor receive navigation keys normally contained by Base UI.
          if (
            dragging.current &&
            ['ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight'].includes(
              event.key
            )
          ) {
            event.preventBaseUIHandler()
          }
        }}
        contentHeight='100%'
        contentClassName='h-[85dvh] max-h-[calc(100dvh-1rem)] gap-0 overflow-hidden p-0 sm:max-w-[1160px] sm:p-0'
        headerClassName='border-b px-5 py-4 pr-12'
        bodyClassName='h-full min-h-0 py-0'
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
              disabled={locked || !draft}
              onClick={() => {
                if (!draft || locked || props.value !== baseline) return
                props.onChange(JSON.stringify(draft))
                setOpen(false)
              }}
            >
              {t('Apply to channel')}
            </Button>
          </>
        }
      >
        {draft && draftParsed && (
          <div className='flex h-full min-h-0 flex-col'>
            {conflict && (
              <Alert className='shrink-0 rounded-none border-x-0 border-t-0'>
                <AlertTitle>
                  {t('The channel preset changed while this editor was open.')}
                </AlertTitle>
                <AlertDescription>
                  {t(
                    'Your draft is still here. Close and reopen the editor to load the latest preset before applying changes.'
                  )}
                </AlertDescription>
              </Alert>
            )}
            {legacy && (
              <Alert className='shrink-0 rounded-none border-x-0 border-t-0'>
                <AlertTitle>{t('Legacy preset changes')}</AlertTitle>
                <AlertDescription>
                  {t(
                    'Convert saved text patches into editable prompt content before editing entries. Save the channel to persist the conversion.'
                  )}
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    disabled={locked}
                    onClick={() => {
                      const result = migratePresetPatches(JSON.stringify(draft))
                      setMigrationError(result.error || '')
                      if (!result.error) {
                        setDraft(
                          JSON.parse(result.value) as Record<string, unknown>
                        )
                      }
                    }}
                  >
                    {t('Convert to editable entries')}
                  </Button>
                  {migrationError && <p role='status'>{t(migrationError)}</p>}
                </AlertDescription>
              </Alert>
            )}
            <Tabs
              value={tab}
              onValueChange={(value) => setTab(String(value))}
              className='min-h-0 flex-1 gap-0'
            >
              <div className='shrink-0 overflow-x-auto border-b px-4 py-2'>
                <TabsList variant='line' aria-label={t('Preset settings')}>
                  <TabsTrigger value='entries'>
                    {t('Preset entries')}
                  </TabsTrigger>
                  <TabsTrigger value='regex'>
                    {t('Embedded regex scripts')}
                  </TabsTrigger>
                  <TabsTrigger value='macros'>
                    {t('Macro values and time')}
                  </TabsTrigger>
                </TabsList>
              </div>
              <TabsContent
                value='entries'
                keepMounted
                className='min-h-0 overflow-hidden'
              >
                <div className='flex h-full min-h-0'>
                  {!focusWriting && (
                    <div
                      ref={listPanel}
                      className={cn(
                        'w-full shrink-0 border-r md:block md:w-[300px]',
                        mobileEditing && 'hidden'
                      )}
                    >
                      <PresetEntryList
                        entries={draftParsed.entries}
                        selected={selected}
                        search={search}
                        onSearch={setSearch}
                        disabled={locked}
                        canRestore={!!draft.entry_overrides}
                        onSelect={(identifier) => {
                          setSelected(identifier)
                          setMobileEditing(true)
                          requestAnimationFrame(() =>
                            editorHeading.current?.focus()
                          )
                        }}
                        onToggle={(identifiers, enabled) => {
                          const overrides = {
                            ...((draft.entry_overrides || {}) as Record<
                              string,
                              boolean
                            >),
                          }
                          for (const identifier of identifiers) {
                            overrides[identifier] = enabled
                          }
                          setDraft({ ...draft, entry_overrides: overrides })
                        }}
                        onRestore={() => {
                          const next = { ...draft }
                          delete next.entry_overrides
                          setDraft(next)
                        }}
                        onReorder={(identifier, target) =>
                          setDraft(
                            reorderPresetEntries(draft, identifier, target)
                          )
                        }
                        onDragging={(active) => {
                          dragging.current = active
                        }}
                      />
                    </div>
                  )}
                  <section
                    aria-label={t('Entry editor')}
                    className={cn(
                      'min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain p-4 md:block md:p-5',
                      !mobileEditing && !focusWriting && 'hidden'
                    )}
                  >
                    {entry ? (
                      <div className='flex h-full min-h-0 flex-col gap-4'>
                        <div className='flex shrink-0 flex-wrap items-start justify-between gap-2'>
                          <div className='flex min-w-0 flex-1 flex-col gap-1'>
                            <Button
                              type='button'
                              variant='ghost'
                              size='sm'
                              className='mb-1 w-fit md:hidden'
                              onClick={() => {
                                setMobileEditing(false)
                                setFocusWriting(false)
                                requestAnimationFrame(() =>
                                  listPanel.current
                                    ?.querySelector<HTMLButtonElement>(
                                      '[aria-pressed="true"]'
                                    )
                                    ?.focus()
                                )
                              }}
                            >
                              {t('Back to entries')}
                            </Button>
                            <h3
                              ref={editorHeading}
                              tabIndex={-1}
                              className='text-sm font-semibold [overflow-wrap:anywhere] break-words outline-none'
                            >
                              {entry.name}
                            </h3>
                            <p className='text-muted-foreground text-xs break-all'>
                              {t('Identifier')}: <code>{entry.identifier}</code>
                            </p>
                          </div>
                          {!entry.marker && (
                            <Button
                              type='button'
                              size='sm'
                              variant='ghost'
                              aria-pressed={focusWriting}
                              onClick={() => setFocusWriting(!focusWriting)}
                            >
                              {focusWriting
                                ? t('Show entry list')
                                : t('Focus writing')}
                            </Button>
                          )}
                        </div>
                        <PresetEntryFields
                          entry={entry}
                          onChange={(changes) =>
                            setDraft(
                              editPresetConfigEntry(
                                draft,
                                entry.identifier,
                                changes
                              )
                            )
                          }
                          disabled={locked || legacy}
                        />
                      </div>
                    ) : (
                      <EmptyState title={t('Select an entry to edit')} />
                    )}
                  </section>
                </div>
              </TabsContent>
              <TabsContent
                value='regex'
                keepMounted
                className='min-h-0 overflow-y-auto overscroll-contain p-5'
              >
                <PresetRegexSettings
                  config={draft}
                  onChange={(value) =>
                    setDraft(JSON.parse(value) as Record<string, unknown>)
                  }
                  disabled={locked}
                />
              </TabsContent>
              <TabsContent
                value='macros'
                keepMounted
                className='min-h-0 overflow-y-auto overscroll-contain p-5'
              >
                <SillyTavernMacroEditor
                  config={draft}
                  onChange={setDraft}
                  disabled={locked}
                />
              </TabsContent>
            </Tabs>
          </div>
        )}
        <ConfirmDialog
          open={discard}
          onOpenChange={setDiscard}
          title={t('Discard preset changes?')}
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
    </>
  )
}
