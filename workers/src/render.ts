// Render module — template rendering for strings, entries, and vars.
// Port of render.go's renderString, renderEntry, walkEntry, mergeVars.

import { executeTemplate, type FuncMap } from "./template.ts";
import { buildFuncMap } from "./template-funcs.ts";

let cachedFuncMap: FuncMap | undefined;

function getFuncMap(): FuncMap {
  if (!cachedFuncMap) cachedFuncMap = buildFuncMap();
  return cachedFuncMap;
}

export function renderString(tmpl: string, data: unknown): string {
  return executeTemplate(tmpl, data, getFuncMap());
}

export function renderEntry(
  raw: unknown,
  data: unknown,
): unknown {
  if (raw === null || raw === undefined) return null;
  return walkEntry(raw, data);
}

function walkEntry(v: unknown, data: unknown): unknown {
  if (typeof v === "string") return renderString(v, data);

  if (Array.isArray(v)) {
    return v.map((item) => walkEntry(item, data));
  }

  if (v !== null && typeof v === "object") {
    const obj = v as Record<string, unknown>;
    const out: Record<string, unknown> = {};
    const keys = Object.keys(obj).sort();
    for (const k of keys) {
      out[k] = walkEntry(obj[k], data);
    }
    return out;
  }

  // numbers, booleans, null pass through
  return v;
}

export function renderVars(
  vars: Record<string, unknown> | undefined,
  data: unknown,
): Record<string, unknown> {
  if (!vars || Object.keys(vars).length === 0) return {};
  const rendered = renderEntry(vars, data);
  if (rendered === null || typeof rendered !== "object" || Array.isArray(rendered)) {
    return {};
  }
  return rendered as Record<string, unknown>;
}

export function mergeVars(
  parent: Record<string, unknown> | undefined,
  child: Record<string, unknown> | undefined,
): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  if (parent) Object.assign(out, parent);
  if (child) Object.assign(out, child);
  return out;
}
