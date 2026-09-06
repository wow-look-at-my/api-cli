import { describe, it, expect } from "vitest";
import {
  isTruthy,
  parseInput,
  renderPredicate,
  selectView,
  applyFormat,
  buildFormatContext,
} from "../src/format.ts";
import type { Format, View } from "../src/config.ts";

describe("isTruthy", () => {
  it("treats empty string as falsy", () => {
    expect(isTruthy("")).toBe(false);
    expect(isTruthy("   ")).toBe(false);
  });

  it('treats "false", "0", "no" as falsy (case-insensitive)', () => {
    expect(isTruthy("false")).toBe(false);
    expect(isTruthy("FALSE")).toBe(false);
    expect(isTruthy("False")).toBe(false);
    expect(isTruthy("0")).toBe(false);
    expect(isTruthy("no")).toBe(false);
    expect(isTruthy("NO")).toBe(false);
  });

  it("treats everything else as truthy", () => {
    expect(isTruthy("true")).toBe(true);
    expect(isTruthy("yes")).toBe(true);
    expect(isTruthy("1")).toBe(true);
    expect(isTruthy("anything")).toBe(true);
  });
});

describe("parseInput", () => {
  it("parses JSON", () => {
    expect(parseInput('{"key":"val"}', "json")).toEqual({ key: "val" });
    expect(parseInput("[1,2,3]", "json")).toEqual([1, 2, 3]);
  });

  it("returns string for invalid JSON", () => {
    expect(parseInput("not json", "json")).toBe("not json");
  });

  it("parses lines", () => {
    expect(parseInput("a\nb\nc\n", "lines")).toEqual(["a", "b", "c"]);
  });

  it("returns empty array for empty lines", () => {
    expect(parseInput("", "lines")).toEqual([]);
  });

  it("returns raw string", () => {
    expect(parseInput("hello\n", "raw")).toBe("hello");
  });
});

describe("renderPredicate", () => {
  it("defaults to {{.tty}} when empty", () => {
    const ctx = buildFormatContext(null, {}, true, 80);
    expect(renderPredicate("", ctx)).toBe(true);
  });

  it("evaluates custom predicate", () => {
    const ctx = buildFormatContext([1, 2], {}, true, 80);
    expect(renderPredicate('{{ kindIs "slice" .data }}', ctx)).toBe(true);
    expect(renderPredicate('{{ kindIs "map" .data }}', ctx)).toBe(false);
  });
});

describe("selectView", () => {
  const views: View[] = [
    { name: "table", when: '{{ kindIs "slice" .data }}', template: "TABLE" },
    { name: "detail", default: true, template: "DETAIL" },
  ];

  it("selects by flag name", () => {
    const ctx = buildFormatContext(null, {}, true, 80);
    expect(selectView(views, ctx, "detail").name).toBe("detail");
    expect(selectView(views, ctx, "table").name).toBe("table");
  });

  it("throws for unknown view name", () => {
    const ctx = buildFormatContext(null, {}, true, 80);
    expect(() => selectView(views, ctx, "nonexistent")).toThrow("unknown view");
  });

  it("selects by predicate when no flag", () => {
    const sliceCtx = buildFormatContext([1, 2], {}, true, 80);
    expect(selectView(views, sliceCtx, "").name).toBe("table");

    const objCtx = buildFormatContext({ a: 1 }, {}, true, 80);
    expect(selectView(views, objCtx, "").name).toBe("detail");
  });

  it("falls back to default view", () => {
    const noPredicateViews: View[] = [
      { name: "a", template: "A" },
      { name: "b", default: true, template: "B" },
    ];
    const ctx = buildFormatContext(null, {}, true, 80);
    expect(selectView(noPredicateViews, ctx, "").name).toBe("b");
  });

  it("falls back to first view", () => {
    const noDefaultViews: View[] = [
      { name: "a", template: "A" },
      { name: "b", template: "B" },
    ];
    const ctx = buildFormatContext(null, {}, true, 80);
    expect(selectView(noDefaultViews, ctx, "").name).toBe("a");
  });
});

describe("applyFormat", () => {
  const format: Format = {
    input: "json",
    when: "{{.tty}}",
    views: [
      {
        name: "table",
        when: '{{ kindIs "slice" .data }}',
        template:
          '{{ range .data }}{{ .name }}\n{{ end }}',
      },
      {
        name: "detail",
        default: true,
        template: "Name: {{.data.name}}\n",
      },
    ],
  };

  it("formats JSON object output with detail view", () => {
    const result = applyFormat(
      '{"name":"Alice"}',
      format,
      {},
      "always",
      "",
    );
    expect(result.applied).toBe(true);
    expect(result.formatted).toBe("Name: Alice\n");
  });

  it("formats JSON array output with table view", () => {
    const result = applyFormat(
      '[{"name":"Alice"},{"name":"Bob"}]',
      format,
      {},
      "always",
      "",
    );
    expect(result.applied).toBe(true);
    expect(result.formatted).toContain("Alice");
    expect(result.formatted).toContain("Bob");
  });

  it("skips formatting when mode is raw", () => {
    const result = applyFormat(
      '{"name":"Alice"}',
      format,
      {},
      "raw",
      "",
    );
    expect(result.applied).toBe(false);
    expect(result.formatted).toBe('{"name":"Alice"}');
  });

  it("forces specific view", () => {
    const result = applyFormat(
      '{"name":"Alice"}',
      format,
      {},
      "always",
      "table",
    );
    expect(result.applied).toBe(true);
    // Object data, but table view forced — template may produce empty output
    // since .data is not a slice
  });
});
