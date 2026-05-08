// Cloudflare Workers entry point for api-cli.
//
// Loads a JSON config (from environment binding or inline), builds the
// command tree, and serves each leaf command as an HTTP endpoint.
//
// URL mapping:
//   GET /                       → index of all commands
//   GET /_health                → {"status":"ok"}
//   GET /_commands              → JSON list of leaf commands with metadata
//   GET /_warnings              → config compatibility warnings
//   GET /<path>[/args...]?flags → execute leaf command via fetch()
//
// Config is provided via:
//   - API_CLI_CONFIG env binding (JSON string)
//   - Or a KV namespace binding CONFIG_KV with key "config"

import { loadConfig, type Config, type Format } from "./config.ts";
import { collectLeaves, listLeaves, resolveLeaf, extractArgs, validateRequired, type ResolvedLeaf } from "./router.ts";
import { execLeaf } from "./exec.ts";
import { analyzeConfig } from "./warnings.ts";

export interface Env {
  API_CLI_CONFIG?: string;
  CONFIG_KV?: KVNamespace;
  [key: string]: unknown;
}

// Cached state per isolate
let cachedConfig: Config | undefined;
let cachedLeaves: ResolvedLeaf[] | undefined;

async function getConfig(env: Env): Promise<Config> {
  if (cachedConfig) return cachedConfig;

  let json: string | undefined;

  if (env.API_CLI_CONFIG) {
    json = env.API_CLI_CONFIG;
  } else if (env.CONFIG_KV) {
    json = (await env.CONFIG_KV.get("config")) ?? undefined;
  }

  if (!json) {
    throw new Error("no config: set API_CLI_CONFIG env var or CONFIG_KV binding");
  }

  cachedConfig = loadConfig(json);
  cachedLeaves = collectLeaves(cachedConfig);
  return cachedConfig;
}

function getLeaves(): ResolvedLeaf[] {
  if (!cachedLeaves) throw new Error("config not loaded");
  return cachedLeaves;
}

function envFromBindings(env: Env): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(env)) {
    if (typeof v === "string" && k !== "API_CLI_CONFIG") {
      out[k] = v;
    }
  }
  return out;
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);
    const path = url.pathname;

    // Health check — no config needed
    if (path === "/_health") {
      return json({ status: "ok" });
    }

    // Load config
    let cfg: Config;
    try {
      cfg = await getConfig(env);
    } catch (e) {
      return json(
        { error: e instanceof Error ? e.message : "failed to load config" },
        502,
      );
    }

    // Index
    if (path === "/" || path === "") {
      const leaves = listLeaves(cfg);
      return json({
        name: cfg.name,
        description: cfg.description ?? "",
        endpoints: leaves.map((l) => ({
          path: `/${l.path}`,
          description: l.description,
          args: l.args.map((a) => ({
            name: a.name,
            type: a.type || "string",
            required: a.required ?? false,
            variadic: a.variadic ?? false,
            description: a.description ?? "",
          })),
          flags: l.flags.map((f) => ({
            name: f.name,
            type: f.type || "string",
            default: f.default,
            required: f.required ?? false,
            description: f.description ?? "",
          })),
          hasSteps: l.hasSteps,
        })),
      });
    }

    // Command list
    if (path === "/_commands") {
      return json(listLeaves(cfg));
    }

    // Warnings
    if (path === "/_warnings") {
      return json(analyzeConfig(cfg));
    }

    // Resolve command
    const leaves = getLeaves();
    const resolved = resolveLeaf(leaves, path);
    if (!resolved) {
      // Try to find group commands to show help
      const prefix = path.startsWith("/") ? path.slice(1) : path;
      const matching = leaves.filter((l) => l.path.startsWith(prefix + "/"));
      if (matching.length > 0) {
        return json({
          error: `"${prefix}" is a command group, not a leaf command`,
          available: matching.map((l) => ({
            path: `/${l.path}`,
            description: l.node.description ?? "",
          })),
        }, 404);
      }
      return json({ error: `no command matches path "${path}"` }, 404);
    }

    const { leaf, extraSegments } = resolved;

    // Extract args and flags
    let resolvedArgs;
    try {
      resolvedArgs = extractArgs(leaf, extraSegments, url.searchParams);
    } catch (e) {
      return json(
        { error: e instanceof Error ? e.message : "invalid arguments" },
        400,
      );
    }

    // Validate required
    const validationErr = validateRequired(
      leaf,
      resolvedArgs.argMap,
      resolvedArgs.flagMap,
    );
    if (validationErr) {
      return json({ error: validationErr }, 400);
    }

    // Execute
    try {
      const result = await execLeaf(
        leaf,
        resolvedArgs.argMap,
        resolvedArgs.flagMap,
        envFromBindings(env),
        resolvedArgs.formatMode,
        resolvedArgs.viewName,
        cfg.formats,
      );

      const headers = new Headers();
      headers.set("Content-Type", result.contentType);
      headers.set("X-API-CLI-Executions", String(result.executions));
      if (result.warnings.length > 0) {
        headers.set("X-API-CLI-Warnings", JSON.stringify(result.warnings));
      }

      return new Response(result.body, {
        status: result.status,
        headers,
      });
    } catch (e) {
      return json(
        {
          error: "execution failed",
          detail: e instanceof Error ? e.message : String(e),
        },
        500,
      );
    }
  },
};

function json(data: unknown, status = 200): Response {
  return new Response(JSON.stringify(data, null, 2) + "\n", {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
