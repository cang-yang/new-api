/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
export function summarizeReplayBody(body: unknown, limit = 1600): string {
  if (body == null) return "";
  const text = typeof body === "string" ? body : JSON.stringify(body, null, 2);
  return text.length <= limit ? text : `${text.slice(0, limit)}\n…`;
}
