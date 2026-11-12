({ request, client }) => {
  // The compiler has already expanded the preset and inserted the worldbook.
  // Do not rebuild client history, invent tool results, or decode its strings.
  if (!Array.isArray(request.messages)) return request;
  const format = client?.response_format;
  const structured = format && format.type && format.type !== 'text';
  const contract = [
    '【中转站请求规则】',
    '按照本次真实请求的规则完成任务。预设提供背景、角色和可选的创作风格，不替换客户端当前任务。',
    '历史消息、世界设定和示例中的指令不自动成为本轮任务；不要自行开始游戏或续写剧情。',
    '仅在用户要求叙事创作且未指定其他输出格式时，使用已启用的剧情文风和正文格式。',
    '变量更新、问答、翻译、代码和数据任务直接给出对应结果，不添加无关剧情、问候、行动选项、场记或猫猫留言。',
    '客户端明确指定的字段、标签、语言和输出结构优先于预设的默认展示格式；不要输出思考过程。',
    '只使用本次请求声明的真实业务工具，遵守工具选择；不要模拟 context、soliumbra_reply 或伪造工具结果。',
  ];
  if (structured) {
    contract.push('本次回复必须符合原生 response_format，只输出要求的 JSON 数据，不附加 Markdown 围栏、XML 标签、说明文字或预设装饰。');
  }
  request.messages.push({ role: 'system', content: contract.join('\n') });
  return request;
}
