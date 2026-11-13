({ calls, client }) => {
  // Preserve declared result components and their stable order, separating
  // auxiliary notes and optional display framing from actual task data.
  if (!Array.isArray(calls) || calls.length !== 1) {
    throw new TypeError('Exactly one complete preset reply is required');
  }
  let args = calls[0]?.arguments;
  if (typeof args === 'string') args = JSON.parse(args);
  if (!args || typeof args !== 'object' || Array.isArray(args) ||
      typeof args.game_content !== 'string' || !args.game_content.trim()) {
    throw new TypeError('The preset reply requires complete final content');
  }
  const format = client?.response_format?.type ?? client?.default_output?.format;
  if (format && format !== 'text') {
    // The request hook declares game_content as the complete task result.
    // Do not append optional presentation fields to a structured result.
    return args.game_content;
  }
  const order = ['game_content', 'game', 'story_body', 'extra', 'options', 'summary', 'comment'];
  const auxiliary = new Set(['think_nya', 'report_nya', 'nya_oath']);
  const keys = [...order, ...Object.keys(args).filter(key => !order.includes(key) && !auxiliary.has(key))];
  const tags = { options: 'options', summary: 'summary', comment: 'comment' };
  const parts = [];
  for (const key of keys) {
    if (!(key in args) || auxiliary.has(key)) continue;
    const value = args[key];
    if (typeof value !== 'string') throw new TypeError('Reply components must be strings');
    if (!value) continue;
    const tag = tags[key];
    // No automatic game wrapper or trim: literal markup, Markdown whitespace,
    // JSON escaping and code indentation are part of the task's result data.
    if (!tag || (value.includes(`<${tag}>`) && value.includes(`</${tag}>`)) ||
        tag === 'comment' && /^<details[\s>]/i.test(value)) {
      parts.push(value);
    } else {
      parts.push(`<${tag}>\n${value}\n</${tag}>`);
    }
  }
  return parts.join('\n');
}
