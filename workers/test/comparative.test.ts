// Comparative tests — verifies the TypeScript port produces identical
// results to the Go implementation for shared functionality.
// These tests load the actual example configs from the repo root and
// exercise config parsing, template rendering, router resolution, and
// format system behavior.

import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { loadConfig, validate, type Config } from "../src/config.ts";
import { collectLeaves, listLeaves, resolveLeaf, extractArgs } from "../src/router.ts";
import { renderString, renderEntry, renderVars, mergeVars } from "../src/render.ts";
import { isTruthy, parseInput, selectView, buildFormatContext, applyFormat } from "../src/format.ts";
import { parseCurlCommand, parseCurlArgv } from "../src/curl-parser.ts";
import { analyzeConfig } from "../src/warnings.ts";

const repoRoot = resolve(import.meta.dirname!, "../..");

function loadExample(name: string): Config {
  const json = readFileSync(resolve(repoRoot, name), "utf-8");
  return loadConfig(json);
}

describe("comparative: api.example.json", () => {
  let cfg: Config;

  it("loads and validates without error", () => {
    cfg = loadExample("api.example.json");
    expect(cfg.name).toBe("apicli");
  });

  it("has correct top-level structure", () => {
    expect(cfg.vars).toEqual({ base_url: "https://jsonplaceholder.typicode.com" });
    expect(cfg.command).toBeDefined();
    expect(typeof cfg.command).toBe("string");
  });

  it("has correct number of leaf commands", () => {
    const leaves = collectLeaves(cfg);
    expect(leaves.length).toBe(9);
  });

  it("resolves all leaf paths correctly", () => {
    const leaves = collectLeaves(cfg);
    const paths = leaves.map((l) => l.path).sort();
    expect(paths).toEqual([
      "posts/create",
      "posts/get",
      "posts/list",
      "user-or-id",
      "user-posts",
      "users/batch",
      "users/get",
      "users/list",
      "users/save",
    ]);
  });

  it("resolves users/get with path arg", () => {
    const leaves = collectLeaves(cfg);
    const r = resolveLeaf(leaves, "/users/get/1");
    expect(r).toBeDefined();
    expect(r!.leaf.path).toBe("users/get");
    const args = extractArgs(r!.leaf, r!.extraSegments, new URLSearchParams());
    expect(args.argMap["id"]).toBe(1);
  });

  it("inherits vars down the tree", () => {
    const leaves = collectLeaves(cfg);
    const getUserLeaf = leaves.find((l) => l.path === "users/get")!;
    expect(getUserLeaf.vars["base_url"]).toBe("https://jsonplaceholder.typicode.com");
  });

  it("inherits command from root", () => {
    const leaves = collectLeaves(cfg);
    const getUserLeaf = leaves.find((l) => l.path === "users/get")!;
    // users/get inherits the root command
    expect(typeof getUserLeaf.cmdTmpl).toBe("string");
    expect((getUserLeaf.cmdTmpl as string)).toContain("curl");
  });

  it("overrides command on posts/create", () => {
    const leaves = collectLeaves(cfg);
    const createLeaf = leaves.find((l) => l.path === "posts/create")!;
    expect(Array.isArray(createLeaf.cmdTmpl)).toBe(true);
    expect((createLeaf.cmdTmpl as string[])[0]).toBe("curl");
  });

  it("renders entry templates", () => {
    const entry = renderEntry(
      { path: "/users/{{.arg.id}}" },
      { arg: { id: 42 } },
    );
    expect(entry).toEqual({ path: "/users/42" });
  });

  it("renders querystring from entry", () => {
    const entry = renderEntry(
      { path: "/users", query: { _limit: "{{.flag.limit}}" } },
      { flag: { limit: 3 } },
    );
    expect(entry).toEqual({ path: "/users", query: { _limit: "3" } });

    const qs = renderString("{{querystring .entry.query}}", { entry });
    expect(qs).toBe("?_limit=3");
  });

  it("renders the full root command template for users/get", () => {
    const data = {
      var: { base_url: "https://jsonplaceholder.typicode.com" },
      entry: { path: "/users/1" },
    };
    const rendered = renderString(cfg.command as string, data);
    expect(rendered).toContain("curl");
    expect(rendered).toContain("https://jsonplaceholder.typicode.com/users/1");
  });

  it("parses rendered command as curl", () => {
    const data = {
      var: { base_url: "https://jsonplaceholder.typicode.com" },
      entry: { path: "/users/1" },
    };
    const rendered = renderString(cfg.command as string, data);
    const result = parseCurlCommand(rendered);
    expect(result.error).toBeUndefined();
    expect(result.request!.url).toBe("https://jsonplaceholder.typicode.com/users/1");
    expect(result.request!.method).toBe("GET");
  });

  it("renders argv-form command for posts/create", () => {
    const leaves = collectLeaves(cfg);
    const createLeaf = leaves.find((l) => l.path === "posts/create")!;
    const cmd = createLeaf.cmdTmpl as string[];
    const data = {
      var: { base_url: "https://jsonplaceholder.typicode.com" },
      flag: { title: "hello", body: "world", user: 1 },
    };
    const argv = cmd.map((el) => renderString(el, data));
    expect(argv[0]).toBe("curl");
    expect(argv).toContain("-X");
    expect(argv).toContain("POST");
    const bodyIdx = argv.findIndex((a) => a.startsWith("{"));
    expect(bodyIdx).toBeGreaterThan(-1);
    const body = JSON.parse(argv[bodyIdx]!);
    expect(body.title).toBe("hello");
    expect(body.body).toBe("world");
    expect(body.userId).toBe(1);
  });

  it("formats named reference resolves to user format", () => {
    const leaves = collectLeaves(cfg);
    const getUserLeaf = leaves.find((l) => l.path === "users/get")!;
    expect(getUserLeaf.formatRef).toBe("user");
  });

  it("user format has table and detail views", () => {
    expect(cfg.formats!["user"]).toBeDefined();
    const f = cfg.formats!["user"]!;
    expect(f.views.length).toBe(2);
    expect(f.views[0]!.name).toBe("table");
    expect(f.views[1]!.name).toBe("detail");
  });

  it("table view selects for array data", () => {
    const f = cfg.formats!["user"]!;
    const ctx = buildFormatContext([{ id: 1 }], {}, true, 80);
    const v = selectView(f.views, ctx, "");
    expect(v.name).toBe("table");
  });

  it("detail view selects for object data", () => {
    const f = cfg.formats!["user"]!;
    const ctx = buildFormatContext({ id: 1, name: "Alice" }, {}, true, 80);
    const v = selectView(f.views, ctx, "");
    expect(v.name).toBe("detail");
  });

  it("format applies correctly to user detail", () => {
    const f = cfg.formats!["user"]!;
    const result = applyFormat(
      '{"id":1,"name":"Alice","username":"alice","email":"alice@example.com","phone":"555-1234","website":"alice.com"}',
      f,
      {},
      "always",
      "",
    );
    expect(result.applied).toBe(true);
    expect(result.formatted).toContain("Alice");
    expect(result.formatted).toContain("alice@example.com");
  });

  it("steps work for user-posts (two-step resolution)", () => {
    const leaves = collectLeaves(cfg);
    const leaf = leaves.find((l) => l.path === "user-posts")!;
    expect(leaf.node.steps).toHaveLength(1);
    expect(leaf.node.steps![0]!.name).toBe("user");
  });

  it("conditional steps work for user-or-id", () => {
    const leaves = collectLeaves(cfg);
    const leaf = leaves.find((l) => l.path === "user-or-id")!;
    expect(leaf.node.steps).toHaveLength(1);
    expect(leaf.node.steps![0]!.when).toContain("regexMatch");

    // Numeric ID: step should be skipped
    const whenResult = renderString(leaf.node.steps![0]!.when!, { arg: { id: "42" } });
    expect(isTruthy(whenResult)).toBe(false);

    // Username: step should run
    const whenResult2 = renderString(leaf.node.steps![0]!.when!, { arg: { id: "Bret" } });
    expect(isTruthy(whenResult2)).toBe(true);
  });

  it("analyzeConfig reports warnings", () => {
    const warnings = analyzeConfig(cfg);
    // Example config has no cwd/stdin/confirm, so filesystem warnings
    // come from preconditions only
    const fsWarnings = warnings.filter((w) => w.feature.includes("precondition"));
    expect(fsWarnings.length).toBeGreaterThan(0); // users/save has fileExists
  });
});

describe("comparative: github.example.json", () => {
  let cfg: Config;

  it("loads and validates without error", () => {
    cfg = loadExample("github.example.json");
    expect(cfg.name).toBe("github");
  });

  it("has all expected leaf commands", () => {
    const leaves = collectLeaves(cfg);
    const paths = leaves.map((l) => l.path).sort();
    expect(paths).toContain("user/get");
    expect(paths).toContain("user/repos");
    expect(paths).toContain("repo/get");
    expect(paths).toContain("repo/issues");
    expect(paths).toContain("repo/commits");
    expect(paths).toContain("search/repos");
    expect(paths).toContain("rate-limit");
    expect(paths.length).toBeGreaterThanOrEqual(20);
  });

  it("vars include filter template", () => {
    expect(cfg.vars!["filter"]).toBeDefined();
    expect(String(cfg.vars!["filter"])).toContain("walk");
  });

  it("command template includes curl + jq pipe", () => {
    expect(typeof cfg.command).toBe("string");
    expect(String(cfg.command)).toContain("curl");
    expect(String(cfg.command)).toContain("jq");
  });

  it("renders root command with token", () => {
    const data = {
      var: {
        base_url: "https://api.github.com",
        ua: "test-agent",
        filter: ".",
      },
      env: { GITHUB_TOKEN: "ghp_test123" },
      entry: { path: "/users/octocat" },
    };
    const rendered = renderString(cfg.command as string, data);
    expect(rendered).toContain("Authorization: Bearer ghp_test123");
    expect(rendered).toContain("https://api.github.com/users/octocat");
  });

  it("renders root command without token", () => {
    const data = {
      var: {
        base_url: "https://api.github.com",
        ua: "test-agent",
        filter: ".",
      },
      env: {},
      entry: { path: "/users/octocat" },
    };
    const rendered = renderString(cfg.command as string, data);
    expect(rendered).not.toContain("Authorization");
    expect(rendered).toContain("https://api.github.com/users/octocat");
  });

  it("parses github curl command", () => {
    const data = {
      var: {
        base_url: "https://api.github.com",
        ua: "test-agent",
        filter: ".",
      },
      env: {},
      entry: { path: "/users/octocat" },
    };
    const rendered = renderString(cfg.command as string, data);
    const result = parseCurlCommand(rendered);
    // There's a pipe to jq — should warn
    expect(result.warnings.length).toBeGreaterThan(0);
    expect(result.request).toBeDefined();
    expect(result.request!.url).toContain("api.github.com");
  });

  it("has named formats for various resource types", () => {
    expect(cfg.formats!["user"]).toBeDefined();
    expect(cfg.formats!["repo"]).toBeDefined();
    expect(cfg.formats!["issue"]).toBeDefined();
    expect(cfg.formats!["release"]).toBeDefined();
    expect(cfg.formats!["commit"]).toBeDefined();
    expect(cfg.formats!["branch"]).toBeDefined();
    expect(cfg.formats!["tag"]).toBeDefined();
    expect(cfg.formats!["rate"]).toBeDefined();
  });

  it("search/issues overrides vars.filter to keep repository_url", () => {
    const leaves = collectLeaves(cfg);
    const leaf = leaves.find((l) => l.path === "search/issues")!;
    expect(leaf.vars["filter"]).toBeDefined();
    expect(String(leaf.vars["filter"])).toContain("repository_url");
  });

  it("repo/readme overrides command", () => {
    const leaves = collectLeaves(cfg);
    const leaf = leaves.find((l) => l.path === "repo/readme")!;
    // Has its own command that uses Accept: application/vnd.github.raw
    expect(typeof leaf.cmdTmpl).toBe("string");
    expect(String(leaf.cmdTmpl)).toContain("vnd.github.raw");
  });

  it("user/orgs has inline format", () => {
    const leaves = collectLeaves(cfg);
    const leaf = leaves.find((l) => l.path === "user/orgs")!;
    expect(leaf.formatRef).toBeDefined();
    expect(typeof leaf.formatRef).toBe("object");
  });
});

describe("comparative: isTruthy matches Go", () => {
  // These match the Go isTruthy function in format.go exactly
  const cases: [string, boolean][] = [
    ["", false],
    ["  ", false],
    ["false", false],
    ["False", false],
    ["FALSE", false],
    ["0", false],
    ["no", false],
    ["NO", false],
    ["No", false],
    ["true", true],
    ["1", true],
    ["yes", true],
    ["anything", true],
    ["  true  ", true],
    [" false ", false],
  ];

  for (const [input, expected] of cases) {
    it(`isTruthy("${input}") === ${expected}`, () => {
      expect(isTruthy(input)).toBe(expected);
    });
  }
});

describe("comparative: mergeVars matches Go", () => {
  it("child overrides parent", () => {
    expect(mergeVars({ a: 1, b: 2 }, { b: 3 })).toEqual({ a: 1, b: 3 });
  });

  it("nil parent", () => {
    expect(mergeVars(undefined, { a: 1 })).toEqual({ a: 1 });
  });

  it("nil child", () => {
    expect(mergeVars({ a: 1 }, undefined)).toEqual({ a: 1 });
  });

  it("both nil", () => {
    expect(mergeVars(undefined, undefined)).toEqual({});
  });
});

describe("comparative: parseInput matches Go", () => {
  it("json mode", () => {
    expect(parseInput('{"a":1}', "json")).toEqual({ a: 1 });
    expect(parseInput("[1,2]", "json")).toEqual([1, 2]);
    expect(parseInput("not json", "json")).toBe("not json");
  });

  it("lines mode", () => {
    expect(parseInput("a\nb\n", "lines")).toEqual(["a", "b"]);
    expect(parseInput("", "lines")).toEqual([]);
  });

  it("raw mode", () => {
    expect(parseInput("hello\n", "raw")).toBe("hello");
  });
});
