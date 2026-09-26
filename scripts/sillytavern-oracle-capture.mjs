import http from 'node:http';

const host = '127.0.0.1';
const port = 17899;
const captures = [];
let tag = '';

const respond = (response, status, body) => {
  response.writeHead(status, { 'content-type': 'application/json; charset=utf-8' });
  response.end(JSON.stringify(body));
};

http.createServer(async (request, response) => {
  const pathname = new URL(request.url, `http://${host}:${port}`).pathname;
  if (request.method === 'GET' && pathname === '/capture') {
    return respond(response, 200, captures);
  }
  if (request.method === 'POST' && pathname === '/reset') {
    captures.length = 0;
    return respond(response, 200, { ok: true });
  }
  if (request.method === 'POST' && pathname === '/tag') {
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    tag = Buffer.concat(chunks).toString('utf8').slice(0, 200);
    return respond(response, 200, { tag });
  }
  if (request.method === 'GET' && pathname.endsWith('/models')) {
    return respond(response, 200, { object: 'list', data: [{ id: 'oracle-test', object: 'model' }] });
  }
  if (request.method === 'POST' && pathname.endsWith('/chat/completions')) {
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    let body;
    try {
      body = JSON.parse(Buffer.concat(chunks).toString('utf8'));
    } catch {
      return respond(response, 400, { error: { message: 'Invalid JSON' } });
    }
    captures.push({ tag, body });
    if (body.stream) {
      response.writeHead(200, { 'content-type': 'text/event-stream', 'cache-control': 'no-cache' });
      response.write(`data: ${JSON.stringify({ id: 'oracle-capture', object: 'chat.completion.chunk', choices: [{ index: 0, delta: { role: 'assistant', content: 'OK' }, finish_reason: null }] })}\n\n`);
      response.write('data: [DONE]\n\n');
      return response.end();
    }
    return respond(response, 200, { id: 'oracle-capture', object: 'chat.completion', choices: [{ index: 0, message: { role: 'assistant', content: 'OK' }, finish_reason: 'stop' }] });
  }
  return respond(response, 404, { error: { message: 'Not found' } });
}).listen(port, host, () => process.stdout.write(`Oracle capture at http://${host}:${port}\n`));
