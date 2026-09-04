// Go text/template engine — TypeScript implementation.
//
// Supports the subset of Go's text/template used by api-cli configs:
//   - {{.field}} dot access, nested
//   - {{if pipeline}}...{{else}}...{{end}}
//   - {{range pipeline}}...{{else}}...{{end}}  with $index/$key and .
//   - {{with pipeline}}...{{else}}...{{end}}
//   - {{$var := pipeline}} / {{$var = pipeline}}
//   - pipelines: expr | func arg ...
//   - function calls: func arg1 arg2
//   - {{- ... -}} whitespace trimming
//   - missingkey=zero semantics

// ---------- Token types ----------

const enum TT {
  Text,
  Action, // {{ ... }}
}

interface Token {
  type: TT;
  value: string;
  trimLeft: boolean;
  trimRight: boolean;
}

// ---------- AST node types ----------

type Node =
  | TextNode
  | ActionNode
  | IfNode
  | RangeNode
  | WithNode
  | AssignNode;

interface TextNode {
  kind: "text";
  text: string;
}

interface ActionNode {
  kind: "action";
  pipeline: Pipeline;
}

interface IfNode {
  kind: "if";
  pipeline: Pipeline;
  body: Node[];
  elseBody: Node[];
}

interface RangeNode {
  kind: "range";
  pipeline: Pipeline;
  indexVar?: string;
  valueVar?: string;
  body: Node[];
  elseBody: Node[];
}

interface WithNode {
  kind: "with";
  pipeline: Pipeline;
  body: Node[];
  elseBody: Node[];
}

interface AssignNode {
  kind: "assign";
  name: string;
  declare: boolean;
  pipeline: Pipeline;
}

// A pipeline is a chain of commands separated by |
interface Pipeline {
  commands: PipelineCommand[];
}

// A command is a function call or a single value
interface PipelineCommand {
  args: Expr[];
}

type Expr =
  | { kind: "dot" }
  | { kind: "field"; path: string[] }
  | { kind: "variable"; name: string }
  | { kind: "variable_field"; name: string; path: string[] }
  | { kind: "string"; value: string }
  | { kind: "number"; value: number }
  | { kind: "bool"; value: boolean }
  | { kind: "nil" }
  | { kind: "call"; name: string; args: Expr[] }
  | { kind: "paren"; pipeline: Pipeline };

export type FuncMap = Record<string, (...args: unknown[]) => unknown>;

// ---------- Lexer ----------

function tokenize(src: string): Token[] {
  const tokens: Token[] = [];
  let i = 0;
  while (i < src.length) {
    const open = src.indexOf("{{", i);
    if (open === -1) {
      tokens.push({
        type: TT.Text,
        value: src.slice(i),
        trimLeft: false,
        trimRight: false,
      });
      break;
    }
    if (open > i) {
      tokens.push({
        type: TT.Text,
        value: src.slice(i, open),
        trimLeft: false,
        trimRight: false,
      });
    }
    // Find matching }}
    let depth = 0;
    let j = open + 2;
    let inString: string | null = null;
    let escaped = false;
    while (j < src.length) {
      const ch = src[j]!;
      if (escaped) {
        escaped = false;
        j++;
        continue;
      }
      if (ch === "\\") {
        escaped = true;
        j++;
        continue;
      }
      if (inString) {
        if (ch === inString) inString = null;
        j++;
        continue;
      }
      if (ch === '"' || ch === "`") {
        inString = ch;
        j++;
        continue;
      }
      if (j + 1 < src.length && src[j] === "{" && src[j + 1] === "{") {
        depth++;
        j += 2;
        continue;
      }
      if (j + 1 < src.length && src[j] === "}" && src[j + 1] === "}") {
        if (depth === 0) {
          break;
        }
        depth--;
        j += 2;
        continue;
      }
      j++;
    }

    let inner = src.slice(open + 2, j);
    const trimLeft = inner.startsWith("-");
    const trimRight = inner.endsWith("-");
    if (trimLeft) inner = inner.slice(1);
    if (trimRight) inner = inner.slice(0, -1);
    inner = inner.trim();

    if (trimLeft && tokens.length > 0) {
      const last = tokens[tokens.length - 1]!;
      if (last.type === TT.Text) {
        last.value = last.value.replace(/\s+$/, "");
      }
    }

    tokens.push({
      type: TT.Action,
      value: inner,
      trimLeft,
      trimRight,
    });

    i = j + 2;
  }

  // Apply trimRight: if an action has trimRight, trim leading whitespace
  // from the next text token.
  for (let t = 0; t < tokens.length - 1; t++) {
    if (tokens[t]!.type === TT.Action && tokens[t]!.trimRight) {
      const next = tokens[t + 1]!;
      if (next.type === TT.Text) {
        next.value = next.value.replace(/^\s+/, "");
      }
    }
  }

  return tokens;
}

// ---------- Parser ----------

function parse(tokens: Token[]): Node[] {
  let pos = 0;

  function parseNodes(terminators: string[]): Node[] {
    const nodes: Node[] = [];
    while (pos < tokens.length) {
      const tok = tokens[pos]!;
      if (tok.type === TT.Text) {
        if (tok.value) nodes.push({ kind: "text", text: tok.value });
        pos++;
        continue;
      }
      // Action
      const action = tok.value;
      if (terminators.some((t) => action === t || action.startsWith(t + " "))) {
        break;
      }
      pos++;
      if (action.startsWith("if ")) {
        nodes.push(parseIf(action.slice(3).trim()));
      } else if (action.startsWith("range ")) {
        nodes.push(parseRange(action.slice(6).trim()));
      } else if (action.startsWith("with ")) {
        nodes.push(parseWith(action.slice(5).trim()));
      } else if (action.startsWith("$") && (action.includes(":=") || action.includes(" = "))) {
        nodes.push(parseAssign(action));
      } else {
        nodes.push({ kind: "action", pipeline: parsePipeline(action) });
      }
    }
    return nodes;
  }

  function parseIf(condStr: string): IfNode {
    const pipeline = parsePipeline(condStr);
    const body = parseNodes(["else", "end"]);
    let elseBody: Node[] = [];
    if (pos < tokens.length) {
      const tok = tokens[pos]!;
      if (tok.value === "else") {
        pos++;
        elseBody = parseNodes(["end"]);
      } else if (tok.value.startsWith("else if ")) {
        // {{else if ...}} — parse as nested if inside elseBody
        const nestedCond = tok.value.slice(8).trim();
        pos++;
        elseBody = [parseIf(nestedCond)];
      }
    }
    if (pos < tokens.length && tokens[pos]!.value === "end") pos++;
    return { kind: "if", pipeline, body, elseBody };
  }

  function parseRange(exprStr: string): RangeNode {
    let indexVar: string | undefined;
    let valueVar: string | undefined;
    let pipeStr = exprStr;

    // Check for $i, $v := range ...
    const assignMatch = exprStr.match(
      /^(\$\w+)\s*,\s*(\$\w+)\s*:=\s*(.+)$/,
    );
    if (assignMatch) {
      indexVar = assignMatch[1]!;
      valueVar = assignMatch[2]!;
      pipeStr = assignMatch[3]!;
    } else {
      const singleMatch = exprStr.match(/^(\$\w+)\s*,?\s*:=\s*(.+)$/);
      if (singleMatch) {
        valueVar = singleMatch[1]!;
        pipeStr = singleMatch[2]!;
      }
    }

    const pipeline = parsePipeline(pipeStr);
    const body = parseNodes(["else", "end"]);
    let elseBody: Node[] = [];
    if (pos < tokens.length && tokens[pos]!.value === "else") {
      pos++;
      elseBody = parseNodes(["end"]);
    }
    if (pos < tokens.length && tokens[pos]!.value === "end") pos++;
    return { kind: "range", pipeline, indexVar, valueVar, body, elseBody };
  }

  function parseWith(exprStr: string): WithNode {
    const pipeline = parsePipeline(exprStr);
    const body = parseNodes(["else", "end"]);
    let elseBody: Node[] = [];
    if (pos < tokens.length && tokens[pos]!.value === "else") {
      pos++;
      elseBody = parseNodes(["end"]);
    }
    if (pos < tokens.length && tokens[pos]!.value === "end") pos++;
    return { kind: "with", pipeline, body, elseBody };
  }

  function parseAssign(action: string): AssignNode {
    const declareMatch = action.match(/^(\$\w+)\s*:=\s*(.+)$/);
    if (declareMatch) {
      return {
        kind: "assign",
        name: declareMatch[1]!,
        declare: true,
        pipeline: parsePipeline(declareMatch[2]!),
      };
    }
    const reassignMatch = action.match(/^(\$\w+)\s*=\s*(.+)$/);
    if (reassignMatch) {
      return {
        kind: "assign",
        name: reassignMatch[1]!,
        declare: false,
        pipeline: parsePipeline(reassignMatch[2]!),
      };
    }
    throw new Error(`invalid assignment: ${action}`);
  }

  return parseNodes([]);
}

// ---------- Pipeline/expression parser ----------

function parsePipeline(src: string): Pipeline {
  // Split on top-level | (not inside strings, parens, or nested calls)
  const segments = splitPipeline(src);
  return { commands: segments.map(parseCommand) };
}

function splitPipeline(src: string): string[] {
  const parts: string[] = [];
  let depth = 0;
  let inStr: string | null = null;
  let escaped = false;
  let start = 0;
  for (let i = 0; i < src.length; i++) {
    const ch = src[i]!;
    if (escaped) {
      escaped = false;
      continue;
    }
    if (ch === "\\") {
      escaped = true;
      continue;
    }
    if (inStr) {
      if (ch === inStr) inStr = null;
      continue;
    }
    if (ch === '"' || ch === "`") {
      inStr = ch;
      continue;
    }
    if (ch === "(") {
      depth++;
      continue;
    }
    if (ch === ")") {
      depth--;
      continue;
    }
    if (ch === "|" && depth === 0) {
      parts.push(src.slice(start, i).trim());
      start = i + 1;
    }
  }
  parts.push(src.slice(start).trim());
  return parts.filter((p) => p.length > 0);
}

function parseCommand(src: string): PipelineCommand {
  const args = tokenizeExpr(src);
  return { args };
}

function tokenizeExpr(src: string): Expr[] {
  const exprs: Expr[] = [];
  let i = 0;
  src = src.trim();

  while (i < src.length) {
    // Skip whitespace
    while (i < src.length && /\s/.test(src[i]!)) i++;
    if (i >= src.length) break;

    const ch = src[i]!;

    // Parenthesized subexpression
    if (ch === "(") {
      let depth = 1;
      let j = i + 1;
      while (j < src.length && depth > 0) {
        if (src[j] === "(") depth++;
        else if (src[j] === ")") depth--;
        j++;
      }
      const inner = src.slice(i + 1, j - 1).trim();
      exprs.push({ kind: "paren", pipeline: parsePipeline(inner) });
      i = j;
      continue;
    }

    // String literal (double-quoted)
    if (ch === '"') {
      let j = i + 1;
      let str = "";
      while (j < src.length && src[j] !== '"') {
        if (src[j] === "\\" && j + 1 < src.length) {
          const next = src[j + 1]!;
          if (next === "n") str += "\n";
          else if (next === "t") str += "\t";
          else if (next === "\\") str += "\\";
          else if (next === '"') str += '"';
          else str += "\\" + next;
          j += 2;
        } else {
          str += src[j];
          j++;
        }
      }
      j++; // skip closing "
      exprs.push({ kind: "string", value: str });
      i = j;
      continue;
    }

    // Backtick string (raw)
    if (ch === "`") {
      const end = src.indexOf("`", i + 1);
      const j = end === -1 ? src.length : end;
      exprs.push({ kind: "string", value: src.slice(i + 1, j) });
      i = j + 1;
      continue;
    }

    // Dot access: .field.subfield or bare .
    if (ch === ".") {
      if (i + 1 >= src.length || /[\s)|]/.test(src[i + 1]!)) {
        exprs.push({ kind: "dot" });
        i++;
        continue;
      }
      const path: string[] = [];
      let j = i + 1;
      while (j < src.length) {
        const fieldStart = j;
        while (j < src.length && /[a-zA-Z0-9_]/.test(src[j]!)) j++;
        if (j === fieldStart) break;
        path.push(src.slice(fieldStart, j));
        if (j < src.length && src[j] === ".") {
          j++;
        } else {
          break;
        }
      }
      if (path.length === 0) {
        exprs.push({ kind: "dot" });
        i++;
      } else {
        exprs.push({ kind: "field", path });
        i = j;
      }
      continue;
    }

    // Variable: $name or $name.field
    if (ch === "$") {
      let j = i + 1;
      while (j < src.length && /[a-zA-Z0-9_]/.test(src[j]!)) j++;
      const name = src.slice(i, j);
      if (j < src.length && src[j] === ".") {
        j++;
        const path: string[] = [];
        while (j < src.length) {
          const fieldStart = j;
          while (j < src.length && /[a-zA-Z0-9_]/.test(src[j]!)) j++;
          if (j === fieldStart) break;
          path.push(src.slice(fieldStart, j));
          if (j < src.length && src[j] === ".") {
            j++;
          } else {
            break;
          }
        }
        exprs.push({ kind: "variable_field", name, path });
      } else {
        exprs.push({ kind: "variable", name });
      }
      i = j;
      continue;
    }

    // Number
    if (/[0-9]/.test(ch) || (ch === "-" && i + 1 < src.length && /[0-9]/.test(src[i + 1]!))) {
      let j = i;
      if (ch === "-") j++;
      while (j < src.length && /[0-9.]/.test(src[j]!)) j++;
      const numStr = src.slice(i, j);
      exprs.push({ kind: "number", value: Number(numStr) });
      i = j;
      continue;
    }

    // Keyword or function name
    if (/[a-zA-Z_]/.test(ch)) {
      let j = i;
      while (j < src.length && /[a-zA-Z0-9_]/.test(src[j]!)) j++;
      const word = src.slice(i, j);
      if (word === "true") exprs.push({ kind: "bool", value: true });
      else if (word === "false") exprs.push({ kind: "bool", value: false });
      else if (word === "nil") exprs.push({ kind: "nil" });
      else if (word === "not" || word === "and" || word === "or" ||
               word === "eq" || word === "ne" || word === "lt" ||
               word === "le" || word === "gt" || word === "ge" ||
               word === "len" || word === "index" || word === "call" ||
               word === "print" || word === "println" || word === "printf" ||
               word === "slice") {
        exprs.push({ kind: "call", name: word, args: [] });
      } else {
        // Treat as function name
        exprs.push({ kind: "call", name: word, args: [] });
      }
      i = j;
      continue;
    }

    i++;
  }

  return exprs;
}

// ---------- Execution ----------

interface ExecContext {
  dot: unknown;
  vars: Map<string, unknown>;
  funcs: FuncMap;
}

function resolveDot(dot: unknown, path: string[]): unknown {
  let val = dot;
  for (const key of path) {
    if (val === null || val === undefined) return undefined;
    if (typeof val === "object") {
      val = (val as Record<string, unknown>)[key];
    } else {
      return undefined;
    }
  }
  return val;
}

function evalExpr(expr: Expr, ctx: ExecContext, pipeArg?: unknown): unknown {
  switch (expr.kind) {
    case "dot":
      return ctx.dot;
    case "field":
      return resolveDot(ctx.dot, expr.path);
    case "variable":
      return ctx.vars.get(expr.name);
    case "variable_field": {
      const base = ctx.vars.get(expr.name);
      return resolveDot(base, expr.path);
    }
    case "string":
      return expr.value;
    case "number":
      return expr.value;
    case "bool":
      return expr.value;
    case "nil":
      return null;
    case "paren":
      return evalPipeline(expr.pipeline, ctx);
    case "call":
      return undefined; // handled in evalCommand
  }
}

function evalCommand(cmd: PipelineCommand, ctx: ExecContext, pipeArg?: unknown): unknown {
  const { args } = cmd;
  if (args.length === 0) return pipeArg ?? undefined;

  const first = args[0]!;

  // Single value (no function call)
  if (args.length === 1 && first.kind !== "call") {
    return evalExpr(first, ctx);
  }

  // First is a function call
  if (first.kind === "call") {
    const fnName = first.name;
    const fn = ctx.funcs[fnName];
    if (!fn) throw new Error(`function "${fnName}" not defined`);

    const fnArgs: unknown[] = [];
    for (let i = 1; i < args.length; i++) {
      const a = args[i]!;
      if (a.kind === "call") {
        // Nested call treated as function with remaining args
        const subArgs = args.slice(i);
        fnArgs.push(evalCommand({ args: subArgs }, ctx, pipeArg));
        break;
      }
      fnArgs.push(evalExpr(a, ctx));
    }
    if (pipeArg !== undefined) fnArgs.push(pipeArg);
    return fn(...fnArgs);
  }

  // First is a value, rest are... shouldn't happen in Go templates,
  // but handle gracefully
  return evalExpr(first, ctx);
}

function evalPipeline(pipeline: Pipeline, ctx: ExecContext): unknown {
  let val: unknown = undefined;
  for (let i = 0; i < pipeline.commands.length; i++) {
    val = evalCommand(pipeline.commands[i]!, ctx, i === 0 ? undefined : val);
  }
  return val;
}

function isTruthyValue(v: unknown): boolean {
  if (v === null || v === undefined) return false;
  if (typeof v === "boolean") return v;
  if (typeof v === "number") return v !== 0;
  if (typeof v === "string") return v.length > 0;
  if (Array.isArray(v)) return v.length > 0;
  if (v instanceof Map) return v.size > 0;
  if (typeof v === "object") return true;
  return Boolean(v);
}

function formatValue(v: unknown): string {
  if (v === null || v === undefined) return "";
  if (typeof v === "string") return v;
  if (typeof v === "number" || typeof v === "boolean") return String(v);
  if (Array.isArray(v)) return v.map(formatValue).join(" ");
  if (typeof v === "object") {
    // Go's fmt prints maps as map[k1:v1 k2:v2], but for templates
    // we typically want JSON or the raw value for nested access
    return JSON.stringify(v);
  }
  return String(v);
}

function execNodes(nodes: Node[], ctx: ExecContext): string {
  let out = "";
  for (const node of nodes) {
    out += execNode(node, ctx);
  }
  return out;
}

function execNode(node: Node, ctx: ExecContext): string {
  switch (node.kind) {
    case "text":
      return node.text;

    case "action": {
      const val = evalPipeline(node.pipeline, ctx);
      return formatValue(val);
    }

    case "assign": {
      const val = evalPipeline(node.pipeline, ctx);
      ctx.vars.set(node.name, val);
      return "";
    }

    case "if": {
      const val = evalPipeline(node.pipeline, ctx);
      if (isTruthyValue(val)) {
        return execNodes(node.body, ctx);
      }
      return execNodes(node.elseBody, ctx);
    }

    case "with": {
      const val = evalPipeline(node.pipeline, ctx);
      if (isTruthyValue(val)) {
        const childCtx: ExecContext = {
          dot: val,
          vars: new Map(ctx.vars),
          funcs: ctx.funcs,
        };
        return execNodes(node.body, childCtx);
      }
      return execNodes(node.elseBody, ctx);
    }

    case "range": {
      const val = evalPipeline(node.pipeline, ctx);
      if (val === null || val === undefined) {
        return execNodes(node.elseBody, ctx);
      }

      let items: [unknown, unknown][];
      if (Array.isArray(val)) {
        if (val.length === 0) return execNodes(node.elseBody, ctx);
        items = val.map((v, i) => [i, v]);
      } else if (typeof val === "object" && val !== null) {
        const entries = Object.entries(val as Record<string, unknown>);
        if (entries.length === 0) return execNodes(node.elseBody, ctx);
        items = entries;
      } else {
        return execNodes(node.elseBody, ctx);
      }

      let result = "";
      for (const [key, value] of items) {
        const childCtx: ExecContext = {
          dot: value,
          vars: new Map(ctx.vars),
          funcs: ctx.funcs,
        };
        if (node.indexVar) childCtx.vars.set(node.indexVar, key);
        if (node.valueVar) childCtx.vars.set(node.valueVar, value);
        result += execNodes(node.body, childCtx);
      }
      return result;
    }
  }
}

// ---------- Public API ----------

export class Template {
  private nodes: Node[];
  private funcs: FuncMap;

  constructor(src: string, funcs: FuncMap = {}) {
    const tokens = tokenize(src);
    this.nodes = parse(tokens);
    this.funcs = funcs;
  }

  execute(data: unknown): string {
    const ctx: ExecContext = {
      dot: data,
      vars: new Map(),
      funcs: this.funcs,
    };
    return execNodes(this.nodes, ctx);
  }
}

export function executeTemplate(
  src: string,
  data: unknown,
  funcs: FuncMap = {},
): string {
  return new Template(src, funcs).execute(data);
}
