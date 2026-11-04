// Exec module — renders command templates, parses curl, executes via fetch().
// This is the Workers equivalent of build.go's runLeaf + exec.go's doExec.

import type { Cmd, Format } from "./config.ts";
import { cmdDefined, cmdIsShell, resolveFormat } from "./config.ts";
import type { ResolvedLeaf } from "./router.ts";
import { renderString, renderEntry, renderVars, mergeVars } from "./render.ts";
import { parseCurlCommand, parseCurlArgv, type ParseWarning } from "./curl-parser.ts";
import { applyFormat } from "./format.ts";
import { isTruthy } from "./format.ts";

export interface ExecResult {
  status: number;
  body: string;
  headers: Record<string, string>;
  warnings: string[];
  executions: number;
  contentType: string;
}

// execLeaf runs a resolved leaf command: renders templates, parses curl,
// makes fetch() calls, applies formatting.
export async function execLeaf(
  leaf: ResolvedLeaf,
  argMap: Record<string, unknown>,
  flagMap: Record<string, unknown>,
  env: Record<string, string>,
  formatMode: string,
  viewName: string,
  formats: Record<string, Format> | undefined,
): Promise<ExecResult> {
  const warnings: string[] = [];
  let executions = 0;

  // Warn about unsupported features
  if (leaf.cwdTmpl) {
    warnings.push("cwd: working directory is not supported on Workers (ignored)");
  }
  if (leaf.stdinTmpl) {
    warnings.push("stdin: stdin templates are not supported on Workers (ignored)");
  }
  if (leaf.confirmTmpl) {
    warnings.push("confirm: interactive confirmation is not supported on Workers (skipped)");
  }

  // Build data context (mirrors runLeaf in build.go)
  const preFlagData: Record<string, unknown> = {
    arg: argMap,
    flag: {},
    env,
  };
  const renderedVars = renderVars(leaf.vars, preFlagData);
  preFlagData["var"] = renderedVars;

  // Apply templated flag defaults
  const resolvedFlags = resolveFlags(leaf, flagMap, preFlagData);
  const data: Record<string, unknown> = {
    arg: argMap,
    flag: resolvedFlags,
    env,
    var: renderedVars,
  };

  // Preconditions
  for (const p of leaf.node.preconditions ?? []) {
    const msg = renderString(p, data).trim();
    if (msg) {
      return {
        status: 400,
        body: JSON.stringify({ error: msg }),
        headers: {},
        warnings,
        executions: 0,
        contentType: "application/json",
      };
    }
  }

  // Steps
  const resultMap: Record<string, unknown> = {};
  data["result"] = resultMap;

  for (const step of leaf.node.steps ?? []) {
    // Conditional step
    if (step.when) {
      const whenOut = renderString(step.when, data);
      if (!isTruthy(whenOut)) continue;
    }

    const stepCmd = cmdDefined(step.command) ? step.command : leaf.cmdTmpl;
    if (!cmdDefined(stepCmd)) {
      return {
        status: 500,
        body: JSON.stringify({ error: `step "${step.name}": no command available` }),
        headers: {},
        warnings,
        executions,
        contentType: "application/json",
      };
    }

    // Render step entry
    const stepEntry = renderEntry(step.entry, data) ?? {};
    data["entry"] = stepEntry;

    if (step.cwd) {
      warnings.push(`step "${step.name}": cwd not supported on Workers`);
    }
    if (step.stdin) {
      warnings.push(`step "${step.name}": stdin not supported on Workers`);
    }

    // Execute step
    const { response, stepWarnings } = await execCommand(stepCmd, data);
    executions++;
    warnings.push(...stepWarnings);

    if (!response.ok) {
      const errBody = await response.text();
      return {
        status: response.status,
        body: errBody,
        headers: {},
        warnings,
        executions,
        contentType: "text/plain",
      };
    }

    const text = await response.text();
    resultMap[step.name] = parseResult(text);
  }

  // Render leaf entry
  const entry = renderEntry(leaf.node.entry, data) ?? {};
  data["entry"] = entry;

  if (!cmdDefined(leaf.cmdTmpl)) {
    return {
      status: 500,
      body: JSON.stringify({ error: "no command available to run" }),
      headers: {},
      warnings,
      executions,
      contentType: "application/json",
    };
  }

  // Execute leaf command
  const { response: leafResp, stepWarnings: leafWarnings } = await execCommand(
    leaf.cmdTmpl,
    data,
  );
  executions++;
  warnings.push(...leafWarnings);

  const output = await leafResp.text();

  if (!leafResp.ok) {
    return {
      status: leafResp.status,
      body: output,
      headers: {},
      warnings,
      executions,
      contentType: "text/plain",
    };
  }

  // Apply formatting
  const effFormat = resolveFormat(leaf.formatRef, formats);
  if (effFormat && formatMode !== "raw") {
    try {
      const { formatted, applied } = applyFormat(
        output,
        effFormat,
        data,
        formatMode,
        viewName,
      );
      if (applied) {
        return {
          status: 200,
          body: formatted,
          headers: {},
          warnings,
          executions,
          contentType: "text/plain",
        };
      }
    } catch (e) {
      warnings.push(`format error: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  // Detect content type from output
  const ct = looksLikeJSON(output) ? "application/json" : "text/plain";

  return {
    status: 200,
    body: output,
    headers: {},
    warnings,
    executions,
    contentType: ct,
  };
}

function resolveFlags(
  leaf: ResolvedLeaf,
  flagMap: Record<string, unknown>,
  preFlagData: unknown,
): Record<string, unknown> {
  const out: Record<string, unknown> = { ...flagMap };
  for (const f of leaf.node.flags ?? []) {
    if (out[f.name] !== undefined && out[f.name] !== "") continue;
    if (typeof f.default === "string" && f.default.includes("{{")) {
      out[f.name] = renderString(f.default, preFlagData);
    }
  }
  return out;
}

async function execCommand(
  cmd: Cmd,
  data: unknown,
): Promise<{ response: Response; stepWarnings: string[] }> {
  const warnings: string[] = [];

  let parseResult;
  if (cmdIsShell(cmd)) {
    const rendered = renderString(cmd, data);
    parseResult = parseCurlCommand(rendered);
  } else {
    const argv = (cmd as string[]).map((el) => renderString(el, data));
    // Filter out spread sentinels
    const cleaned: string[] = [];
    for (const el of argv) {
      if (el.startsWith("\x00")) {
        const rest = el.slice(1);
        if (rest) cleaned.push(...rest.split("\x00"));
      } else {
        cleaned.push(el);
      }
    }
    parseResult = parseCurlArgv(cleaned);
  }

  for (const w of parseResult.warnings) {
    warnings.push(`${w.flag}: ${w.message}`);
  }

  if (parseResult.error || !parseResult.request) {
    return {
      response: new Response(
        JSON.stringify({ error: parseResult.error ?? "failed to parse command" }),
        { status: 502 },
      ),
      stepWarnings: warnings,
    };
  }

  const req = parseResult.request;
  const init: RequestInit = {
    method: req.method,
    headers: req.headers,
    redirect: req.followRedirects ? "follow" : "manual",
  };
  if (req.body !== undefined && req.method !== "GET" && req.method !== "HEAD") {
    init.body = req.body;
  }

  try {
    const response = await fetch(req.url, init);
    return { response, stepWarnings: warnings };
  } catch (e) {
    return {
      response: new Response(
        JSON.stringify({
          error: `fetch failed: ${e instanceof Error ? e.message : String(e)}`,
        }),
        { status: 502 },
      ),
      stepWarnings: warnings,
    };
  }
}

function parseResult(s: string): unknown {
  const trimmed = s.trim();
  if (!trimmed) return trimmed;
  try {
    return JSON.parse(trimmed);
  } catch {
    return trimmed;
  }
}

function looksLikeJSON(s: string): boolean {
  const t = s.trim();
  return (t.startsWith("{") && t.endsWith("}")) ||
         (t.startsWith("[") && t.endsWith("]"));
}
