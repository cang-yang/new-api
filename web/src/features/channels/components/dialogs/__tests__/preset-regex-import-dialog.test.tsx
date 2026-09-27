/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the GNU
Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { render, screen } from '@testing-library/react'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { expect, test, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import zh from '@/i18n/locales/zh.json'

import { PresetRegexImportDialog } from '../preset-regex-import-dialog'

test('Chinese locale translates every visible preset regex import choice and warning', async () => {
  const i18n = createInstance()
  await i18n.init({
    lng: 'zhCN',
    fallbackLng: 'en',
    resources: { en, zhCN: zh },
    keySeparator: false,
    interpolation: { escapeValue: false },
  })
  render(
    <I18nextProvider i18n={i18n}>
      <PresetRegexImportDialog
        scripts={[
          {
            scriptName: 'HTML',
            placement: [2],
            findRegex: '/x/g',
            replaceString: '<div>reply</div>',
          },
        ]}
        onClose={vi.fn()}
        onImport={vi.fn()}
      />
    </I18nextProvider>
  )
  expect(
    screen.getByRole('alertdialog', { name: '导入预设内的正则脚本？' })
  ).toBeVisible()
  expect(screen.getByRole('switch', { name: '导入接收侧规则' })).toBeChecked()
  expect(screen.getByText('接收侧规则')).toBeVisible()
  expect(screen.getByText('发送侧规则')).toBeVisible()
  expect(
    screen.getByText(/请选择要复制到此渠道正则规则中的内置脚本/)
  ).toBeVisible()
  expect(screen.getByText(/在返回客户端前处理上游回复内容/)).toBeVisible()
  expect(screen.getByText(/处理发往上游的消息/)).toBeVisible()
  expect(screen.getByText(/部分接收侧规则会生成 HTML/)).toBeVisible()
  expect(screen.getByRole('button', { name: '仅导入预设' })).toBeVisible()
  expect(screen.getByRole('button', { name: '导入正则规则' })).toBeVisible()
  expect(screen.queryByText(/The preset is ready/)).not.toBeInTheDocument()
})
