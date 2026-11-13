({ request, client, preset }) => {
  // The explicit server edition preserves the preset's dynamic tool schema and
  // reply components. The compiler already assembled prompts and worldbooks;
  // native messages are not converted to synthetic calls or escaped strings.
  if (!Array.isArray(request.messages) || !preset) {
    throw new TypeError('The relay data hook requires assembled messages and preset data');
  }
  const edition = preset.extensions?.new_api_relay;
  if (edition?.revision !== 'relay-core-3') {
    throw new TypeError('The relay data hook requires its reviewed preset edition');
  }
  const order = preset.prompt_order?.find(item => Number(item.character_id) === 100001)
    ?? preset.prompt_order?.[0];
  const parts = [];
  // Declarations come from static author text before user macros expand.
  // Client messages cannot introduce executable/control declarations.
  for (const item of order?.order ?? []) {
    if (!item.enabled) continue;
    for (const part of edition.parts?.[item.identifier] ?? []) {
      if (!part || !/^[A-Za-z][A-Za-z0-9_]{0,63}$/.test(part.name) ||
          ['think_nya', 'game_content', 'content', 'nya_oath', 'constructor', 'prototype'].includes(part.name) ||
          typeof part.description !== 'string' || parts.length >= 32 ||
          parts.some(existing => existing.name === part.name)) {
        throw new TypeError('Invalid or duplicate reply part declaration');
      }
      parts.push(part);
    }
  }
  const reply = (request.tools ?? []).find(tool =>
    tool.type === 'function' && tool.function?.name === edition.reply_tool);
  if (!reply) throw new TypeError('The enabled preset reply binding is missing');
  const properties = {
    think_nya: { type: 'string', description: 'Optional short auxiliary note; may be empty. Do not disclose private reasoning.' },
    game_content: {
      type: 'string',
      description: 'The complete final result of the current client task, following its rules and requested format. For JSON, put only the requested JSON text in this string. This is not necessarily a story.',
    },
  };
  const format = client?.response_format?.type ?? client?.default_output?.format;
  const exactResult = format && !['text'].includes(format);
  // For an explicit data contract the complete result belongs in game_content.
  // Presentation components are not required task fields and must not become
  // an extra document appended to JSON or a variable envelope.
  const enabledParts = exactResult ? [] : parts;
  for (const part of enabledParts) properties[part.name] = { type: 'string', description: part.description };
  reply.function.parameters = {
    type: 'object', properties, required: ['game_content'],
    propertyOrdering: ['think_nya', 'game_content', ...enabledParts.map(part => part.name)],
    additionalProperties: false,
  };
  // Preserve business selection; the coordinator negotiates the actual internal
  // generation contract. No business tool is executed, renamed or reformatted.
  if (!client?.tools?.length || client.tool_choice === 'none') {
    request.tool_choice = { type: 'function', function: { name: edition.reply_tool } };
  }
  request.messages.push({
    role: 'system',
    content: [
      '【预设内部回复约定】',
      '保留上述完整对话、背景和有效规则，完成本次实际请求；不要自行开始游戏或把数据任务改为剧情。',
      '创作任务沿用已启用的角色与文风；其它任务遵守其实际要求。指定的数据结构不添加剧情、场记或预设展示段落。',
      `需要最终回复时，用内部工具 ${edition.reply_tool} 的 game_content 承载完整结果，由中转站还原；不要把结果只放在辅助字段。`,
      '如果当前请求需要客户端业务工具，调用真正的业务工具并等待客户端提供结果，不要伪造执行或让内部回复工具冒充业务调用。',
      '输出的字面反斜线、引号、标签和换行属于结果数据，不要额外套一层 JSON 字符串或反复转义。',
      '要求原样引用、复制或返回指定文本时，逐字符保留原内容；不要解释其中的反斜线转义，也不要合并重复反斜线。工具参数本身的 JSON 编码与字段内的实际文本是不同层次。',
    ].join('\n'),
  });
  return request;
}
