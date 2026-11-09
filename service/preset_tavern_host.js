// Independent, data-only Tavern Helper contract. No DOM or external IO.
let request, currentScript = '', input;
let messages = [], ready = [], diagnostics = [];
let checkMetadata;
const report = globalThis.__tavernDiagnostic;
delete globalThis.__tavernDiagnostic;
let phase = 'import', pendingWork = 0;
const context = () => report(phase, currentScript, 'script-failed');
const listeners = new Map(), scriptData = new Map(), messageData = new Map();
const copy = value => JSON.parse(JSON.stringify(value));
const object = value => value !== null && typeof value === 'object' && !Array.isArray(value);
const reject = capability => {
  report(phase, currentScript, capability);
  if (diagnostics.length < 16) diagnostics.push(true);
  throw new Error('Unavailable gateway capability');
};
const events = Object.freeze({
  GENERATION_STARTED: 'generation_started',
  GENERATION_AFTER_COMMANDS: 'GENERATION_AFTER_COMMANDS',
  CHAT_COMPLETION_PROMPT_READY: 'chat_completion_prompt_ready',
  CHAT_COMPLETION_SETTINGS_READY: 'chat_completion_settings_ready',
  MESSAGE_RECEIVED: 'message_received',
  GENERATION_ENDED: 'generation_ended',
});
const tavern_events = new Proxy(events, {
  get(target, key) {
    if (typeof key === 'symbol') return target[key];
    if (!Object.hasOwn(target, key)) return reject('event:' + key);
    return target[key];
  },
});
function subscribe(event, listener, order = 'normal') {
  if (typeof event !== 'string' || !event || typeof listener !== 'function')
    throw new TypeError('Invalid event listener');
  if (['app_ready', 'chat_id_changed', 'character_message_rendered', 'user_message_rendered',
       'message_edited', 'message_swiped', 'generation_stopped'].includes(event))
    return reject('event:' + event);
  const list = listeners.get(event) || [];
  const existing = list.find(item => item.fn === listener && item.owner === currentScript);
  if (existing && order === 'normal') return {stop: () => remove(event, listener, existing.owner)};
  if (existing) list.splice(list.indexOf(existing), 1);
  if ([...listeners.values()].reduce((n, items) => n + items.length, 0) >= 256)
    throw new RangeError('Listener limit');
  const item = {fn: listener, owner: currentScript, once: order === 'once'};
  if (order === 'first') list.unshift(item); else list.push(item);
  listeners.set(event, list);
  return {stop: () => remove(event, listener, item.owner)};
}
function remove(event, listener, owner = currentScript) {
  listeners.set(event, (listeners.get(event) || []).filter(item => item.fn !== listener || item.owner !== owner));
}
let eventActive = false;
async function emit(event, ...args) {
  // A gateway realm has no per-iframe async context. Reject overlapping and
  // nested dispatch rather than attributing a resumed callback to another script.
  if (eventActive) return reject('concurrent-or-nested-events');
  eventActive = true;
  try {
    for (const item of [...(listeners.get(event) || [])]) {
      if (!(listeners.get(event) || []).includes(item)) continue;
      if (item.once) remove(event, item.fn, item.owner);
      const previous = currentScript;
      const previousPhase = phase;
      currentScript = item.owner;
      phase = Object.values(events).includes(event) ? event : 'custom-event';
      context();
      try {
        await item.fn(...args);
        if (pendingWork) reject('unawaited-async-work');
      } finally { currentScript = previous; phase = previousPhase; }
    }
  } finally { eventActive = false; }
}
function indexOf(value) {
  if (value === 'latest') value = -1;
  const n = Number(value);
  if (!Number.isInteger(n)) throw new TypeError('Invalid message index');
  return n < 0 ? messages.length + n : n;
}
function chatMessage(index) {
  const raw = messages[index];
  if (!raw || typeof raw.content !== 'string' || !['system','user','assistant'].includes(raw.role))
    return reject('text-message-only');
  return {message_id: index, role: raw.role, name: raw.name || (raw.role === 'user' ? input.user : input.char),
    message: raw.content, is_hidden: false, data: copy(messageData.get(index) || {}), extra: {}};
}
function getChatMessages(range, options = {}) {
  if (options.include_swipes) return reject('message-swipes');
  let start, end;
  const value = String(range).replaceAll('{{lastMessageId}}', String(messages.length - 1));
  const bounds = /^(-?\d+)-(-?\d+)$/.exec(value);
  if (bounds) { start = indexOf(bounds[1]); end = indexOf(bounds[2]); }
  else start = end = indexOf(value);
  if (options.hide_state === 'hidden') return [];
  const result = [];
  for (let i = Math.max(0, start); i <= Math.min(end, messages.length - 1); i++) {
    if (options.role && options.role !== 'all' && messages[i].role !== options.role) continue;
    result.push(chatMessage(i));
  }
  return result;
}
async function setChatMessages(changes, options = {}) {
  if (!Array.isArray(changes)) throw new TypeError('Expected message changes');
  if (options.refresh && !['none', 'affected'].includes(options.refresh)) return reject('chat-reload');
  for (const change of changes) {
    const index = indexOf(change.message_id), raw = messages[index];
    if (!raw) throw new RangeError('Message missing');
    for (const key of Object.keys(change)) {
      if (!['message_id','message','role','name','data','is_hidden'].includes(key)) return reject('message-field:' + key);
    }
    if (change.is_hidden) return reject('hidden-messages');
    if ('message' in change) {
      if (typeof change.message !== 'string' || typeof raw.content !== 'string') return reject('text-message-only');
      raw.content = change.message;
    }
    if ('role' in change) {
      if (!['system','user','assistant'].includes(change.role)) throw new TypeError('Invalid message role');
      raw.role = change.role;
    }
    if ('name' in change) {
      if (typeof change.name !== 'string') throw new TypeError('Invalid message name');
      raw.name = change.name;
    }
    if ('data' in change) {
      if (!object(change.data)) throw new TypeError('Invalid message data');
      messageData.set(index, copy(change.data));
    }
  }
}
function variableTable(options = {type:'chat'}) {
  if (!object(options)) throw new TypeError('Invalid variable options');
  const type = options.type ?? 'chat';
  if (type === 'chat' || type === 'global') return [input, type === 'chat' ? 'variables' : 'globalVariables'];
  if (type === 'script') {
    const id = options.script_id || currentScript;
    if (!scriptData.has(id)) throw new RangeError('Unknown script');
    return [scriptData, id];
  }
  if (type === 'message') {
    const id = indexOf(options.message_id ?? 'latest');
    if (!messages[id]) throw new RangeError('Message missing');
    return [messageData, id];
  }
  return reject('variable-scope:' + type);
}
function getVariables(options) {
  const [table,key] = variableTable(options);
  return copy((table instanceof Map ? table.get(key) : table[key]) || {});
}
function replaceVariables(value, options) {
  if (!object(value)) throw new TypeError('Variables must be an object');
  const [table,key] = variableTable(options);
  if (table instanceof Map) table.set(key, copy(value)); else table[key] = copy(value);
}
function updateVariablesWith(updater, options) {
  // Resolve a relative script/message scope before yielding to an async updater.
  const resolved = {...(options || {type:'chat'})};
  if (resolved.type === 'script' && !resolved.script_id) resolved.script_id = currentScript;
  if (resolved.type === 'message') resolved.message_id = indexOf(resolved.message_id ?? 'latest');
  const result = updater(getVariables(resolved));
  if (result && typeof result.then === 'function') {
    pendingWork++;
    return Promise.resolve(result).then(value => {
      replaceVariables(value, resolved); return getVariables(resolved);
    }).finally(() => {pendingWork--;});
  }
  replaceVariables(result, resolved);
  return getVariables(resolved);
}
function mergeVariables(target, source, defaults = false) {
  if (!object(source)) throw new TypeError('Variables must be an object');
  function merge(into, from) {
    for (const key of Object.keys(from)) {
      const value = from[key], present = Object.hasOwn(into, key);
      const previous = present ? into[key] : undefined;
      if ((object(value) && object(previous)) || (Array.isArray(value) && Array.isArray(previous))) {
        merge(previous, value);
      } else if (!defaults || !present) {
        Object.defineProperty(into, key, {value:copy(value),enumerable:true,writable:true,configurable:true});
      }
    }
    return into;
  }
  return merge(target, source);
}
function getAllVariables() {
  const merged = mergeVariables({}, input.globalVariables || {});
  mergeVariables(merged, getVariables({type:'script'}));
  mergeVariables(merged, input.variables || {});
  for (let index = 0; index < messages.length; index++)
    mergeVariables(merged, messageData.get(index) || {});
  return merged;
}
const api = {
  tavern_events,
  eventOn: (event,fn) => subscribe(event,fn),
  eventOnce: (event,fn) => subscribe(event,fn,'once'),
  eventMakeFirst: (event,fn) => subscribe(event,fn,'first'),
  eventMakeLast: (event,fn) => subscribe(event,fn,'last'),
  eventEmit: emit, eventEmitAndWait: emit, eventRemoveListener: remove,
  eventClearEvent: event => { listeners.set(event, (listeners.get(event)||[]).filter(item=>item.owner!==currentScript)); },
  eventClearAll: () => { for(const [event,list] of listeners) listeners.set(event,list.filter(item=>item.owner!==currentScript)); },
  getChatMessages, setChatMessages, getLastMessageId: () => messages.length - 1,
  getScriptId: () => currentScript,
  getVariables, replaceVariables, updateVariablesWith,
  insertOrAssignVariables: (value,options) => updateVariablesWith(v=>mergeVariables(v,value), options),
  insertVariables: (value,options) => updateVariablesWith(v=>mergeVariables(v,value,true), options),
  getAllVariables,
  $: value => { if (typeof value !== 'function') return reject('jquery-dom'); ready.push({fn:value,owner:currentScript}); },
};
Object.assign(globalThis, api);
globalThis.TavernHelper = Object.freeze({...api});
for (const name of ['document', 'localStorage', 'sessionStorage'])
  Object.defineProperty(globalThis, name, {get: () => reject('browser-dom')});
for (const name of ['fetch', 'XMLHttpRequest', 'WebSocket'])
  Object.defineProperty(globalThis, name, {get: () => reject('external-io')});
globalThis.console = Object.freeze({log(){},warn(){},error(){},info(){},debug(){}});
export async function start(serialized) {
  input = JSON.parse(serialized);
  checkMetadata = createPresetDataHost(input.dataHost.settings, input.dataHost.metadata, {
    getCurrentChatId: () => input.chatID, event_types: tavern_events,
  });
  request = input.request;
  messages = request.messages;
  for (const script of input.scripts) scriptData.set(script.id, script.data || {});
  for (const script of input.scripts) {
    currentScript = script.id;
    phase = 'import';
    context();
    await import(script.module);
    if (eventActive) return reject('unawaited-event');
    if (pendingWork) return reject('unawaited-async-work');
    while (ready.length) {
      const item = ready.shift();
      currentScript = item.owner;
      phase = 'ready';
      context();
      await item.fn();
      if (eventActive || pendingWork) return reject('unawaited-async-work');
    }
  }
  await emit(events.GENERATION_STARTED, 'normal', {}, false);
  await emit(events.GENERATION_AFTER_COMMANDS, 'normal', {}, false);
  const prompt = {chat:messages,dryRun:false};
  await emit(events.CHAT_COMPLETION_PROMPT_READY, prompt);
  request.messages = messages = prompt.chat;
  await emit(events.CHAT_COMPLETION_SETTINGS_READY, request);
  messages = request.messages;
  checkMetadata();
  return JSON.stringify({request,diagnostics});
}
export async function finish(serialized) {
  const text = JSON.parse(serialized);
  messages.push({role:'assistant',content:text});
  const id = messages.length - 1;
  await emit(events.MESSAGE_RECEIVED, id, 'normal');
  await emit(events.GENERATION_ENDED, id);
  if (!messages[id] || typeof messages[id].content !== 'string')
    throw new TypeError('Final response must be text');
  checkMetadata();
  return JSON.stringify(messages[id].content);
}
