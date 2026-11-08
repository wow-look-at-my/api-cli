// Config types — mirrors the Go structs in config.go exactly so both
// implementations parse the same JSON.

export interface Config {
  $schema?: string;
  name: string;
  description?: string;
  vars?: Record<string, unknown>;
  command?: Cmd;
  cwd?: string;
  stdin?: string;
  formats?: Record<string, Format>;
  commands?: CommandNode[];
}

export interface CommandNode {
  name: string;
  description?: string;
  args?: Arg[];
  flags?: Flag[];
  vars?: Record<string, unknown>;
  command?: Cmd;
  cwd?: string;
  stdin?: string;
  steps?: Step[];
  entry?: unknown;
  preconditions?: string[];
  confirm?: string;
  format?: FormatRef;
  commands?: CommandNode[];
}

export interface Format {
  input?: "json" | "lines" | "raw";
  when?: string;
  views: View[];
}

export interface View {
  name: string;
  when?: string;
  default?: boolean;
  template: string;
}

// FormatRef: either a string (named reference) or an inline Format object.
export type FormatRef = string | Format;

export interface Step {
  name: string;
  when?: string;
  entry?: unknown;
  command?: Cmd;
  cwd?: string;
  stdin?: string;
}

// Cmd: either a string (shell form) or string[] (argv form).
export type Cmd = string | string[];

export interface Arg {
  name: string;
  type?: "string" | "int";
  required?: boolean;
  variadic?: boolean;
  description?: string;
}

export interface Flag {
  name: string;
  short?: string;
  type?: "string" | "bool" | "int" | "string-slice";
  default?: unknown;
  required?: boolean;
  conflicts?: string[];
  description?: string;
}

// --- Helpers ---

export function cmdDefined(cmd: Cmd | undefined): cmd is Cmd {
  if (cmd === undefined || cmd === null) return false;
  if (typeof cmd === "string") return cmd.length > 0;
  return Array.isArray(cmd) && cmd.length > 0;
}

export function cmdIsShell(cmd: Cmd): cmd is string {
  return typeof cmd === "string";
}

export function formatRefDefined(ref: FormatRef | undefined): ref is FormatRef {
  if (ref === undefined || ref === null) return false;
  if (typeof ref === "string") return ref.length > 0;
  return typeof ref === "object" && "views" in ref;
}

export function resolveFormat(
  ref: FormatRef | undefined,
  formats: Record<string, Format> | undefined,
): Format | undefined {
  if (!formatRefDefined(ref)) return undefined;
  if (typeof ref === "string") return formats?.[ref];
  return ref;
}

// --- Validation ---

const RESERVED_NAMES = new Set([
  "help",
  "completion",
  "__complete",
  "docs",
]);

const VALID_FLAG_TYPES = new Set(["", "string", "bool", "int", "string-slice"]);
const VALID_ARG_TYPES = new Set(["", "string", "int"]);
const VALID_FORMAT_INPUTS = new Set(["", "json", "lines", "raw"]);

export function validate(cfg: Config): string | null {
  if (!cfg.name?.trim()) return 'top-level "name" is required';

  if (cfg.formats) {
    for (const [name, f] of Object.entries(cfg.formats)) {
      if (!name.trim()) return "formats: empty name";
      const err = validateFormat(f, `formats["${name}"]`);
      if (err) return err;
    }
  }

  const seen = new Set<string>();
  const hasRootCmd = cmdDefined(cfg.command);
  for (let i = 0; i < (cfg.commands?.length ?? 0); i++) {
    const c = cfg.commands![i]!;
    const err = validateCommand(
      c,
      `commands[${i}]`,
      seen,
      hasRootCmd,
      cfg.formats,
    );
    if (err) return err;
  }
  return null;
}

function validateFormat(f: Format | undefined, where: string): string | null {
  if (!f) return `${where}: empty format`;
  if (f.input && !VALID_FORMAT_INPUTS.has(f.input))
    return `${where}: input "${f.input}" must be one of json|lines|raw`;
  if (!f.views?.length) return `${where}: at least one view is required`;
  const viewNames = new Set<string>();
  for (let i = 0; i < f.views.length; i++) {
    const v = f.views[i]!;
    const vw = `${where}.views[${i}]`;
    if (!v.name?.trim()) return `${vw}: name required`;
    if (viewNames.has(v.name)) return `${vw}: duplicate view name "${v.name}"`;
    viewNames.add(v.name);
    if (!v.template?.trim()) return `${vw}: template required`;
  }
  return null;
}

function validateCommand(
  c: CommandNode,
  where: string,
  siblings: Set<string>,
  inheritedCmd: boolean,
  formats: Record<string, Format> | undefined,
): string | null {
  if (!c.name?.trim()) return `${where}: "name" is required`;
  if (/[\s/]/.test(c.name))
    return `${where}: name "${c.name}" must not contain whitespace or slashes`;
  if (RESERVED_NAMES.has(c.name))
    return `${where}: name "${c.name}" is reserved`;
  if (siblings.has(c.name))
    return `${where}: duplicate command name "${c.name}"`;
  siblings.add(c.name);

  const argNames = new Set<string>();
  let requiredAfterOptional = false;
  for (let i = 0; i < (c.args?.length ?? 0); i++) {
    const a = c.args![i]!;
    const aw = `${where}.args[${i}]`;
    if (!a.name?.trim()) return `${aw}: name required`;
    if (a.type && !VALID_ARG_TYPES.has(a.type))
      return `${aw}: type "${a.type}" must be one of string|int`;
    if (argNames.has(a.name))
      return `${aw}: duplicate arg name "${a.name}"`;
    argNames.add(a.name);
    if (a.variadic && i !== c.args!.length - 1)
      return `${aw}: variadic arg "${a.name}" must be the last arg`;
    if (!a.required) {
      requiredAfterOptional = true;
    } else if (requiredAfterOptional) {
      return `${aw}: required arg "${a.name}" cannot follow an optional arg`;
    }
  }

  const flagNames = new Set<string>();
  const flagShorts = new Set<string>();
  for (let i = 0; i < (c.flags?.length ?? 0); i++) {
    const fl = c.flags![i]!;
    const fw = `${where}.flags[${i}]`;
    if (!fl.name?.trim()) return `${fw}: name required`;
    if (fl.type && !VALID_FLAG_TYPES.has(fl.type))
      return `${fw}: type "${fl.type}" must be one of string|bool|int|string-slice`;
    if (flagNames.has(fl.name))
      return `${fw}: duplicate flag name "${fl.name}"`;
    flagNames.add(fl.name);
    if (fl.short) {
      if (fl.short.length !== 1)
        return `${fw}: short "${fl.short}" must be a single character`;
      if (flagShorts.has(fl.short))
        return `${fw}: duplicate short "${fl.short}"`;
      flagShorts.add(fl.short);
    }
    if (fl.name.startsWith("no-"))
      return `${fw}: flag name "${fl.name}" cannot start with "no-" (reserved for bool negation)`;
  }
  for (let i = 0; i < (c.flags?.length ?? 0); i++) {
    const fl = c.flags![i]!;
    const fw = `${where}.flags[${i}]`;
    for (const peer of fl.conflicts ?? []) {
      if (peer === fl.name)
        return `${fw}: flag "${fl.name}" conflicts with itself`;
      if (!flagNames.has(peer))
        return `${fw}: flag "${fl.name}" conflicts with unknown flag "${peer}"`;
    }
  }

  const haveCmd = inheritedCmd || cmdDefined(c.command);

  if (!c.commands?.length) {
    if (!haveCmd)
      return `${where}: leaf has no command and no ancestor defines one`;
  }

  if (c.entry !== undefined && c.commands?.length)
    return `${where}: \`entry\` is only allowed on leaves (nodes with no subcommands)`;
  if (c.steps?.length && c.commands?.length)
    return `${where}: \`steps\` is only allowed on leaves (nodes with no subcommands)`;
  if (c.preconditions?.length && c.commands?.length)
    return `${where}: \`preconditions\` is only allowed on leaves (nodes with no subcommands)`;

  const stepNames = new Set<string>();
  for (let i = 0; i < (c.steps?.length ?? 0); i++) {
    const s = c.steps![i]!;
    const sw = `${where}.steps[${i}]`;
    if (!s.name?.trim()) return `${sw}: name required`;
    if (stepNames.has(s.name))
      return `${sw}: duplicate step name "${s.name}"`;
    stepNames.add(s.name);
  }

  if (formatRefDefined(c.format)) {
    const fw = `${where}.format`;
    if (typeof c.format === "object") {
      const err = validateFormat(c.format, fw);
      if (err) return err;
    } else if (typeof c.format === "string") {
      if (!formats?.[c.format])
        return `${fw}: references unknown format "${c.format}"`;
    }
  }

  const childSeen = new Set<string>();
  for (let i = 0; i < (c.commands?.length ?? 0); i++) {
    const child = c.commands![i]!;
    const err = validateCommand(
      child,
      `${where}.commands[${i}]`,
      childSeen,
      haveCmd,
      formats,
    );
    if (err) return err;
  }
  return null;
}

export function loadConfig(json: string): Config {
  const cfg = JSON.parse(json) as Config;
  const err = validate(cfg);
  if (err) throw new Error(`validate config: ${err}`);
  return cfg;
}
