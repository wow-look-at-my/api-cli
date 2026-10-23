import { describe, it, expect } from "vitest";
import { validate, loadConfig, cmdDefined, formatRefDefined, type Config } from "../src/config.ts";

describe("config validation", () => {
  it("requires top-level name", () => {
    expect(validate({ name: "" } as Config)).toBe('top-level "name" is required');
    expect(validate({ name: "  " } as Config)).toBe('top-level "name" is required');
  });

  it("accepts minimal config", () => {
    expect(
      validate({
        name: "test",
        command: "echo hi",
        commands: [{ name: "run" }],
      } as Config),
    ).toBeNull();
  });

  it("rejects reserved command names", () => {
    const err = validate({
      name: "t",
      command: "echo",
      commands: [{ name: "help" }],
    } as Config);
    expect(err).toContain("reserved");
  });

  it("rejects duplicate sibling names", () => {
    const err = validate({
      name: "t",
      command: "echo",
      commands: [{ name: "a" }, { name: "a" }],
    } as Config);
    expect(err).toContain("duplicate command name");
  });

  it("rejects whitespace in names", () => {
    const err = validate({
      name: "t",
      command: "echo",
      commands: [{ name: "has space" }],
    } as Config);
    expect(err).toContain("whitespace");
  });

  it("rejects leaf without command", () => {
    const err = validate({
      name: "t",
      commands: [{ name: "leaf" }],
    } as Config);
    expect(err).toContain("no command and no ancestor");
  });

  it("validates arg types", () => {
    const err = validate({
      name: "t",
      command: "echo",
      commands: [
        { name: "run", args: [{ name: "x", type: "badtype" as "string" }] },
      ],
    } as Config);
    expect(err).toContain("must be one of string|int");
  });

  it("validates flag types", () => {
    const err = validate({
      name: "t",
      command: "echo",
      commands: [
        { name: "run", flags: [{ name: "x", type: "badtype" as "string" }] },
      ],
    } as Config);
    expect(err).toContain("must be one of string|bool|int|string-slice");
  });

  it("rejects flag names starting with no-", () => {
    const err = validate({
      name: "t",
      command: "echo",
      commands: [{ name: "run", flags: [{ name: "no-thing" }] }],
    } as Config);
    expect(err).toContain('cannot start with "no-"');
  });

  it("rejects variadic arg not last", () => {
    const err = validate({
      name: "t",
      command: "echo",
      commands: [
        {
          name: "run",
          args: [
            { name: "a", variadic: true },
            { name: "b" },
          ],
        },
      ],
    } as Config);
    expect(err).toContain("must be the last arg");
  });

  it("rejects required arg after optional", () => {
    const err = validate({
      name: "t",
      command: "echo",
      commands: [
        {
          name: "run",
          args: [
            { name: "a" },
            { name: "b", required: true },
          ],
        },
      ],
    } as Config);
    expect(err).toContain("cannot follow an optional arg");
  });

  it("rejects entry on group nodes", () => {
    const err = validate({
      name: "t",
      command: "echo",
      commands: [
        {
          name: "group",
          entry: { x: 1 },
          commands: [{ name: "leaf" }],
        },
      ],
    } as Config);
    expect(err).toContain("only allowed on leaves");
  });

  it("rejects steps on group nodes", () => {
    const err = validate({
      name: "t",
      command: "echo",
      commands: [
        {
          name: "group",
          steps: [{ name: "s" }],
          commands: [{ name: "leaf" }],
        },
      ],
    } as Config);
    expect(err).toContain("only allowed on leaves");
  });

  it("validates format references", () => {
    const err = validate({
      name: "t",
      command: "echo",
      commands: [{ name: "run", format: "nonexistent" }],
    } as Config);
    expect(err).toContain('references unknown format "nonexistent"');
  });

  it("validates inline formats", () => {
    const err = validate({
      name: "t",
      command: "echo",
      commands: [
        {
          name: "run",
          format: { views: [] },
        },
      ],
    } as Config);
    expect(err).toContain("at least one view is required");
  });

  it("validates named formats", () => {
    const err = validate({
      name: "t",
      command: "echo",
      formats: { "f": { input: "badinput" as "json", views: [{ name: "v", template: "t" }] } },
      commands: [{ name: "run" }],
    } as Config);
    expect(err).toContain("must be one of json|lines|raw");
  });

  it("validates step names", () => {
    const err = validate({
      name: "t",
      command: "echo",
      commands: [
        {
          name: "run",
          steps: [
            { name: "s" },
            { name: "s" },
          ],
        },
      ],
    } as Config);
    expect(err).toContain('duplicate step name "s"');
  });

  it("rejects flag conflicting with itself", () => {
    const err = validate({
      name: "t",
      command: "echo",
      commands: [
        {
          name: "run",
          flags: [{ name: "a", conflicts: ["a"] }],
        },
      ],
    } as Config);
    expect(err).toContain("conflicts with itself");
  });

  it("rejects flag conflicting with unknown peer", () => {
    const err = validate({
      name: "t",
      command: "echo",
      commands: [
        {
          name: "run",
          flags: [{ name: "a", conflicts: ["z"] }],
        },
      ],
    } as Config);
    expect(err).toContain('conflicts with unknown flag "z"');
  });

  it("loadConfig parses and validates", () => {
    const cfg = loadConfig(
      JSON.stringify({
        name: "test",
        command: "echo hi",
        commands: [{ name: "run" }],
      }),
    );
    expect(cfg.name).toBe("test");
  });

  it("loadConfig throws on invalid", () => {
    expect(() => loadConfig(JSON.stringify({ name: "" }))).toThrow();
  });
});

describe("cmdDefined", () => {
  it("returns false for undefined/null/empty", () => {
    expect(cmdDefined(undefined)).toBe(false);
    expect(cmdDefined("")).toBe(false);
    expect(cmdDefined([])).toBe(false);
  });

  it("returns true for valid commands", () => {
    expect(cmdDefined("echo hi")).toBe(true);
    expect(cmdDefined(["echo", "hi"])).toBe(true);
  });
});

describe("formatRefDefined", () => {
  it("returns false for undefined/null/empty", () => {
    expect(formatRefDefined(undefined)).toBe(false);
    expect(formatRefDefined("")).toBe(false);
  });

  it("returns true for valid refs", () => {
    expect(formatRefDefined("user")).toBe(true);
    expect(
      formatRefDefined({
        views: [{ name: "v", template: "t" }],
      }),
    ).toBe(true);
  });
});
