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
import { useMutation } from '@tanstack/react-query'
import { BookOpen, Upload } from 'lucide-react'
import { useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { handleServerError } from '@/lib/handle-server-error'

import { importWorldbook } from '../api'
import {
  createWorldbookConfig,
  parseWorldbook,
  worldbookConfirmEntry,
  worldbookSchema,
  type Worldbook,
  type WorldbookConfig,
} from '../lib/worldbook'
import { WorldbookEntryFields } from './worldbook-entry-fields'
import { WorldbookEntryOverview } from './worldbook-entry-overview'
import { WorldbookPreview } from './worldbook-preview'

type Props = {
  scopeKey: string | number
  value: string
  presetValue?: string
  onChange: (value: string) => void
  disabled?: boolean
}

export function WorldbookSettings(props: Props) {
  return <ScopedWorldbookSettings key={props.scopeKey} {...props} />
}

function ScopedWorldbookSettings(props: Props) {
  const { t } = useTranslation()
  const current = useMemo(() => parseWorldbook(props.value), [props.value])
  const [open, setOpen] = useState(false)
  const [draft, setDraft] = useState<WorldbookConfig>(createWorldbookConfig)
  const [baseline, setBaseline] = useState('')
  const [initial, setInitial] = useState('')
  const [bookID, setBookID] = useState('')
  const [entryID, setEntryID] = useState('')
  const [tab, setTab] = useState('books')
  const [confirm, setConfirm] = useState<'discard' | 'remove' | null>(null)
  const fileInput = useRef<HTMLInputElement>(null)
  const conflict = open && baseline !== props.value
  const importFile = useMutation({
    mutationFn: async (file: File) => {
      if (file.size > 6 * 1024 * 1024) {
        throw new Error(t('Worldbook files must be smaller than 6 MB.'))
      }
      const book = await importWorldbook(
        file.name.replace(/\.json$/i, ''),
        JSON.parse(await file.text())
      )
      const next = { ...draft, enabled: true, books: [...draft.books, book] }
      if (!worldbookSchema.safeParse(next).success) {
        throw new Error(
          t('Worldbook limits exceeded: up to 16 books and 1000 entries.')
        )
      }
      return { next, book }
    },
    onSuccess: ({ next, book }) => {
      setDraft(next)
      setBookID(book.id)
      setEntryID('')
    },
    onError: (error) => handleServerError(error),
    meta: { errorToast: false },
  })
  const locked = props.disabled || conflict || importFile.isPending
  const book = draft.books.find((item) => item.id === bookID) ?? draft.books[0]
  const entry = book?.entries.find((item) => item.id === entryID)
  const valid = worldbookSchema.safeParse(draft).success
  const updateBook = (next: Worldbook) =>
    setDraft({
      ...draft,
      books: draft.books.map((item) => (item.id === next.id ? next : item)),
    })
  const close = () => {
    if (importFile.isPending) return
    if (JSON.stringify(draft) !== initial) setConfirm('discard')
    else setOpen(false)
  }
  return (
    <>
      <section
        aria-label={t('Worldbooks')}
        className='bg-muted/20 flex min-w-0 flex-wrap items-center gap-3 rounded-xl border p-4'
      >
        <BookOpen className='size-5 shrink-0' aria-hidden='true' />
        <div className='min-w-0 flex-1 space-y-1'>
          <p className='text-sm font-semibold'>{t('Worldbooks')}</p>
          <p className='text-muted-foreground truncate text-xs'>
            {current?.books.length
              ? current.books.map((item) => item.name).join(' · ')
              : t(
                  'Import background information and include relevant entries automatically.'
                )}
          </p>
          {!!current?.books.length && (
            <p className='text-muted-foreground text-xs'>
              {current.enabled ? t('Enabled') : t('Disabled')}
            </p>
          )}
          {!current && (
            <p role='alert' className='text-destructive text-xs'>
              {t('Invalid worldbook settings')}
            </p>
          )}
        </div>
        <Button
          type='button'
          variant='outline'
          disabled={props.disabled || !current}
          onClick={() => {
            if (!current) return
            setDraft(current)
            setInitial(JSON.stringify(current))
            setBaseline(props.value)
            setBookID(current.books[0]?.id || '')
            setEntryID('')
            setTab('books')
            setOpen(true)
          }}
        >
          {t('Manage worldbooks')}
        </Button>
      </section>
      <Dialog
        open={open}
        onOpenChange={(next) => {
          if (!next) close()
        }}
        title={t('Manage worldbooks')}
        description={t(
          'For Chat Completions requests. Reads recent user and assistant text; does not store conversations.'
        )}
        contentClassName='sm:max-w-5xl'
        contentHeight='min(70dvh, 760px)'
        bodyViewportClassName='overflow-hidden'
        bodyClassName='h-full'
        footer={
          <>
            <p className='text-muted-foreground mr-auto text-xs'>
              {t('Apply here, then save the channel to take effect.')}
            </p>
            <Button
              type='button'
              variant='outline'
              disabled={importFile.isPending}
              onClick={close}
            >
              {t('Cancel')}
            </Button>
            <Button
              type='button'
              disabled={locked || !valid}
              onClick={() => {
                props.onChange(JSON.stringify(draft))
                setOpen(false)
              }}
            >
              {t('Apply changes')}
            </Button>
          </>
        }
      >
        {open && (
          <Tabs
            value={tab}
            onValueChange={setTab}
            className='h-full min-h-0 gap-3'
          >
            <div className='flex shrink-0 flex-wrap items-center justify-between gap-3'>
              <TabsList>
                <TabsTrigger value='books'>
                  {t('Books and entries')}
                </TabsTrigger>
                <TabsTrigger value='settings'>
                  {t('Matching settings')}
                </TabsTrigger>
                <TabsTrigger value='preview'>{t('Test matching')}</TabsTrigger>
              </TabsList>
              <Label className='gap-3'>
                {t('Enable worldbooks')}
                <Switch
                  aria-label={t('Enable worldbooks')}
                  disabled={locked}
                  checked={draft.enabled}
                  onCheckedChange={(enabled) => setDraft({ ...draft, enabled })}
                />
              </Label>
            </div>
            {conflict && (
              <p role='alert' className='text-destructive text-sm'>
                {t('Channel settings changed. Close and reopen this editor.')}
              </p>
            )}
            {!valid && (
              <p role='alert' className='text-destructive text-sm'>
                {t('Invalid worldbook settings')}
              </p>
            )}
            <TabsContent
              value={tab}
              className={`min-h-0 min-w-0 flex-1 overflow-x-hidden pr-1 ${tab === 'books' ? 'flex flex-col overflow-y-hidden' : 'overflow-y-auto'}`}
            >
              {tab === 'books' && (
                <div className='flex min-h-0 flex-1 flex-col gap-3'>
                  <div
                    className='flex shrink-0 flex-wrap items-center justify-between gap-2 rounded-lg border border-dashed px-3 py-2'
                    onDragOver={(event) => event.preventDefault()}
                    onDrop={(event) => {
                      event.preventDefault()
                      if (!locked && event.dataTransfer.files[0]) {
                        importFile.mutate(event.dataTransfer.files[0])
                      }
                    }}
                  >
                    <p className='text-muted-foreground text-xs'>
                      {t('Drop a worldbook JSON here, or choose a file.')}
                    </p>
                    <Button
                      type='button'
                      variant='outline'
                      disabled={locked}
                      onClick={() => fileInput.current?.click()}
                    >
                      <Upload className='size-4' aria-hidden='true' />
                      {importFile.isPending
                        ? t('Importing...')
                        : t('Import worldbook')}
                    </Button>
                    <input
                      ref={fileInput}
                      type='file'
                      accept='.json,application/json'
                      className='hidden'
                      aria-label={t('Import worldbook file')}
                      onChange={(event) => {
                        const file = event.target.files?.[0]
                        event.target.value = ''
                        if (file && !locked) importFile.mutate(file)
                      }}
                    />
                  </div>
                  {!book && (
                    <p className='text-muted-foreground py-8 text-center text-sm'>
                      {t(
                        'Import your first worldbook to view and edit its entries.'
                      )}
                    </p>
                  )}
                  {book && (
                    <>
                      <div className='flex shrink-0 flex-wrap items-end gap-3'>
                        <div className='min-w-0 flex-1 basis-full space-y-2 sm:basis-0'>
                          <Label htmlFor='worldbook-book'>
                            {t('Worldbook')}
                          </Label>
                          <Select
                            value={book.id}
                            onValueChange={(value) => {
                              if (!value) return
                              setBookID(value)
                              setEntryID('')
                            }}
                            disabled={locked}
                          >
                            <SelectTrigger
                              id='worldbook-book'
                              className='w-full'
                            >
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent alignItemWithTrigger={false}>
                              {draft.books.map((item) => (
                                <SelectItem key={item.id} value={item.id}>
                                  {item.name}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        </div>
                        <Label className='h-8 gap-3'>
                          {t('Enable book')}
                          <Switch
                            aria-label={t('Enable book')}
                            disabled={locked}
                            checked={book.enabled}
                            onCheckedChange={(enabled) =>
                              updateBook({ ...book, enabled })
                            }
                          />
                        </Label>
                        <Button
                          type='button'
                          variant='ghost'
                          disabled={locked}
                          onClick={() => setConfirm('remove')}
                        >
                          {t('Remove book')}
                        </Button>
                      </div>
                      <div
                        className={
                          entry
                            ? 'hidden'
                            : 'flex min-h-0 flex-1 flex-col gap-3'
                        }
                      >
                        <WorldbookEntryOverview
                          key={book.id}
                          book={book}
                          active={draft.enabled && book.enabled}
                          disabled={locked}
                          onEdit={setEntryID}
                          onChange={(next) =>
                            updateBook({
                              ...book,
                              entries: book.entries.map((item) =>
                                item.id === next.id ? next : item
                              ),
                            })
                          }
                          onEnableAll={(review) =>
                            updateBook({
                              ...book,
                              entries: book.entries.map((item) =>
                                review.some((entry) => entry.id === item.id)
                                  ? worldbookConfirmEntry(item)
                                  : item
                              ),
                            })
                          }
                        />
                      </div>
                      {entry && (
                        <div className='flex min-h-0 flex-1 flex-col gap-3'>
                          <div className='flex shrink-0 items-center gap-3 border-b pb-3'>
                            <Button
                              type='button'
                              variant='outline'
                              onClick={() => setEntryID('')}
                            >
                              {t('Back to all entries')}
                            </Button>
                            <h3 className='min-w-0 truncate text-sm font-semibold'>
                              {entry.name || entry.id}
                            </h3>
                          </div>
                          <div className='min-h-0 flex-1 overflow-y-auto pr-2'>
                            <WorldbookEntryFields
                              key={`${book.id}:${entry.id}`}
                              entry={entry}
                              disabled={locked}
                              onChange={(next) =>
                                updateBook({
                                  ...book,
                                  entries: book.entries.map((item) =>
                                    item.id === next.id ? next : item
                                  ),
                                })
                              }
                            />
                          </div>
                        </div>
                      )}
                    </>
                  )}
                </div>
              )}
              {tab === 'settings' && (
                <fieldset disabled={locked} className='space-y-5'>
                  <div className='space-y-2'>
                    <Label htmlFor='worldbook-depth'>
                      {t('Recent messages to check')}
                    </Label>
                    <Input
                      id='worldbook-depth'
                      type='number'
                      min={1}
                      max={50}
                      value={draft.scan_depth}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          scan_depth: Number(event.target.value),
                        })
                      }
                    />
                    <p className='text-muted-foreground text-xs'>
                      {t(
                        'Checks recent user, assistant and tool-call text. System prompts and media metadata are not scanned.'
                      )}
                    </p>
                  </div>
                  <div className='space-y-2'>
                    <Label htmlFor='worldbook-budget'>
                      {t('Maximum added characters')}
                    </Label>
                    <Input
                      id='worldbook-budget'
                      type='number'
                      min={256}
                      max={64000}
                      value={draft.max_chars}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          max_chars: Number(event.target.value),
                        })
                      }
                    />
                    <p className='text-muted-foreground text-xs'>
                      {t(
                        'Higher-priority entries are included first. Oversized entries are skipped, not cut in half. This is a character limit, not a token limit.'
                      )}
                    </p>
                  </div>
                  <div className='space-y-2'>
                    <Label
                      htmlFor='worldbook-recursive'
                      className='justify-between gap-3'
                    >
                      {t('Include related entries')}
                      <Switch
                        id='worldbook-recursive'
                        checked={draft.recursive}
                        disabled={locked}
                        onCheckedChange={(recursive) =>
                          setDraft({ ...draft, recursive })
                        }
                      />
                    </Label>
                    <p className='text-muted-foreground text-xs'>
                      {t(
                        'Included entries can trigger more entries, for up to two extra passes. The same content is not added twice.'
                      )}
                    </p>
                  </div>
                  <div className='space-y-2'>
                    <Label htmlFor='worldbook-injection-mode'>
                      {t('Insertion position')}
                    </Label>
                    <Select
                      value={draft.injection_mode}
                      onValueChange={(value) =>
                        setDraft({
                          ...draft,
                          injection_mode:
                            value as WorldbookConfig['injection_mode'],
                        })
                      }
                      disabled={locked}
                    >
                      <SelectTrigger
                        id='worldbook-injection-mode'
                        className='w-full'
                      >
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent alignItemWithTrigger={false}>
                        <SelectItem value='inherit'>
                          {t('Follow preset worldbook markers')}
                        </SelectItem>
                        <SelectItem value='system_prelude'>
                          {t('System prelude')}
                        </SelectItem>
                        <SelectItem value='before_last_user'>
                          {t('Before the latest user message')}
                        </SelectItem>
                        <SelectItem value='at_depth'>
                          {t('At a fixed conversation depth')}
                        </SelectItem>
                      </SelectContent>
                    </Select>
                    <p className='text-muted-foreground text-xs'>
                      {t(
                        'Matched entries are placed before preset scripts run. Follow preset markers uses the worldbook section when enabled; otherwise it inserts background before the conversation.'
                      )}
                    </p>
                  </div>
                  {draft.injection_mode === 'at_depth' && (
                    <div className='space-y-2'>
                      <Label htmlFor='worldbook-injection-depth'>
                        {t('Conversation depth')}
                      </Label>
                      <Input
                        id='worldbook-injection-depth'
                        type='number'
                        min={0}
                        max={20}
                        value={draft.injection_depth}
                        onChange={(event) =>
                          setDraft({
                            ...draft,
                            injection_depth: Number(event.target.value),
                          })
                        }
                      />
                      <p className='text-muted-foreground text-xs'>
                        {t(
                          'Depth 0 adds background after the latest message; depth 1 places it before that message. Tool calls and results stay together.'
                        )}
                      </p>
                    </div>
                  )}
                </fieldset>
              )}
              {tab === 'preview' && (
                <WorldbookPreview
                  config={draft}
                  presetValue={props.presetValue}
                  disabled={locked || !valid}
                />
              )}
            </TabsContent>
          </Tabs>
        )}
      </Dialog>
      <ConfirmDialog
        open={confirm !== null}
        onOpenChange={(next) => {
          if (!next) setConfirm(null)
        }}
        title={confirm === 'remove' ? t('Remove book?') : t('Discard changes?')}
        desc={
          confirm === 'remove'
            ? t(
                'This removes the book from this channel draft. The source file is not deleted.'
              )
            : t('Your unapplied worldbook edits will be discarded.')
        }
        destructive
        confirmText={confirm === 'remove' ? t('Remove') : t('Discard')}
        handleConfirm={() => {
          if (confirm === 'remove' && book) {
            setDraft({
              ...draft,
              books: draft.books.filter((item) => item.id !== book.id),
            })
            setBookID('')
            setEntryID('')
          } else setOpen(false)
          setConfirm(null)
        }}
      />
    </>
  )
}
