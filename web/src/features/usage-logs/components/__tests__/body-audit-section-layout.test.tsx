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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance, type i18n } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import zhCN from '@/i18n/locales/zh.json'

import type { BodyAudit } from '../../types'
import { BodyAuditJsonContent } from '../body-audit-json-content'
import { BodyAuditSection } from '../dialogs/body-audit-section'

const { getAudit, previewReplay } = vi.hoisted(() => ({
  getAudit: vi.fn(),
  previewReplay: vi.fn(),
}))

vi.mock('../../api', () => ({
  executeBodyAuditReplay: vi.fn(),
  getBodyAudit: getAudit,
  previewBodyAuditReplay: previewReplay,
}))

const audit: BodyAudit = {
  request_id: 'request-1',
  created_at: 1,
  updated_at: 1,
  model_name: 'glm-5.2',
  channel_id: 2093,
  request_body: '{"model":"glm-5.2"}',
  request_body_encoding: 'utf-8',
  request_body_size: 19,
  request_body_truncated: false,
  response_body: '{"choices":[]}',
  response_body_encoding: 'utf-8',
  response_body_size: 14,
  response_body_truncated: false,
  response_status: 200,
  response_content_type: 'application/json',
  response_complete: true,
  client_response_body: '{"choices":[]}',
  client_response_body_encoding: 'utf-8',
  client_response_body_size: 14,
  client_response_body_truncated: false,
  client_response_status: 200,
  client_response_content_type: 'application/json',
  client_response_complete: true,
  attempts: [
    {
      id: 7,
      attempt_no: 0,
      routing_retry_index: 0,
      channel_id: 2093,
      channel_type: 8,
      channel_name: '百度千帆_正常',
      request_model: 'glm-5.2',
      upstream_model: 'glm-5.2',
      request_format: 'openai',
      upstream_format: 'openai',
      target: 'https://qianfan.example/chat/completions',
      state: 'succeeded',
      outcome: 'complete',
      http_status: 200,
      error_code: '',
      error_message: '',
      retry_action: '',
      terminal_kind: 'protocol_terminal',
      complete: true,
      duration_ms: 3000,
      request_body: '{"model":"glm-5.2"}',
      request_body_encoding: 'utf-8',
      request_body_size: 19,
      request_body_truncated: false,
      response_body: '{"choices":[]}',
      response_body_encoding: 'utf-8',
      response_body_size: 14,
      response_body_truncated: false,
    },
  ],
}

function renderAudit() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <I18nextProvider i18n={testI18n}>
      <QueryClientProvider client={queryClient}>
        <BodyAuditSection requestId='request-1' enabled />
      </QueryClientProvider>
    </I18nextProvider>
  )
}

let testI18n: i18n

describe('body audit attempt actions', () => {
  beforeEach(async () => {
    vi.clearAllMocks()
    testI18n = createInstance()
    await testI18n.init({
      lng: 'zhCN',
      fallbackLng: 'zhCN',
      resources: { zhCN },
      nsSeparator: false,
      interpolation: { escapeValue: false },
    })
    getAudit.mockResolvedValue(audit)
    previewReplay.mockImplementation(
      async (_requestId: string, _attemptId: number, mode: string) => ({
        mode,
        available: false,
        unavailable_reason: 'client_level_replay_not_supported',
      })
    )
  })

  test('renders the attempt timeline fully localized in Chinese', async () => {
    const user = userEvent.setup()
    renderAudit()

    expect(
      await screen.findByRole('tab', { name: /^发送正文/ })
    ).toBeInTheDocument()
    expect(getAudit).toHaveBeenCalledWith('request-1', false)
    expect(screen.getByRole('tab', { name: /^响应/ })).toBeInTheDocument()
    const executionTab = screen.getByRole('tab', { name: '执行过程' })
    expect(executionTab).toBeInTheDocument()
    expect(screen.queryByText('上游尝试时间线')).not.toBeInTheDocument()

    await user.click(executionTab)

    expect(await screen.findByText('上游尝试时间线')).toBeInTheDocument()
    expect(screen.getByText('共 1 次尝试')).toBeInTheDocument()
    expect(screen.getByText('第 1 次尝试')).toBeInTheDocument()
    expect(
      screen.queryByRole('tab', { name: '本次请求' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('tab', { name: '本次响应' })
    ).not.toBeInTheDocument()
    expect(getAudit).not.toHaveBeenCalledWith('request-1', true)
  })

  test('keeps all three response views and mounts them on demand', async () => {
    const user = userEvent.setup()
    renderAudit()

    await user.click(await screen.findByRole('tab', { name: /^响应/ }))

    expect(screen.getByRole('tab', { name: '最终结果' })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: '客户端原文' })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: '上游返回' })).toBeInTheDocument()
    expect(
      screen.queryByText('New API 实际返回给客户端的原始正文')
    ).not.toBeInTheDocument()

    await user.click(screen.getByRole('tab', { name: '客户端原文' }))
    expect(
      screen.getByText('New API 实际返回给客户端的原始正文')
    ).toBeInTheDocument()
  })

  test('reads decoded request messages without unescaping literal backslashes', async () => {
    const user = userEvent.setup()
    getAudit.mockResolvedValue({
      ...audit,
      request_body:
        '{"messages":[{"role":"user","content":"first\\nsecond \\"quoted\\" C:\\\\temp"}]}',
    })
    renderAudit()
    await user.click(await screen.findByRole('tab', { name: '可读内容' }))
    const content = screen.getByText(/first second "quoted"/)
    expect(content.textContent).toContain('first\nsecond "quoted" C:\\temp')
    await user.click(screen.getByRole('button', { name: '复制原文' }))
    expect(await navigator.clipboard.readText()).toBe(
      '{"messages":[{"role":"user","content":"first\\nsecond \\"quoted\\" C:\\\\temp"}]}'
    )
  })

  test('shows a retryable error instead of claiming the audit is missing', async () => {
    getAudit.mockRejectedValue(new Error('network unavailable'))
    renderAudit()
    expect(
      await screen.findByRole('button', { name: '重试' })
    ).toBeInTheDocument()
    expect(screen.queryByText('此请求未存储正文审计')).not.toBeInTheDocument()
  })

  test('reads Responses string input and instructions without hiding the request text', async () => {
    const user = userEvent.setup()
    getAudit.mockResolvedValue({
      ...audit,
      request_body: JSON.stringify({
        instructions: 'Please answer\nbriefly',
        input: 'First line\nSecond line',
      }),
    })
    renderAudit()
    await user.click(await screen.findByRole('tab', { name: '可读内容' }))
    expect(screen.getByText(/Please answer briefly/).textContent).toContain(
      'Please answer\nbriefly'
    )
    expect(screen.getByText(/First line Second line/).textContent).toContain(
      'First line\nSecond line'
    )
  })

  test('includes tool descriptions and parameters in readable request context', async () => {
    const user = userEvent.setup()
    getAudit.mockResolvedValue({
      ...audit,
      request_body: JSON.stringify({
        messages: [{ role: 'user', content: 'Continue' }],
        tools: [
          {
            type: 'function',
            function: {
              name: 'game_content',
              description: 'World context\nHistorical context',
              parameters: {
                type: 'object',
                properties: { content: { type: 'string' } },
              },
            },
          },
        ],
        temperature: 0.7,
      }),
    })
    renderAudit()
    await user.click(await screen.findByRole('tab', { name: '可读内容' }))
    expect(
      screen.getByText(/World context Historical context/).textContent
    ).toBe('World context\nHistorical context')
    expect(screen.getByText('game_content')).toBeInTheDocument()
    expect(screen.getByText('0.7')).toBeInTheDocument()
  })

  test('can reveal fields beyond the initial preview and expand long text', async () => {
    const user = userEvent.setup()
    const longText = `${'Long context\n'.repeat(400)}Context ending`
    render(
      <I18nextProvider i18n={testI18n}>
        <BodyAuditJsonContent
          value={{
            messages: Array.from({ length: 260 }, (_, i) => `Message ${i}`),
            description: longText,
          }}
        />
      </I18nextProvider>
    )
    expect(screen.queryByText('$["description"]')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /^展开 ·/ }))
    expect(screen.getByText('$["description"]')).toBeInTheDocument()
    await user.click(screen.getByText('展开长文本'))
    await waitFor(() =>
      expect(screen.getByText(/Context ending/).textContent).toBe(longText)
    )
  })

  test('shows tool results and invalid arguments instead of only reasoning', async () => {
    const user = userEvent.setup()
    getAudit.mockResolvedValue({
      ...audit,
      client_response_body: JSON.stringify({
        choices: [
          {
            message: {
              reasoning_content: 'analysis',
              tool_calls: [
                {
                  id: 'call-1',
                  function: {
                    name: 'submit_image_plan',
                    arguments: '{"images":[}',
                  },
                },
              ],
            },
          },
        ],
      }),
    })
    renderAudit()
    await user.click(await screen.findByRole('tab', { name: /^响应/ }))
    expect(screen.getByText('submit_image_plan')).toBeInTheDocument()
    expect(screen.getByText('工具参数不是有效的 JSON')).toBeInTheDocument()
    expect(screen.getByText('{"images":[}')).toBeInTheDocument()
  })

  test('warns in final result when a capture is truncated even if client writes succeeded', async () => {
    const user = userEvent.setup()
    getAudit.mockResolvedValue({
      ...audit,
      client_response_body: '{"choices":[{"message":{"content":"partial"}}]}',
      client_response_body_truncated: true,
      client_response_complete: true,
    })
    renderAudit()
    await user.click(await screen.findByRole('tab', { name: /^响应/ }))
    expect(screen.getByText('采集已截断：此结果可能不完整')).toBeInTheDocument()
  })

  test('distinguishes an interrupted client write from a complete upstream capture', async () => {
    const user = userEvent.setup()
    getAudit.mockResolvedValue({
      ...audit,
      client_response_body: 'eyJ4Ijoi5Q==',
      client_response_body_encoding: 'base64',
      client_response_body_size: 7,
      client_response_complete: false,
    })
    renderAudit()

    await user.click(await screen.findByRole('tab', { name: /^响应/ }))
    expect(screen.getByText('上游采集完整')).toBeInTheDocument()
    expect(screen.getByText('客户端响应写入中断')).toBeInTheDocument()

    await user.click(screen.getByRole('tab', { name: '客户端原文' }))
    expect(
      screen.getByText(
        '客户端响应写入中断；这里只记录了成功写出的部分，可能断在一个字符中间。'
      )
    ).toBeInTheDocument()
    expect(screen.getByText('eyJ4Ijoi5Q==')).toBeInTheDocument()
    expect(
      screen.queryByText('二进制正文以 Base64 显示')
    ).not.toBeInTheDocument()
  })

  test('labels reasoning-only streams without showing raw SSE as the final result', async () => {
    const user = userEvent.setup()
    getAudit.mockResolvedValue({
      ...audit,
      response_body:
        'data: {"choices":[{"delta":{"reasoning":"分析中"}}]}\n\ndata: [DONE]\n\n',
      response_content_type: 'text/event-stream',
      client_response_body: '',
      client_response_body_size: 0,
      client_response_status: 0,
      client_response_complete: false,
    })
    renderAudit()

    await user.click(await screen.findByRole('tab', { name: /^响应/ }))

    expect(screen.getByText('分析中')).toBeInTheDocument()
    expect(screen.getByText('推理')).toBeInTheDocument()
    expect(screen.queryByText(/data: \{"choices"/)).not.toBeInTheDocument()
  })

  test('retains per-attempt payload tabs when retries occurred', async () => {
    const user = userEvent.setup()
    const firstAttempt = audit.attempts?.[0]
    if (!firstAttempt) throw new Error('test fixture requires an audit attempt')
    getAudit.mockResolvedValue({
      ...audit,
      attempts: [firstAttempt, { ...firstAttempt, id: 8, attempt_no: 1 }],
    })
    renderAudit()

    await user.click(await screen.findByRole('tab', { name: '执行过程' }))

    expect(
      await screen.findByRole('tab', { name: '本次请求' })
    ).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: '本次响应' })).toBeInTheDocument()
    expect(getAudit).toHaveBeenCalledWith('request-1', true)
  })

  test('keeps replay in one overflow menu for the selected attempt', async () => {
    const user = userEvent.setup()
    renderAudit()

    await user.click(await screen.findByRole('tab', { name: '执行过程' }))

    const moreActions = await screen.findByRole('button', {
      name: '更多尝试操作',
    })
    expect(
      screen.queryByRole('button', { name: /重放第.*次尝试/ })
    ).not.toBeInTheDocument()
    expect(screen.queryByText('重放所选尝试')).not.toBeInTheDocument()

    await user.click(moreActions)
    const replay = await screen.findByRole('menuitem', {
      name: '重放所选尝试',
    })
    await user.click(replay)

    await waitFor(() => expect(previewReplay).toHaveBeenCalledTimes(2))
    expect(await screen.findByRole('alertdialog')).toBeInTheDocument()
  })
})
