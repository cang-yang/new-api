import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance, type i18n } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import zhCN from '@/i18n/locales/zh.json'

import type { BodyAudit } from '../../types'
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
