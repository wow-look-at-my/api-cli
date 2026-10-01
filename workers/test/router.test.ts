import { describe, it, expect } from "vitest";
import type { Config } from "../src/config.ts";
import {
  collectLeaves,
  listLeaves,
  resolveLeaf,
  extractArgs,
  validateRequired,
} from "../src/router.ts";

const testConfig: Config = {
  name: "test",
  vars: { base_url: "https://api.example.com" },
  command: "curl -fsSL {{.var.base_url}}{{.entry.path}}",
  formats: {
    user: {
      views: [{ name: "table", template: "{{.data}}", default: true }],
    },
  },
  commands: [
    {
      name: "users",
      description: "User operations",
      format: "user",
      commands: [
        {
          name: "get",
          description: "Fetch a user by ID",
          args: [{ name: "id", type: "int", required: true }],
          entry: { path: "/users/{{.arg.id}}" },
        },
        {
          name: "list",
          description: "List users",
          flags: [
            { name: "limit", type: "int", default: 10 },
            { name: "pretty", type: "bool", default: true },
          ],
          entry: { path: "/users", query: { _limit: "{{.flag.limit}}" } },
        },
        {
          name: "batch",
          description: "Fetch multiple users",
          args: [
            {
              name: "ids",
              type: "int",
              required: true,
              variadic: true,
            },
          ],
          command: "echo batch",
        },
      ],
    },
    {
      name: "posts",
      description: "Post operations",
      commands: [
        {
          name: "create",
          description: "Create a post",
          flags: [
            { name: "title", type: "string", required: true },
            { name: "body", type: "string", required: true },
            { name: "user", type: "int", default: 1 },
          ],
          command: ["curl", "-X", "POST", "{{.var.base_url}}/posts"],
        },
      ],
    },
    {
      name: "health",
      description: "Health check (conflicting but fine — different from /_health)",
      command: "curl https://api.example.com/health",
    },
  ],
};

describe("collectLeaves", () => {
  it("flattens command tree into leaves", () => {
    const leaves = collectLeaves(testConfig);
    expect(leaves.length).toBe(5);
    expect(leaves.map((l) => l.path)).toEqual([
      "users/get",
      "users/list",
      "users/batch",
      "posts/create",
      "health",
    ]);
  });

  it("inherits vars from ancestors", () => {
    const leaves = collectLeaves(testConfig);
    const getUserLeaf = leaves.find((l) => l.path === "users/get")!;
    expect(getUserLeaf.vars["base_url"]).toBe("https://api.example.com");
  });

  it("inherits command from ancestors", () => {
    const leaves = collectLeaves(testConfig);
    const getUserLeaf = leaves.find((l) => l.path === "users/get")!;
    expect(getUserLeaf.cmdTmpl).toBe(testConfig.command);
  });

  it("overrides command on leaves", () => {
    const leaves = collectLeaves(testConfig);
    const createLeaf = leaves.find((l) => l.path === "posts/create")!;
    expect(createLeaf.cmdTmpl).toEqual(["curl", "-X", "POST", "{{.var.base_url}}/posts"]);
  });

  it("inherits format from group", () => {
    const leaves = collectLeaves(testConfig);
    const getUserLeaf = leaves.find((l) => l.path === "users/get")!;
    expect(getUserLeaf.formatRef).toBe("user");
  });
});

describe("listLeaves", () => {
  it("returns metadata for all leaves", () => {
    const info = listLeaves(testConfig);
    expect(info.length).toBe(5);
    const get = info.find((l) => l.path === "users/get")!;
    expect(get.description).toBe("Fetch a user by ID");
    expect(get.args.length).toBe(1);
    expect(get.args[0]!.name).toBe("id");
  });
});

describe("resolveLeaf", () => {
  const leaves = collectLeaves(testConfig);

  it("resolves exact path", () => {
    const r = resolveLeaf(leaves, "/users/get");
    expect(r).toBeDefined();
    expect(r!.leaf.path).toBe("users/get");
    expect(r!.extraSegments).toEqual([]);
  });

  it("resolves path with positional args", () => {
    const r = resolveLeaf(leaves, "/users/get/42");
    expect(r).toBeDefined();
    expect(r!.leaf.path).toBe("users/get");
    expect(r!.extraSegments).toEqual(["42"]);
  });

  it("resolves top-level leaf", () => {
    const r = resolveLeaf(leaves, "/health");
    expect(r).toBeDefined();
    expect(r!.leaf.path).toBe("health");
  });

  it("returns undefined for non-existent path", () => {
    expect(resolveLeaf(leaves, "/nonexistent")).toBeUndefined();
  });

  it("returns undefined for group path", () => {
    expect(resolveLeaf(leaves, "/users")).toBeUndefined();
  });

  it("handles trailing slash", () => {
    const r = resolveLeaf(leaves, "/users/list/");
    expect(r).toBeDefined();
    expect(r!.leaf.path).toBe("users/list");
  });
});

describe("extractArgs", () => {
  const leaves = collectLeaves(testConfig);

  it("extracts int arg from path segment", () => {
    const leaf = leaves.find((l) => l.path === "users/get")!;
    const result = extractArgs(leaf, ["42"], new URLSearchParams());
    expect(result.argMap["id"]).toBe(42);
  });

  it("extracts int arg from query param", () => {
    const leaf = leaves.find((l) => l.path === "users/get")!;
    const result = extractArgs(leaf, [], new URLSearchParams("id=42"));
    expect(result.argMap["id"]).toBe(42);
  });

  it("path segment takes priority over query", () => {
    const leaf = leaves.find((l) => l.path === "users/get")!;
    const result = extractArgs(leaf, ["42"], new URLSearchParams("id=99"));
    expect(result.argMap["id"]).toBe(42);
  });

  it("extracts flags from query params", () => {
    const leaf = leaves.find((l) => l.path === "users/list")!;
    const result = extractArgs(leaf, [], new URLSearchParams("limit=5"));
    expect(result.flagMap["limit"]).toBe(5);
  });

  it("uses flag defaults", () => {
    const leaf = leaves.find((l) => l.path === "users/list")!;
    const result = extractArgs(leaf, [], new URLSearchParams());
    expect(result.flagMap["limit"]).toBe(10);
    expect(result.flagMap["pretty"]).toBe(true);
  });

  it("parses bool flag from query", () => {
    const leaf = leaves.find((l) => l.path === "users/list")!;
    const result = extractArgs(leaf, [], new URLSearchParams("pretty=false"));
    expect(result.flagMap["pretty"]).toBe(false);
  });

  it("extracts variadic args from path", () => {
    const leaf = leaves.find((l) => l.path === "users/batch")!;
    const result = extractArgs(leaf, ["1", "2", "3"], new URLSearchParams());
    expect(result.argMap["ids"]).toEqual([1, 2, 3]);
  });

  it("extracts _format special param", () => {
    const leaf = leaves.find((l) => l.path === "users/get")!;
    const result = extractArgs(
      leaf,
      ["1"],
      new URLSearchParams("_format=raw"),
    );
    expect(result.formatMode).toBe("raw");
  });

  it("extracts _view special param", () => {
    const leaf = leaves.find((l) => l.path === "users/get")!;
    const result = extractArgs(
      leaf,
      ["1"],
      new URLSearchParams("_view=detail"),
    );
    expect(result.viewName).toBe("detail");
  });

  it("throws on invalid int arg", () => {
    const leaf = leaves.find((l) => l.path === "users/get")!;
    expect(() =>
      extractArgs(leaf, ["notanumber"], new URLSearchParams()),
    ).toThrow("expected integer");
  });
});

describe("validateRequired", () => {
  const leaves = collectLeaves(testConfig);

  it("returns null when all required present", () => {
    const leaf = leaves.find((l) => l.path === "users/get")!;
    expect(validateRequired(leaf, { id: 42 }, {})).toBeNull();
  });

  it("returns error for missing required arg", () => {
    const leaf = leaves.find((l) => l.path === "users/get")!;
    expect(validateRequired(leaf, {}, {})).toContain('required arg "id" is missing');
  });

  it("returns error for missing required flag", () => {
    const leaf = leaves.find((l) => l.path === "posts/create")!;
    expect(validateRequired(leaf, {}, { title: "", body: "x", user: 1 })).toContain(
      'required flag "title" is missing',
    );
  });

  it("returns error for empty required variadic", () => {
    const leaf = leaves.find((l) => l.path === "users/batch")!;
    expect(validateRequired(leaf, { ids: [] }, {})).toContain("needs at least one value");
  });
});
