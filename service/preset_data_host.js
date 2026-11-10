// Data shared by hooks in one request, never a simulated browser or chat store.
// A hook may explicitly carry native protocol messages into its own output.
// Freezing this view does not imply any automatic history repair by the host.
function freezePresetDataView(value) {
  if (value && typeof value === 'object') {
    for (const child of Object.values(value)) freezePresetDataView(child);
    Object.freeze(value);
  }
  return value;
}

function createPresetDataHost(settings, metadata = {}, extra = {}) {
  const context = Object.freeze({
    chatCompletionSettings: freezePresetDataView(settings),
    chatMetadata: metadata,
  });
  const tavern = Object.freeze({...extra, getContext: () => context});
  const window = {
    SillyTavern: tavern,
    get parent() { return window; },
    get top() { return window; },
    get window() { return window; },
    get self() { return window; },
  };
  Object.defineProperties(globalThis, {
    window: {value: window},
    SillyTavern: {value: tavern},
  });
  return () => {
    const text = JSON.stringify(metadata);
    if (!text || text.length > 262144) throw new RangeError('Request metadata limit');
    let bytes = 0;
    for (const char of text) {
      const point = char.codePointAt(0);
      bytes += point <= 127 ? 1 : point <= 2047 ? 2 : point <= 65535 ? 3 : 4;
      if (bytes > 262144) throw new RangeError('Request metadata limit');
    }
    return metadata;
  };
}
