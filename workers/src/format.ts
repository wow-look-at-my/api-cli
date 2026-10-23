// Format system — port of format.go.
// Handles view selection, predicate evaluation, and output formatting.

import type { Format, View } from "./config.ts";
import { renderString } from "./render.ts";

export function isTruthy(s: string): boolean {
  const t = s.trim();
  if (t === "") return false;
  switch (t.toLowerCase()) {
    case "false":
    case "0":
    case "no":
      return false;
  }
  return true;
}

export function parseInput(s: string, mode: string | undefined): unknown {
  switch (mode) {
    case "lines": {
      const trimmed = s.replace(/\n$/, "");
      if (trimmed === "") return [];
      return trimmed.split("\n");
    }
    case "raw":
      return s.replace(/\n$/, "");
    default: {
      // json
      const t = s.trim();
      if (t === "") return t;
      try {
        return JSON.parse(t);
      } catch {
        return t;
      }
    }
  }
}

export interface FormatContext {
  data: unknown;
  tty: boolean;
  width: number;
  [key: string]: unknown;
}

export function buildFormatContext(
  parsed: unknown,
  data: Record<string, unknown>,
  isTTY: boolean,
  width: number,
): FormatContext {
  const ctx: FormatContext = {
    data: parsed,
    tty: isTTY,
    width,
  };
  for (const [k, v] of Object.entries(data)) {
    ctx[k] = v;
  }
  return ctx;
}

export function renderPredicate(
  tmpl: string | undefined,
  ctx: FormatContext,
): boolean {
  const t = tmpl || "{{.tty}}";
  const out = renderString(t, ctx);
  return isTruthy(out);
}

export function selectView(
  views: View[],
  ctx: FormatContext,
  viewFlag: string,
): View {
  if (viewFlag) {
    const found = views.find((v) => v.name === viewFlag);
    if (!found) throw new Error(`unknown view "${viewFlag}"`);
    return found;
  }

  for (const v of views) {
    if (!v.when) continue;
    if (renderPredicate(v.when, ctx)) return v;
  }

  const defaultView = views.find((v) => v.default);
  if (defaultView) return defaultView;

  return views[0]!;
}

export function applyFormat(
  output: string,
  format: Format,
  data: Record<string, unknown>,
  formatMode: string,
  viewName: string,
): { formatted: string; applied: boolean } {
  // In Workers context: always "tty" when format=always, never when format=raw
  const isTTY = formatMode === "always" || formatMode === "auto";
  const width = 120; // reasonable default for HTTP responses

  if (formatMode === "raw") {
    return { formatted: output, applied: false };
  }

  // Check author predicate
  const preCtx = buildFormatContext(null, data, isTTY, width);
  if (!renderPredicate(format.when, preCtx)) {
    return { formatted: output, applied: false };
  }

  // Parse and format
  const parsed = parseInput(output, format.input);
  const ctx = buildFormatContext(parsed, data, isTTY, width);

  const view = selectView(format.views, ctx, viewName);
  const rendered = renderString(view.template, ctx);
  return { formatted: rendered, applied: true };
}
