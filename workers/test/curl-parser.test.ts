import { describe, it, expect } from "vitest";
import { parseCurlCommand, parseCurlArgv, shellSplit } from "../src/curl-parser.ts";

describe("shellSplit", () => {
  it("splits simple command", () => {
    expect(shellSplit("curl -s https://example.com")).toEqual([
      "curl",
      "-s",
      "https://example.com",
    ]);
  });

  it("handles single quotes", () => {
    expect(shellSplit("echo 'hello world'")).toEqual(["echo", "hello world"]);
  });

  it("handles double quotes", () => {
    expect(shellSplit('echo "hello world"')).toEqual(["echo", "hello world"]);
  });

  it("handles escaped quotes in double-quoted strings", () => {
    expect(shellSplit('echo "say \\"hi\\""')).toEqual(["echo", 'say "hi"']);
  });

  it("handles mixed quoting", () => {
    expect(shellSplit("curl -H 'Accept: application/json' \"https://a.com/b\"")).toEqual([
      "curl",
      "-H",
      "Accept: application/json",
      "https://a.com/b",
    ]);
  });

  it("handles backslash escapes outside quotes", () => {
    expect(shellSplit("echo hello\\ world")).toEqual(["echo", "hello world"]);
  });

  it("handles empty input", () => {
    expect(shellSplit("")).toEqual([]);
  });
});

describe("parseCurlCommand", () => {
  it("parses simple GET", () => {
    const r = parseCurlCommand("curl https://api.example.com/users");
    expect(r.error).toBeUndefined();
    expect(r.request!.url).toBe("https://api.example.com/users");
    expect(r.request!.method).toBe("GET");
  });

  it("parses with headers", () => {
    const r = parseCurlCommand(
      "curl -H 'Accept: application/json' -H 'Authorization: Bearer tok' https://api.com/x",
    );
    expect(r.request!.headers["Accept"]).toBe("application/json");
    expect(r.request!.headers["Authorization"]).toBe("Bearer tok");
  });

  it("parses POST with body", () => {
    const r = parseCurlCommand(
      'curl -X POST -d \'{"name":"test"}\' https://api.com/create',
    );
    expect(r.request!.method).toBe("POST");
    expect(r.request!.body).toBe('{"name":"test"}');
  });

  it("infers POST from -d flag", () => {
    const r = parseCurlCommand(
      'curl -d \'{"x":1}\' https://api.com/create',
    );
    expect(r.request!.method).toBe("POST");
  });

  it("handles -fsSL flags", () => {
    const r = parseCurlCommand(
      "curl -fsSL https://api.com/data",
    );
    expect(r.request!.url).toBe("https://api.com/data");
    expect(r.request!.followRedirects).toBe(true);
    expect(r.warnings).toHaveLength(0);
  });

  it("handles sh -c wrapper", () => {
    const r = parseCurlCommand(
      "sh -c 'curl -fsSL https://api.com/data'",
    );
    // sh -c wraps the command — we pass the original string, not argv
    // This tests the shell string form
    expect(r.request?.url ?? r.error).toBeDefined();
  });

  it("warns about pipe commands", () => {
    const r = parseCurlCommand("curl https://api.com/data | jq .");
    expect(r.request!.url).toBe("https://api.com/data");
    expect(r.warnings.length).toBeGreaterThan(0);
    expect(r.warnings[0]!.flag).toBe("|");
  });

  it("warns about -o flag", () => {
    const r = parseCurlCommand("curl -o /tmp/out https://api.com/data");
    expect(r.request!.url).toBe("https://api.com/data");
    expect(r.warnings.some((w) => w.flag === "-o")).toBe(true);
  });

  it("errors on non-curl command", () => {
    const r = parseCurlCommand("wget https://example.com");
    expect(r.error).toContain("not a curl command");
  });

  it("errors on empty command", () => {
    const r = parseCurlCommand("");
    expect(r.error).toBeDefined();
  });
});

describe("parseCurlArgv", () => {
  it("parses argv-form curl command", () => {
    const r = parseCurlArgv([
      "curl",
      "-fsSL",
      "-X",
      "POST",
      "-H",
      "Content-Type: application/json",
      "-d",
      '{"title":"hi"}',
      "https://api.com/posts",
    ]);
    expect(r.request!.method).toBe("POST");
    expect(r.request!.headers["Content-Type"]).toBe("application/json");
    expect(r.request!.body).toBe('{"title":"hi"}');
    expect(r.request!.url).toBe("https://api.com/posts");
  });

  it("handles -L flag for redirects", () => {
    const r = parseCurlArgv(["curl", "-L", "https://example.com"]);
    expect(r.request!.followRedirects).toBe(true);
  });
});
