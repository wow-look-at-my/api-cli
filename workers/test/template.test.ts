import { describe, it, expect } from "vitest";
import { executeTemplate, type FuncMap } from "../src/template.ts";
import { buildFuncMap } from "../src/template-funcs.ts";

const funcs = buildFuncMap();

function render(tmpl: string, data: unknown = {}, extra?: FuncMap): string {
  return executeTemplate(tmpl, data, { ...funcs, ...extra });
}

describe("template engine", () => {
  describe("text passthrough", () => {
    it("returns plain text unchanged", () => {
      expect(render("hello world")).toBe("hello world");
    });

    it("handles empty template", () => {
      expect(render("")).toBe("");
    });
  });

  describe("dot access", () => {
    it("accesses top-level fields", () => {
      expect(render("{{.name}}", { name: "Alice" })).toBe("Alice");
    });

    it("accesses nested fields", () => {
      expect(render("{{.a.b.c}}", { a: { b: { c: 42 } } })).toBe("42");
    });

    it("returns empty for missing keys (missingkey=zero)", () => {
      expect(render("{{.missing}}", {})).toBe("");
    });

    it("returns empty for deeply missing keys", () => {
      expect(render("{{.a.b.c}}", { a: {} })).toBe("");
    });

    it("accesses bare dot", () => {
      expect(render("{{.}}", "hello")).toBe("hello");
    });
  });

  describe("if/else", () => {
    it("renders truthy branch", () => {
      expect(render("{{if .x}}yes{{end}}", { x: true })).toBe("yes");
    });

    it("renders falsy branch", () => {
      expect(render("{{if .x}}yes{{else}}no{{end}}", { x: false })).toBe("no");
    });

    it("treats empty string as falsy", () => {
      expect(render("{{if .x}}yes{{else}}no{{end}}", { x: "" })).toBe("no");
    });

    it("treats zero as falsy", () => {
      expect(render("{{if .x}}yes{{else}}no{{end}}", { x: 0 })).toBe("no");
    });

    it("treats non-empty arrays as truthy", () => {
      expect(render("{{if .x}}yes{{end}}", { x: [1] })).toBe("yes");
    });

    it("treats empty arrays as falsy", () => {
      expect(render("{{if .x}}yes{{else}}no{{end}}", { x: [] })).toBe("no");
    });

    it("supports else if", () => {
      expect(
        render("{{if .a}}A{{else if .b}}B{{else}}C{{end}}", {
          a: false,
          b: true,
        }),
      ).toBe("B");
    });
  });

  describe("range", () => {
    it("iterates over arrays", () => {
      expect(render("{{range .items}}[{{.}}]{{end}}", { items: [1, 2, 3] })).toBe(
        "[1][2][3]",
      );
    });

    it("iterates with index variable", () => {
      expect(
        render(
          "{{range $i, $v := .items}}{{$i}}:{{$v}} {{end}}",
          { items: ["a", "b"] },
        ),
      ).toBe("0:a 1:b ");
    });

    it("renders else for empty", () => {
      expect(
        render("{{range .items}}x{{else}}empty{{end}}", { items: [] }),
      ).toBe("empty");
    });

    it("renders else for nil", () => {
      expect(
        render("{{range .items}}x{{else}}empty{{end}}", {}),
      ).toBe("empty");
    });

    it("iterates over maps", () => {
      const result = render(
        "{{range $k, $v := .m}}{{$k}}={{$v}} {{end}}",
        { m: { a: 1, b: 2 } },
      );
      expect(result).toContain("a=1");
      expect(result).toContain("b=2");
    });
  });

  describe("with", () => {
    it("sets dot to value when truthy", () => {
      expect(render("{{with .x}}[{{.}}]{{end}}", { x: "hi" })).toBe("[hi]");
    });

    it("skips when falsy", () => {
      expect(render("{{with .x}}yes{{else}}no{{end}}", { x: null })).toBe("no");
    });
  });

  describe("variables", () => {
    it("assigns and reads variables", () => {
      expect(render('{{$x := "hello"}}{{$x}}')).toBe("hello");
    });

    it("reassigns variables", () => {
      expect(render('{{$x := "a"}}{{$x = "b"}}{{$x}}')).toBe("b");
    });
  });

  describe("pipelines", () => {
    it("pipes value to function", () => {
      expect(render('{{.name | upper}}', { name: "alice" })).toBe("ALICE");
    });

    it("chains multiple pipes", () => {
      expect(render('{{"  hello  " | trim | upper}}')).toBe("HELLO");
    });
  });

  describe("whitespace trimming", () => {
    it("trims left with {{-", () => {
      expect(render("hello   {{- .x}}", { x: "world" })).toBe("helloworld");
    });

    it("trims right with -}}", () => {
      expect(render("{{.x -}}   world", { x: "hello" })).toBe("helloworld");
    });

    it("trims both", () => {
      expect(render("a  {{- .x -}}  b", { x: "X" })).toBe("aXb");
    });
  });

  describe("function calls", () => {
    it("calls functions with arguments", () => {
      expect(render('{{printf "%s=%d" "x" 42}}')).toBe("x=42");
    });

    it("calls with parenthesized subexpressions", () => {
      expect(
        render('{{printf "%v" (add 1 2)}}'),
      ).toBe("3");
    });
  });

  describe("string literals", () => {
    it("handles double-quoted strings", () => {
      expect(render('{{"hello"}}')).toBe("hello");
    });

    it("handles escape sequences", () => {
      expect(render('{{"line1\\nline2"}}')).toBe("line1\nline2");
    });

    it("handles backtick strings", () => {
      expect(render("{{`raw string`}}")).toBe("raw string");
    });
  });
});

describe("template functions", () => {
  describe("sprig compatibility", () => {
    it("default", () => {
      expect(render('{{default "fallback" .x}}', {})).toBe("fallback");
      expect(render('{{default "fallback" .x}}', { x: "real" })).toBe("real");
    });

    it("toJson", () => {
      expect(render("{{toJson .x}}", { x: { a: 1 } })).toBe('{"a":1}');
    });

    it("trim", () => {
      expect(render('{{"  hello  " | trim}}')).toBe("hello");
    });

    it("trimSuffix", () => {
      expect(render('{{trimSuffix ".tar.gz" "foo.tar.gz"}}')).toBe("foo");
    });

    it("trimPrefix", () => {
      expect(render('{{trimPrefix "pre-" "pre-value"}}')).toBe("value");
    });

    it("upper/lower", () => {
      expect(render('{{"hello" | upper}}')).toBe("HELLO");
      expect(render('{{"HELLO" | lower}}')).toBe("hello");
    });

    it("contains", () => {
      expect(render('{{contains "ell" "hello"}}')).toBe("true");
    });

    it("hasPrefix/hasSuffix", () => {
      expect(render('{{hasPrefix "he" "hello"}}')).toBe("true");
      expect(render('{{hasSuffix "lo" "hello"}}')).toBe("true");
    });

    it("replace", () => {
      expect(render('{{replace "o" "0" "hello world"}}')).toBe("hell0 w0rld");
    });

    it("substr", () => {
      expect(render('{{substr 0 5 "hello world"}}')).toBe("hello");
    });

    it("b64enc/b64dec", () => {
      expect(render('{{b64enc "hello"}}')).toBe("aGVsbG8=");
      expect(render('{{b64dec "aGVsbG8="}}')).toBe("hello");
    });

    it("regexMatch", () => {
      expect(render('{{regexMatch "^[0-9]+$" "123"}}')).toBe("true");
      expect(render('{{regexMatch "^[0-9]+$" "abc"}}')).toBe("false");
    });

    it("regexReplaceAll", () => {
      expect(render('{{regexReplaceAll "\\\\n.*" "line1\\nline2" ""}}')).toBe("line1");
    });

    it("kindIs", () => {
      expect(render('{{kindIs "slice" .x}}', { x: [1, 2] })).toBe("true");
      expect(render('{{kindIs "map" .x}}', { x: { a: 1 } })).toBe("true");
      expect(render('{{kindIs "string" .x}}', { x: "hi" })).toBe("true");
    });

    it("hasKey", () => {
      expect(render('{{hasKey .x "a"}}', { x: { a: 1 } })).toBe("true");
      expect(render('{{hasKey .x "z"}}', { x: { a: 1 } })).toBe("false");
    });

    it("list", () => {
      expect(render('{{len (list 1 2 3)}}')).toBe("3");
    });

    it("append", () => {
      expect(render('{{$l := list "a" "b"}}{{$l = append $l "c"}}{{len $l}}')).toBe("3");
    });

    it("add/mul/sub/div", () => {
      expect(render("{{add 1 2 3}}")).toBe("6");
      expect(render("{{mul 2 3}}")).toBe("6");
      expect(render("{{sub 10 3}}")).toBe("7");
      expect(render("{{div 10 3}}")).toBe("3");
    });

    it("int", () => {
      expect(render("{{int 3.7}}")).toBe("3");
    });

    it("divf/mulf", () => {
      expect(render("{{divf 10 3}}")).toMatch(/3\.333/);
      expect(render("{{mulf 100.0 0.5}}")).toBe("50");
    });

    it("eq/ne/lt/gt", () => {
      expect(render('{{eq 1 1}}')).toBe("true");
      expect(render('{{ne 1 2}}')).toBe("true");
      expect(render('{{lt 1 2}}')).toBe("true");
      expect(render('{{gt 2 1}}')).toBe("true");
    });

    it("not", () => {
      expect(render("{{not false}}")).toBe("true");
      expect(render("{{not true}}")).toBe("false");
    });

    it("and/or", () => {
      expect(render('{{and true true}}')).toBe("true");
      expect(render('{{or false true}}')).toBe("true");
    });

    it("index", () => {
      expect(render('{{index .x 1}}', { x: ["a", "b", "c"] })).toBe("b");
      expect(render('{{index .x "key"}}', { x: { key: "val" } })).toBe("val");
    });

    it("len", () => {
      expect(render("{{len .x}}", { x: [1, 2, 3] })).toBe("3");
      expect(render('{{len .x}}', { x: "hello" })).toBe("5");
    });

    it("join", () => {
      expect(render('{{join ", " .x}}', { x: ["a", "b", "c"] })).toBe("a, b, c");
    });

    it("split", () => {
      expect(render('{{len (split "," "a,b,c")}}')).toBe("3");
    });
  });

  describe("api-cli custom helpers", () => {
    it("querystring with map", () => {
      expect(render('{{querystring .q}}', { q: { a: "1", b: "2" } })).toContain("?");
      expect(render('{{querystring .q}}', { q: { a: "1", b: "2" } })).toContain("a=1");
      expect(render('{{querystring .q}}', { q: { a: "1", b: "2" } })).toContain("b=2");
    });

    it("querystring drops empty values", () => {
      expect(render('{{querystring .q}}', { q: { a: "", b: "2" } })).toBe("?b=2");
    });

    it("querystring returns empty for nil", () => {
      expect(render('{{querystring .q}}', {})).toBe("");
    });

    it("shellquote", () => {
      expect(render('{{shellquote "hello world"}}')).toBe("'hello world'");
      expect(render("{{shellquote \"it's\"}}")).toBe("'it'\\''s'");
    });

    it("urlpath", () => {
      expect(render('{{urlpath "hello world"}}')).toBe("hello%20world");
      expect(render('{{urlpath "a/b"}}')).toBe("a%2Fb");
    });

    it("fileExists always returns false on Workers", () => {
      expect(render('{{fileExists "/tmp/x"}}')).toBe("false");
    });

    it("dirExists always returns false on Workers", () => {
      expect(render('{{dirExists "/tmp"}}')).toBe("false");
    });
  });
});
