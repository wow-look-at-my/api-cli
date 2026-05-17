// Router — resolves URL paths to leaf commands and extracts args/flags.
//
// URL mapping:
//   GET /<cmd>/<subcmd>[/<positional-args>...]?<flags>&<named-args>
//
// Examples:
//   GET /users/get?id=1          → users get --id 1
//   GET /users/get/1             → users get 1
//   GET /users/list?limit=3      → users list --limit 3
//   GET /posts/create?title=hi   → posts create --title hi
//   GET /user-posts?username=Bret → user-posts --username Bret
//
// Special routes:
//   GET /                 → command index (list all leaves)
//   GET /_health          → {"status":"ok"}
//   GET /_schema          → the loaded config as JSON (redacted)
//   GET /_commands         → list of leaf commands with metadata
//
// Special query params (prefixed with _):
//   _format=raw|auto|always  → output format mode
//   _view=<name>             → force a specific view
//
// Response headers:
//   X-API-CLI-Executions   → number of fetch calls made
//   X-API-CLI-Warnings     → JSON array of warnings (unsupported features)

import type { Config, CommandNode, Arg, Flag, Cmd, FormatRef, Format } from "./config.ts";
import { cmdDefined, formatRefDefined } from "./config.ts";
import { mergeVars } from "./render.ts";

export interface ResolvedLeaf {
  path: string;
  node: CommandNode;
  vars: Record<string, unknown>;
  cmdTmpl: Cmd;
  cwdTmpl: string;
  stdinTmpl: string;
  confirmTmpl: string;
  formatRef: FormatRef | undefined;
}

export interface LeafInfo {
  path: string;
  description: string;
  args: Arg[];
  flags: Flag[];
  hasSteps: boolean;
}

export interface ResolvedArgs {
  argMap: Record<string, unknown>;
  flagMap: Record<string, unknown>;
  extraPathArgs: string[];
  formatMode: string;
  viewName: string;
}

// collectLeaves flattens the command tree into resolved leaf commands.
export function collectLeaves(cfg: Config): ResolvedLeaf[] {
  const leaves: ResolvedLeaf[] = [];
  walkCommands(
    cfg.commands ?? [],
    "",
    cfg.vars ?? {},
    cfg.command,
    cfg.cwd ?? "",
    cfg.stdin ?? "",
    "",
    undefined,
    leaves,
  );
  return leaves;
}

function walkCommands(
  cmds: CommandNode[],
  prefix: string,
  vars: Record<string, unknown>,
  cmd: Cmd | undefined,
  cwd: string,
  stdin: string,
  confirm: string,
  formatRef: FormatRef | undefined,
  out: ResolvedLeaf[],
): void {
  for (const c of cmds) {
    const path = prefix ? `${prefix}/${c.name}` : c.name;
    const effVars = mergeVars(vars, c.vars);
    const effCmd = cmdDefined(c.command) ? c.command : cmd;
    const effCwd = c.cwd || cwd;
    const effStdin = c.stdin || stdin;
    const effConfirm = c.confirm || confirm;
    const effFormat = formatRefDefined(c.format) ? c.format : formatRef;

    if (!c.commands?.length) {
      out.push({
        path,
        node: c,
        vars: effVars,
        cmdTmpl: effCmd!,
        cwdTmpl: effCwd,
        stdinTmpl: effStdin,
        confirmTmpl: effConfirm,
        formatRef: effFormat,
      });
    } else {
      walkCommands(
        c.commands,
        path,
        effVars,
        effCmd,
        effCwd,
        effStdin,
        effConfirm,
        effFormat,
        out,
      );
    }
  }
}

// listLeaves returns metadata about all leaf commands (for the index page).
export function listLeaves(cfg: Config): LeafInfo[] {
  return collectLeaves(cfg).map((leaf) => ({
    path: leaf.path,
    description: leaf.node.description ?? "",
    args: leaf.node.args ?? [],
    flags: leaf.node.flags ?? [],
    hasSteps: (leaf.node.steps?.length ?? 0) > 0,
  }));
}

// resolveLeaf finds the leaf command matching a URL path.
// Returns undefined if no match.
export function resolveLeaf(
  leaves: ResolvedLeaf[],
  urlPath: string,
): { leaf: ResolvedLeaf; extraSegments: string[] } | undefined {
  // Normalize path
  let path = urlPath;
  if (path.startsWith("/")) path = path.slice(1);
  if (path.endsWith("/")) path = path.slice(0, -1);
  if (path === "") return undefined;

  const segments = path.split("/");

  // Try progressively shorter prefixes to find a matching leaf,
  // with remaining segments becoming positional args.
  for (let len = segments.length; len >= 1; len--) {
    const candidatePath = segments.slice(0, len).join("/");
    const leaf = leaves.find((l) => l.path === candidatePath);
    if (leaf) {
      return {
        leaf,
        extraSegments: segments.slice(len),
      };
    }
  }

  return undefined;
}

// extractArgs parses query parameters and path segments into typed arg/flag maps.
export function extractArgs(
  leaf: ResolvedLeaf,
  extraSegments: string[],
  searchParams: URLSearchParams,
): ResolvedArgs {
  const args = leaf.node.args ?? [];
  const flags = leaf.node.flags ?? [];

  // Special params
  const formatMode = searchParams.get("_format") ?? "auto";
  const viewName = searchParams.get("_view") ?? "";

  const argMap: Record<string, unknown> = {};
  const flagMap: Record<string, unknown> = {};

  // Fill args from path segments first, then from query params
  let segIdx = 0;
  for (const a of args) {
    const queryVal = searchParams.get(a.name);
    if (a.variadic) {
      // Collect remaining path segments + query param (if array)
      const vals: string[] = [];
      while (segIdx < extraSegments.length) {
        vals.push(extraSegments[segIdx]!);
        segIdx++;
      }
      if (queryVal !== null) {
        vals.push(...searchParams.getAll(a.name));
      } else if (vals.length === 0) {
        // Try query param array
        const all = searchParams.getAll(a.name);
        if (all.length > 0) vals.push(...all);
      }
      if (a.type === "int") {
        argMap[a.name] = vals.map((v) => {
          const n = parseInt(v, 10);
          if (isNaN(n)) throw new Error(`arg "${a.name}": expected integer, got "${v}"`);
          return n;
        });
      } else {
        argMap[a.name] = vals;
      }
      break;
    }

    let val: string | undefined;
    if (segIdx < extraSegments.length) {
      val = extraSegments[segIdx]!;
      segIdx++;
    } else if (queryVal !== null) {
      val = queryVal;
    }

    if (val === undefined) continue;

    if (a.type === "int") {
      const n = parseInt(val, 10);
      if (isNaN(n)) throw new Error(`arg "${a.name}": expected integer, got "${val}"`);
      argMap[a.name] = n;
    } else {
      argMap[a.name] = val;
    }
  }

  // Fill flags from query params
  for (const f of flags) {
    if (f.name.startsWith("_")) continue; // reserved
    const val = searchParams.get(f.name);
    const typ = f.type || "string";

    if (val === null) {
      // Use default
      switch (typ) {
        case "bool":
          flagMap[f.name] = typeof f.default === "boolean" ? f.default : false;
          break;
        case "int": {
          const def = typeof f.default === "number" ? f.default : 0;
          flagMap[f.name] = def;
          break;
        }
        case "string-slice":
          flagMap[f.name] = Array.isArray(f.default) ? f.default : [];
          break;
        default:
          flagMap[f.name] = typeof f.default === "string" ? f.default : "";
      }
      continue;
    }

    switch (typ) {
      case "bool":
        flagMap[f.name] = val === "true" || val === "1" || val === "yes" || val === "";
        break;
      case "int": {
        const n = parseInt(val, 10);
        flagMap[f.name] = isNaN(n) ? 0 : n;
        break;
      }
      case "string-slice":
        flagMap[f.name] = searchParams.getAll(f.name);
        break;
      default:
        flagMap[f.name] = val;
    }
  }

  return { argMap, flagMap, extraPathArgs: extraSegments.slice(segIdx), formatMode, viewName };
}

// validateRequired checks that required args and flags are present.
export function validateRequired(
  leaf: ResolvedLeaf,
  argMap: Record<string, unknown>,
  flagMap: Record<string, unknown>,
): string | null {
  for (const a of leaf.node.args ?? []) {
    if (!a.required) continue;
    const val = argMap[a.name];
    if (val === undefined || val === null) {
      return `required arg "${a.name}" is missing`;
    }
    if (a.variadic && Array.isArray(val) && val.length === 0) {
      return `required variadic arg "${a.name}" needs at least one value`;
    }
  }
  for (const f of leaf.node.flags ?? []) {
    if (!f.required) continue;
    const val = flagMap[f.name];
    if (val === undefined || val === null || val === "") {
      return `required flag "${f.name}" is missing`;
    }
  }
  return null;
}
