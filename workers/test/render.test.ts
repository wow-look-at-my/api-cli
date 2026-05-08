import { describe, it, expect } from "vitest";
import { renderString, renderEntry, renderVars, mergeVars } from "../src/render.ts";

describe("renderString", () => {
  it("renders simple template", () => {
    expect(renderString("hello {{.name}}", { name: "world" })).toBe("hello world");
  });

  it("renders with nested access", () => {
    expect(
      renderString("{{.a.b}}", { a: { b: "deep" } }),
    ).toBe("deep");
  });

  it("renders with functions", () => {
    expect(renderString("{{.name | upper}}", { name: "alice" })).toBe("ALICE");
  });

  it("renders conditional", () => {
    expect(
      renderString("{{if .x}}yes{{else}}no{{end}}", { x: true }),
    ).toBe("yes");
  });
});

describe("renderEntry", () => {
  it("renders string leaves as templates", () => {
    const result = renderEntry(
      { path: "/users/{{.arg.id}}" },
      { arg: { id: 42 } },
    );
    expect(result).toEqual({ path: "/users/42" });
  });

  it("preserves non-string values", () => {
    const result = renderEntry(
      { count: 5, active: true, name: "{{.x}}" },
      { x: "test" },
    );
    expect(result).toEqual({ count: 5, active: true, name: "test" });
  });

  it("handles nested objects", () => {
    const result = renderEntry(
      { query: { userId: "{{.flag.user}}" } },
      { flag: { user: 1 } },
    );
    expect(result).toEqual({ query: { userId: "1" } });
  });

  it("handles arrays", () => {
    const result = renderEntry(
      ["{{.a}}", "{{.b}}"],
      { a: "x", b: "y" },
    );
    expect(result).toEqual(["x", "y"]);
  });

  it("returns null for null/undefined", () => {
    expect(renderEntry(null, {})).toBeNull();
    expect(renderEntry(undefined, {})).toBeNull();
  });
});

describe("renderVars", () => {
  it("renders templated vars", () => {
    const result = renderVars(
      { host: "{{.env.HOST}}" },
      { env: { HOST: "example.com" } },
    );
    expect(result).toEqual({ host: "example.com" });
  });

  it("passes through non-templated vars", () => {
    const result = renderVars(
      { base_url: "https://api.com" },
      {},
    );
    expect(result).toEqual({ base_url: "https://api.com" });
  });

  it("returns empty for undefined/empty vars", () => {
    expect(renderVars(undefined, {})).toEqual({});
    expect(renderVars({}, {})).toEqual({});
  });
});

describe("mergeVars", () => {
  it("merges parent and child, child wins", () => {
    const result = mergeVars(
      { a: 1, b: 2 },
      { b: 3, c: 4 },
    );
    expect(result).toEqual({ a: 1, b: 3, c: 4 });
  });

  it("handles undefined inputs", () => {
    expect(mergeVars(undefined, { a: 1 })).toEqual({ a: 1 });
    expect(mergeVars({ a: 1 }, undefined)).toEqual({ a: 1 });
  });
});
