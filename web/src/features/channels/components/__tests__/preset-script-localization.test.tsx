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
import { act, cleanup, render, screen } from '@testing-library/react'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, expect, test, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import fr from '@/i18n/locales/fr.json'
import ja from '@/i18n/locales/ja.json'
import ru from '@/i18n/locales/ru.json'
import viLocale from '@/i18n/locales/vi.json'
import zhTW from '@/i18n/locales/zh-TW.json'
import zh from '@/i18n/locales/zh.json'

import { PresetScriptSettings } from '../preset-script-settings'

afterEach(cleanup)

const resources = { en, fr, ja, ru, vi: viLocale, zh, 'zh-TW': zhTW }
const presetControlKeys = [
  'How should this preset be processed?',
  'Choose how this preset handles messages and replies. Only run extras from authors you trust.',
  'Do not use preset extras',
  'The normal preset text still applies.',
  'Use preset settings (SPreset)',
  'Use SPreset settings to organize sent messages, attach preset tools and process replies. Choose this when the author requires SPreset.',
  'Run bundled scripts',
  'Run the scripts below that the author enabled. Choose this when the author asks you to enable Tavern Helper scripts.',
  'Both options were found. Only one can run at a time; follow the preset author’s instructions.',
  'No supported preset extras were found. The normal preset text can still be used.',
  'Current draft: preset extras off',
  'Current draft: use preset settings (SPreset)',
  'Current draft: run bundled scripts',
  'The selected option is unavailable',
  'Choose Off or import a preset with SPreset settings.',
  'No author-enabled scripts are available. Choose Off or import a preset with enabled scripts.',
  'Apply these changes, then save the channel for them to take effect.',
  'Some Tavern functions are unavailable. See advanced details for limits.',
  'Bundled script files',
  'Checking and downloading files does not enable scripts or change your choice above.',
  'Files checked',
  'Disabled by author',
  'Not checked',
  'Check failed',
  'Passing the file check does not guarantee every script function works.',
  'Check and download files',
  'Checking and downloading files…',
  'Advanced details and manual files',
  'This script builds file names while running. Ask the author for a compatible version and the required files; retrying the download alone will not fix it.',
  'A required file is missing. Check and download files, or ask the author for a file bundle.',
  'This script contains an error. Ask the author for a corrected version.',
  'This script uses features that are unavailable here. Ask the author for a compatible version.',
  'This script is too large to check. Ask the author for a smaller version.',
  'This script needs too many linked files. Ask the author for a simplified version.',
  'A required file could not be downloaded. Check the connection and try again, or ask the author for the file.',
  'The file check was cancelled or took too long. Try checking the files again.',
  'This script is saved in an unsupported format. Ask the author for a compatible preset.',
  'This script has a missing or duplicate identifier. Ask the author to correct the preset.',
  'This preset has too many scripts. Ask the author for a version with fewer scripts.',
  'This script could not be checked. Try again or ask the author for a compatible version.',
]

test.each(Object.entries(resources))(
  'the real %s locale provides preset control copy through the translation namespace',
  async (language, resource) => {
    const i18n = createInstance()
    await i18n.init({
      lng: language,
      fallbackLng: false,
      resources: { [language]: resource },
      keySeparator: false,
    })
    expect(Object.keys(resource)).toEqual(['translation'])
    for (const key of presetControlKeys) {
      expect(i18n.exists(key), key).toBe(true)
      expect(i18n.t(key)).toBeTruthy()
      if (language !== 'en') expect(i18n.t(key), key).not.toBe(key)
    }
  }
)

test('the real Chinese resources render controls and update their accessible names after a language change', async () => {
  const i18n = createInstance()
  await i18n.init({
    lng: 'zh',
    fallbackLng: 'en',
    resources,
    keySeparator: false,
  })
  render(
    <I18nextProvider i18n={i18n}>
      <PresetScriptSettings
        config={{ preset: { extensions: { SPreset: { enabled: true } } } }}
        onChange={vi.fn()}
      />
    </I18nextProvider>
  )
  expect(screen.getByRole('radio', { name: '关闭附加功能' })).toBeVisible()
  expect(
    screen.getByRole('radio', { name: '按预设设置处理（SPreset）' })
  ).toBeVisible()
  expect(screen.getByText('当前草稿：关闭附加功能')).toBeVisible()
  await act(async () => {
    await i18n.changeLanguage('en')
  })
  expect(
    screen.getByRole('radio', { name: 'Do not use preset extras' })
  ).toBeVisible()
  expect(screen.queryByRole('radio', { name: '关闭附加功能' })).toBeNull()
})
