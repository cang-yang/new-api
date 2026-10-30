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
import { createParser } from 'eventsource-parser'

export interface AuditToolCall {
  key: string
  id: string
  name: string
  arguments: string
  valid: boolean
  choiceIndex: number
}

export interface AuditOutput {
  index: number
  text: string
  reasoning: string
}

export type AuditWarning =
  | 'length'
  | 'malformed-event'
  | 'upstream-error'
  | 'incomplete'
  | 'content-filter'
  | 'invalid-json'

export interface AuditContent {
  outputs: AuditOutput[]
  toolCalls: AuditToolCall[]
  warnings: AuditWarning[]
}

function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {}
  return value as Record<string, unknown>
}

function text(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

function index(value: unknown, fallback = 0): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : fallback
}

// Each protocol identifies its own blocks. Snapshots replace only their block;
// repeated prose is never deduplicated by comparing string contents.
class AuditContentAccumulator {
  private blocks = new Map<string, AuditOutput>()
  private tools = new Map<string, AuditToolCall>()
  private initialToolInputs = new Map<string, unknown>()
  private warnings = new Set<AuditWarning>()

  warn(warning: AuditWarning) {
    this.warnings.add(warning)
  }

  private block(key: string, choiceIndex = 0): AuditOutput {
    let block = this.blocks.get(key)
    if (!block) {
      block = { index: choiceIndex, text: '', reasoning: '' }
      this.blocks.set(key, block)
    }
    return block
  }

  private tool(
    key: string,
    value: Record<string, unknown>,
    choiceIndex: number,
    append: boolean
  ) {
    const existing = this.tools.get(key) ?? {
      key,
      id: '',
      name: '',
      arguments: '',
      valid: false,
      choiceIndex,
    }
    const fn = record(value.function)
    const name = text(fn.name || value.name)
    const args = fn.arguments ?? value.arguments ?? value.input ?? value.args
    if (value.id || value.call_id) existing.id = text(value.call_id || value.id)
    if (name) existing.name = append ? existing.name + name : name
    if (args !== undefined) {
      const serialized = typeof args === 'string' ? args : JSON.stringify(args)
      existing.arguments = append ? existing.arguments + serialized : serialized
    }
    this.tools.set(key, existing)
  }

  private parts(
    value: unknown,
    key: string,
    choiceIndex: number,
    append: boolean
  ) {
    const block = this.block(key, choiceIndex)
    let answer = '',
      reasoning = ''
    if (typeof value === 'string') answer = value
    if (Array.isArray(value)) {
      value.forEach((item, partIndex) => {
        if (typeof item === 'string') {
          answer += item
          return
        }
        const part = record(item)
        if (
          part.thought === true ||
          part.type === 'thinking' ||
          part.type === 'reasoning'
        ) {
          reasoning += text(part.thinking || part.text)
        } else if (part.type === 'tool_use' || part.type === 'function_call') {
          this.tool(`${key}:tool:${partIndex}`, part, choiceIndex, append)
        } else if (part.functionCall) {
          this.tool(
            `${key}:tool:${partIndex}`,
            record(part.functionCall),
            choiceIndex,
            false
          )
        } else if (
          !part.type ||
          ['text', 'output_text', 'refusal', 'summary_text'].includes(
            text(part.type)
          )
        ) {
          answer += text(part.text || part.refusal)
        }
      })
    }
    block.text = append ? block.text + answer : answer
    block.reasoning = append ? block.reasoning + reasoning : reasoning
  }

  snapshot(value: unknown, depth = 0) {
    if (depth > 12) return
    const data = record(value)
    if (data.error) this.warn('upstream-error')
    if (data.status === 'incomplete') {
      this.warn(
        record(data.incomplete_details).reason === 'max_output_tokens'
          ? 'length'
          : 'incomplete'
      )
    }
    if (data.stop_reason === 'max_tokens') this.warn('length')
    if (Array.isArray(data.choices)) {
      data.choices.forEach((choice, position) =>
        this.chat(record(choice), position)
      )
      return
    }
    if (Array.isArray(data.candidates)) {
      data.candidates.forEach((candidate, position) => {
        const item = record(candidate)
        const n = index(item.index, position)
        this.parts(record(item.content).parts, `gemini:${n}`, n, false)
        if (item.finishReason === 'MAX_TOKENS') this.warn('length')
      })
      return
    }
    if (Array.isArray(data.output)) {
      data.output.forEach((item, position) =>
        this.responseItem(record(item), position)
      )
      return
    }
    if (Array.isArray(data.content) || typeof data.content === 'string') {
      this.parts(data.content, 'message', 0, false)
      return
    }
    if (
      typeof data.output_text === 'string' ||
      typeof data.completion === 'string' ||
      typeof data.text === 'string'
    ) {
      this.block('message').text = text(
        data.output_text ?? data.completion ?? data.text
      )
      return
    }
    for (const key of ['data', 'response', 'message']) {
      if (data[key] && typeof data[key] === 'object') {
        this.snapshot(data[key], depth + 1)
        return
      }
    }
  }

  private chat(choice: Record<string, unknown>, position: number) {
    const n = index(choice.index, position)
    const key = `chat:${n}`
    const append = !!choice.delta
    const message = record(choice.delta ?? choice.message)
    const block = this.block(key, n)
    if (message.content !== undefined) {
      // Text chunks may omit reasoning; updating one field must not clear another.
      const previousReasoning = block.reasoning
      this.parts(message.content, key, n, append)
      if (!append && !block.reasoning) block.reasoning = previousReasoning
    } else if (typeof choice.text === 'string') {
      block.text += choice.text
    }
    let reasoning = text(message.reasoning_content || message.reasoning)
    if (!reasoning && Array.isArray(message.reasoning_details)) {
      reasoning = message.reasoning_details
        .map((item) => text(record(item).text))
        .join('')
    }
    if (reasoning) {
      block.reasoning = append ? block.reasoning + reasoning : reasoning
    }
    if (Array.isArray(message.tool_calls)) {
      message.tool_calls.forEach((call, toolIndex) => {
        const tool = record(call)
        this.tool(
          `${key}:tool:${index(tool.index, toolIndex)}`,
          tool,
          n,
          append
        )
      })
    }
    if (message.function_call) {
      this.tool(`${key}:legacy`, record(message.function_call), n, append)
    }
    if (choice.finish_reason === 'length') this.warn('length')
    if (choice.finish_reason === 'content_filter') this.warn('content-filter')
  }

  private responseItem(item: Record<string, unknown>, outputIndex: number) {
    if (item.type === 'function_call') {
      this.tool(`response:${outputIndex}:tool`, item, 0, false)
    } else if (item.type === 'reasoning') {
      const summary = Array.isArray(item.summary) ? item.summary : []
      summary.forEach((part, partIndex) => {
        this.block(`response:${outputIndex}:reasoning:${partIndex}`).reasoning =
          text(record(part).text)
      })
    } else if (Array.isArray(item.content)) {
      item.content.forEach((part, partIndex) => {
        this.parts(
          [part],
          `response:${outputIndex}:text:${partIndex}`,
          0,
          false
        )
      })
    }
  }

  event(value: unknown, eventName?: string) {
    const data = record(value)
    const type = text(data.type) || eventName || ''
    if (data.error || type === 'error' || type === 'response.failed') {
      this.warn('upstream-error')
    }
    if (type === 'response.incomplete') {
      this.warn(
        record(record(data.response).incomplete_details).reason ===
          'max_output_tokens'
          ? 'length'
          : 'incomplete'
      )
    }
    if (type.startsWith('response.')) {
      const n = index(data.output_index)
      if (
        [
          'response.completed',
          'response.done',
          'response.incomplete',
          'response.failed',
        ].includes(type)
      ) {
        // Only a real final output snapshot replaces accumulated blocks.
        if (
          Array.isArray(record(data.response).output) &&
          (record(data.response).output as unknown[]).length > 0
        ) {
          this.blocks.clear()
          this.tools.clear()
          this.snapshot(data.response)
        }
      } else if (
        type === 'response.output_item.done' ||
        type === 'response.output_item.added'
      ) {
        this.responseItem(record(data.item), n)
      } else if (
        type === 'response.content_part.done' ||
        type === 'response.content_part.added'
      ) {
        this.parts(
          [data.part],
          `response:${n}:text:${index(data.content_index)}`,
          0,
          false
        )
      } else if (
        type === 'response.output_text.delta' ||
        type === 'response.output_text.done' ||
        type === 'response.refusal.delta' ||
        type === 'response.refusal.done'
      ) {
        const block = this.block(
          `response:${n}:text:${index(data.content_index)}`
        )
        if (type.endsWith('.delta')) block.text += text(data.delta)
        else block.text = text(data.text ?? data.refusal)
      } else if (
        type === 'response.reasoning_summary_text.delta' ||
        type === 'response.reasoning_summary_text.done'
      ) {
        const block = this.block(
          `response:${n}:reasoning:${index(data.summary_index)}`
        )
        if (type.endsWith('.delta')) block.reasoning += text(data.delta)
        else block.reasoning = text(data.text)
      } else if (
        type === 'response.function_call_arguments.delta' ||
        type === 'response.function_call_arguments.done'
      ) {
        this.tool(
          `response:${n}:tool`,
          { arguments: data.delta ?? data.arguments },
          0,
          type.endsWith('.delta')
        )
      }
      return
    }
    if (type === 'content_block_start') {
      const block = record(data.content_block)
      const key = `anthropic:${index(data.index)}`
      if (block.type === 'tool_use') {
        // Streaming starts with an empty placeholder input before JSON deltas.
        this.tool(`${key}:tool`, { id: block.id, name: block.name }, 0, false)
        this.initialToolInputs.set(`${key}:tool`, block.input)
      } else this.parts([block], key, 0, false)
      return
    }
    if (type === 'content_block_delta') {
      const delta = record(data.delta)
      const key = `anthropic:${index(data.index)}`
      if (delta.type === 'text_delta') this.block(key).text += text(delta.text)
      if (delta.type === 'thinking_delta') {
        this.block(key).reasoning += text(delta.thinking)
      }
      if (delta.type === 'input_json_delta') {
        this.initialToolInputs.delete(`${key}:tool`)
        this.tool(`${key}:tool`, { arguments: delta.partial_json }, 0, true)
      }
      return
    }
    if (type === 'message_delta') {
      if (record(data.delta).stop_reason === 'max_tokens') this.warn('length')
      return
    }
    if (type === 'content_block_stop') {
      const key = `anthropic:${index(data.index)}:tool`
      if (this.initialToolInputs.has(key)) {
        this.tool(key, { input: this.initialToolInputs.get(key) }, 0, false)
        this.initialToolInputs.delete(key)
      }
      return
    }
    if (type === 'message_start' || type === 'message_stop') return
    if (Array.isArray(data.candidates)) {
      data.candidates.forEach((candidate, position) => {
        const item = record(candidate)
        const n = index(item.index, position)
        this.parts(record(item.content).parts, `gemini:${n}`, n, true)
        if (item.finishReason === 'MAX_TOKENS') this.warn('length')
      })
      return
    }
    this.snapshot(data)
  }

  result(): AuditContent {
    const outputs = new Map<number, AuditOutput>()
    for (const block of this.blocks.values()) {
      const output = outputs.get(block.index) ?? {
        index: block.index,
        text: '',
        reasoning: '',
      }
      output.text += block.text
      output.reasoning += block.reasoning
      outputs.set(block.index, output)
    }
    const toolCalls = [...this.tools.values()].map((tool) => {
      try {
        const args: unknown = JSON.parse(tool.arguments)
        return {
          ...tool,
          valid: !!args && typeof args === 'object' && !Array.isArray(args),
        }
      } catch {
        return { ...tool, valid: false }
      }
    })
    return {
      outputs: [...outputs.values()].sort((a, b) => a.index - b.index),
      toolCalls,
      warnings: [...this.warnings],
    }
  }
}

export function readAuditContent(value: unknown): AuditContent {
  const accumulator = new AuditContentAccumulator()
  accumulator.snapshot(value)
  return accumulator.result()
}

export function readAuditStream(body: string): AuditContent {
  const accumulator = new AuditContentAccumulator()
  const parser = createParser({
    onEvent(event) {
      if (event.data.trim() === '[DONE]') return
      try {
        accumulator.event(JSON.parse(event.data), event.event)
      } catch {
        accumulator.warn('malformed-event')
      }
    },
  })
  parser.feed(body.replaceAll(/\r\n?/g, '\n'))
  // A partial last event is deliberately not promoted into a complete event.
  return accumulator.result()
}
