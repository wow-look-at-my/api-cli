// Template function library — covers the sprig subset and custom helpers
// used by api-cli configs.

import type { FuncMap } from "./template.ts";
import { alignColumns, displayWidth, padLeft, padRight, stripANSI } from "./align.ts";

export function buildFuncMap(extra?: FuncMap): FuncMap {
  const fm: FuncMap = {
    // --- Go builtins ---
    len: goLen,
    index: goIndex,
    slice: goSlice,
    print: goPrint,
    println: goPrintln,
    printf: goPrintf,
    not: (v: unknown) => !isTruthyForLogic(v),
    and: (...args: unknown[]) => {
      let last: unknown = true;
      for (const a of args) {
        last = a;
        if (!isTruthyForLogic(a)) return a;
      }
      return last;
    },
    or: (...args: unknown[]) => {
      for (const a of args) {
        if (isTruthyForLogic(a)) return a;
      }
      return args[args.length - 1];
    },
    eq: (a: unknown, ...rest: unknown[]) => {
      for (const b of rest) {
        if (looseEqual(a, b)) return true;
      }
      return rest.length === 0 ? false : false;
    },
    ne: (a: unknown, b: unknown) => !looseEqual(a, b),
    lt: (a: unknown, b: unknown) => toNum(a) < toNum(b),
    le: (a: unknown, b: unknown) => toNum(a) <= toNum(b),
    gt: (a: unknown, b: unknown) => toNum(a) > toNum(b),
    ge: (a: unknown, b: unknown) => toNum(a) >= toNum(b),
    call: (fn: unknown, ...args: unknown[]) => {
      if (typeof fn !== "function") throw new Error("call: not a function");
      return fn(...args);
    },

    // --- Sprig-compatible helpers ---
    default: (def: unknown, val: unknown) =>
      val === undefined || val === null || val === "" || val === false
        ? def
        : val,
    required: (msg: unknown, val: unknown) => {
      if (val === undefined || val === null || val === "")
        throw new Error(String(msg));
      return val;
    },
    toJson: (v: unknown) => JSON.stringify(v),
    toPrettyJson: (v: unknown) => JSON.stringify(v, null, 2),
    fromJson: (s: unknown) => JSON.parse(String(s)),
    trim: (s: unknown) => String(s ?? "").trim(),
    trimSuffix: (suffix: unknown, s: unknown) => {
      const str = String(s ?? "");
      const sfx = String(suffix);
      return str.endsWith(sfx) ? str.slice(0, -sfx.length) : str;
    },
    trimPrefix: (prefix: unknown, s: unknown) => {
      const str = String(s ?? "");
      const pfx = String(prefix);
      return str.startsWith(pfx) ? str.slice(pfx.length) : str;
    },
    upper: (s: unknown) => String(s ?? "").toUpperCase(),
    lower: (s: unknown) => String(s ?? "").toLowerCase(),
    title: (s: unknown) =>
      String(s ?? "").replace(/\b\w/g, (c) => c.toUpperCase()),
    repeat: (n: unknown, s: unknown) => String(s ?? "").repeat(toNum(n)),
    contains: (substr: unknown, s: unknown) =>
      String(s ?? "").includes(String(substr)),
    hasPrefix: (prefix: unknown, s: unknown) =>
      String(s ?? "").startsWith(String(prefix)),
    hasSuffix: (suffix: unknown, s: unknown) =>
      String(s ?? "").endsWith(String(suffix)),
    replace: (old: unknown, new_: unknown, s: unknown) =>
      String(s ?? "").split(String(old)).join(String(new_)),
    nospace: (s: unknown) => String(s ?? "").replace(/\s+/g, ""),
    substr: (start: unknown, end: unknown, s: unknown) =>
      String(s ?? "").slice(toNum(start), toNum(end)),
    split: (sep: unknown, s: unknown) => String(s ?? "").split(String(sep)),
    join: (sep: unknown, arr: unknown) => {
      if (Array.isArray(arr)) return arr.join(String(sep));
      return String(arr ?? "");
    },
    b64enc: (s: unknown) => btoa(String(s ?? "")),
    b64dec: (s: unknown) => atob(String(s ?? "")),
    regexMatch: (pattern: unknown, s: unknown) =>
      new RegExp(String(pattern)).test(String(s ?? "")),
    regexReplaceAll: (pattern: unknown, s: unknown, repl: unknown) =>
      String(s ?? "").replace(new RegExp(String(pattern), "g"), String(repl)),
    regexFind: (pattern: unknown, s: unknown) => {
      const m = String(s ?? "").match(new RegExp(String(pattern)));
      return m ? m[0] : "";
    },
    indent: (n: unknown, s: unknown) => {
      const pad = " ".repeat(toNum(n));
      return String(s ?? "")
        .split("\n")
        .map((line) => pad + line)
        .join("\n");
    },
    nindent: (n: unknown, s: unknown) => {
      const pad = " ".repeat(toNum(n));
      return (
        "\n" +
        String(s ?? "")
          .split("\n")
          .map((line) => pad + line)
          .join("\n")
      );
    },

    // Type checks (kindIs)
    kindIs: (kind: unknown, v: unknown) => {
      const k = String(kind);
      if (k === "slice") return Array.isArray(v);
      if (k === "map")
        return v !== null && typeof v === "object" && !Array.isArray(v);
      if (k === "string") return typeof v === "string";
      if (k === "int" || k === "int64" || k === "float64")
        return typeof v === "number";
      if (k === "bool") return typeof v === "boolean";
      if (k === "invalid") return v === null || v === undefined;
      return false;
    },
    typeIs: (type_: unknown, v: unknown) => {
      const t = String(type_);
      return typeof v === t;
    },
    hasKey: (obj: unknown, key: unknown) => {
      if (obj === null || obj === undefined || typeof obj !== "object")
        return false;
      return String(key) in (obj as Record<string, unknown>);
    },

    // Math
    add: (...args: unknown[]) =>
      args.reduce((acc: number, v) => acc + toNum(v), 0),
    sub: (a: unknown, b: unknown) => toNum(a) - toNum(b),
    mul: (...args: unknown[]) =>
      args.reduce((acc: number, v) => acc * toNum(v), 1),
    div: (a: unknown, b: unknown) => Math.trunc(toNum(a) / toNum(b)),
    mod: (a: unknown, b: unknown) => toNum(a) % toNum(b),
    max: (...args: unknown[]) => Math.max(...args.map(toNum)),
    min: (...args: unknown[]) => Math.min(...args.map(toNum)),
    ceil: (v: unknown) => Math.ceil(toNum(v)),
    floor: (v: unknown) => Math.floor(toNum(v)),
    round: (p: unknown, v: unknown) => {
      const prec = toNum(p);
      const val = toNum(v);
      const factor = Math.pow(10, prec);
      return Math.round(val * factor) / factor;
    },
    int: (v: unknown) => Math.trunc(toNum(v)),
    float64: (v: unknown) => toNum(v),

    // Float arithmetic (sprig names)
    addf: (...args: unknown[]) =>
      args.reduce((acc: number, v) => acc + toNum(v), 0),
    subf: (a: unknown, b: unknown) => toNum(a) - toNum(b),
    mulf: (...args: unknown[]) =>
      args.reduce((acc: number, v) => acc * toNum(v), 1),
    divf: (a: unknown, b: unknown) => toNum(a) / toNum(b),

    // List helpers
    list: (...args: unknown[]) => [...args],
    append: (list: unknown, item: unknown) => {
      if (!Array.isArray(list)) return [item];
      return [...list, item];
    },
    prepend: (list: unknown, item: unknown) => {
      if (!Array.isArray(list)) return [item];
      return [item, ...list];
    },
    first: (list: unknown) => (Array.isArray(list) ? list[0] : undefined),
    last: (list: unknown) =>
      Array.isArray(list) ? list[list.length - 1] : undefined,
    rest: (list: unknown) =>
      Array.isArray(list) ? list.slice(1) : [],
    initial: (list: unknown) =>
      Array.isArray(list) ? list.slice(0, -1) : [],
    reverse: (list: unknown) =>
      Array.isArray(list) ? [...list].reverse() : list,
    uniq: (list: unknown) =>
      Array.isArray(list) ? [...new Set(list)] : list,
    compact: (list: unknown) =>
      Array.isArray(list) ? list.filter((x) => x !== "" && x !== null && x !== undefined) : list,
    has: (item: unknown, list: unknown) =>
      Array.isArray(list) ? list.includes(item) : false,
    without: (list: unknown, ...items: unknown[]) =>
      Array.isArray(list) ? list.filter((x) => !items.includes(x)) : list,
    sortAlpha: (list: unknown) =>
      Array.isArray(list) ? [...list].sort((a, b) => String(a).localeCompare(String(b))) : list,

    // Dict/map
    dict: (...args: unknown[]) => {
      const out: Record<string, unknown> = {};
      for (let i = 0; i + 1 < args.length; i += 2) {
        out[String(args[i])] = args[i + 1];
      }
      return out;
    },
    keys: (m: unknown) =>
      m && typeof m === "object" ? Object.keys(m as Record<string, unknown>) : [],
    values: (m: unknown) =>
      m && typeof m === "object"
        ? Object.values(m as Record<string, unknown>)
        : [],
    pick: (m: unknown, ...keys: unknown[]) => {
      if (!m || typeof m !== "object") return {};
      const out: Record<string, unknown> = {};
      for (const k of keys) {
        const key = String(k);
        if (key in (m as Record<string, unknown>)) {
          out[key] = (m as Record<string, unknown>)[key];
        }
      }
      return out;
    },
    omit: (m: unknown, ...keys: unknown[]) => {
      if (!m || typeof m !== "object") return {};
      const keySet = new Set(keys.map(String));
      const out: Record<string, unknown> = {};
      for (const [k, v] of Object.entries(m as Record<string, unknown>)) {
        if (!keySet.has(k)) out[k] = v;
      }
      return out;
    },
    merge: (...maps: unknown[]) => {
      const out: Record<string, unknown> = {};
      for (const m of maps) {
        if (m && typeof m === "object") {
          Object.assign(out, m);
        }
      }
      return out;
    },

    // --- api-cli custom helpers ---
    querystring: queryString,
    repeatkey: repeatKey,
    shellquote: shellQuote,
    urlpath: (s: unknown) => encodeURIComponent(String(s ?? "")),
    spread: spreadHelper,
    fileExists: () => false, // no filesystem on Workers
    dirExists: () => false,
    tabwriter: (v: unknown) => {
      const rows = toRows(v);
      return alignColumns(rows, 2);
    },
    padRight: (n: unknown, s: unknown) => padRight(toNum(n), String(s ?? "")),
    padLeft: (n: unknown, s: unknown) => padLeft(toNum(n), String(s ?? "")),
    displayWidth: (s: unknown) => displayWidth(String(s ?? "")),
    stripANSI: (s: unknown) => stripANSI(String(s ?? "")),
  };

  if (extra) Object.assign(fm, extra);
  return fm;
}

// --- Helpers ---

function isTruthyForLogic(v: unknown): boolean {
  if (v === null || v === undefined) return false;
  if (typeof v === "boolean") return v;
  if (typeof v === "number") return v !== 0;
  if (typeof v === "string") return v.length > 0;
  if (Array.isArray(v)) return v.length > 0;
  return true;
}

function looseEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (typeof a === "number" && typeof b === "number") return a === b;
  // Numeric string comparison
  if (typeof a === "number" && typeof b === "string") return a === Number(b);
  if (typeof a === "string" && typeof b === "number") return Number(a) === b;
  return String(a) === String(b);
}

function toNum(v: unknown): number {
  if (typeof v === "number") return v;
  if (typeof v === "string") {
    const n = Number(v);
    return isNaN(n) ? 0 : n;
  }
  if (typeof v === "boolean") return v ? 1 : 0;
  return 0;
}

function goLen(v: unknown): number {
  if (typeof v === "string") return v.length;
  if (Array.isArray(v)) return v.length;
  if (v && typeof v === "object") return Object.keys(v).length;
  return 0;
}

function goIndex(collection: unknown, ...keys: unknown[]): unknown {
  let val = collection;
  for (const key of keys) {
    if (val === null || val === undefined) return undefined;
    if (Array.isArray(val)) {
      const idx = typeof key === "number" ? key : parseInt(String(key), 10);
      val = val[idx];
    } else if (typeof val === "object") {
      val = (val as Record<string, unknown>)[String(key)];
    } else {
      return undefined;
    }
  }
  return val;
}

function goSlice(collection: unknown, ...indices: unknown[]): unknown {
  if (!Array.isArray(collection) && typeof collection !== "string")
    return collection;
  const arr = Array.isArray(collection) ? collection : collection;
  if (indices.length === 0) return arr;
  if (indices.length === 1)
    return typeof arr === "string"
      ? arr.slice(toNum(indices[0]))
      : (arr as unknown[]).slice(toNum(indices[0]));
  return typeof arr === "string"
    ? arr.slice(toNum(indices[0]), toNum(indices[1]))
    : (arr as unknown[]).slice(toNum(indices[0]), toNum(indices[1]));
}

function goPrint(...args: unknown[]): string {
  return args.map((a) => (a === undefined || a === null ? "" : String(a))).join("");
}

function goPrintln(...args: unknown[]): string {
  return goPrint(...args) + "\n";
}

function goPrintf(fmt: unknown, ...args: unknown[]): string {
  return sprintfGo(String(fmt), args);
}

// Minimal Go-compatible sprintf
function sprintfGo(fmt: string, args: unknown[]): string {
  let out = "";
  let ai = 0;
  let i = 0;
  while (i < fmt.length) {
    if (fmt[i] === "%" && i + 1 < fmt.length) {
      i++;
      if (fmt[i] === "%") {
        out += "%";
        i++;
        continue;
      }
      // Parse flags and width
      let flags = "";
      while (i < fmt.length && "-+0 #".includes(fmt[i]!)) {
        flags += fmt[i];
        i++;
      }
      let width = "";
      while (i < fmt.length && /[0-9]/.test(fmt[i]!)) {
        width += fmt[i];
        i++;
      }
      let prec = "";
      if (i < fmt.length && fmt[i] === ".") {
        i++;
        while (i < fmt.length && /[0-9]/.test(fmt[i]!)) {
          prec += fmt[i];
          i++;
        }
      }
      const verb = fmt[i]!;
      i++;
      const arg = ai < args.length ? args[ai] : undefined;
      ai++;

      let s: string;
      switch (verb) {
        case "s":
          s = arg === undefined || arg === null ? "" : String(arg);
          break;
        case "d":
          s = String(Math.trunc(toNum(arg)));
          break;
        case "f": {
          const p = prec ? parseInt(prec, 10) : 6;
          s = toNum(arg).toFixed(p);
          break;
        }
        case "v":
          s = arg === undefined || arg === null ? "<nil>" : String(arg);
          break;
        case "t":
          s = String(Boolean(arg));
          break;
        case "q":
          s = JSON.stringify(String(arg ?? ""));
          break;
        default:
          s = String(arg ?? "");
      }

      if (width) {
        const w = parseInt(width, 10);
        if (flags.includes("-")) {
          s = s.padEnd(w);
        } else {
          s = s.padStart(w, flags.includes("0") ? "0" : " ");
        }
      }
      out += s;
    } else {
      out += fmt[i];
      i++;
    }
  }
  return out;
}

// --- Custom helpers ---

const SPREAD_SENTINEL = "\x00";

function spreadHelper(v: unknown): string {
  if (v === null || v === undefined) return SPREAD_SENTINEL;
  if (Array.isArray(v)) {
    if (v.length === 0) return SPREAD_SENTINEL;
    return SPREAD_SENTINEL + v.map(String).join(SPREAD_SENTINEL);
  }
  return SPREAD_SENTINEL + String(v);
}

function queryString(v: unknown): string {
  if (v === null || v === undefined) return "";
  if (typeof v !== "object") return "";
  const params = new URLSearchParams();
  const entries = Object.entries(v as Record<string, unknown>).sort(
    ([a], [b]) => a.localeCompare(b),
  );
  for (const [key, val] of entries) {
    addQueryValue(params, key, val);
  }
  const enc = params.toString();
  return enc ? "?" + enc : "";
}

function addQueryValue(
  params: URLSearchParams,
  key: string,
  val: unknown,
): void {
  if (val === null || val === undefined) return;
  if (typeof val === "string") {
    if (val !== "") params.append(key, val);
    return;
  }
  if (typeof val === "boolean") {
    params.append(key, String(val));
    return;
  }
  if (typeof val === "number") {
    params.append(key, String(val));
    return;
  }
  if (Array.isArray(val)) {
    for (const item of val) addQueryValue(params, key, item);
    return;
  }
  params.append(key, String(val));
}

function repeatKey(key: unknown, v: unknown): string {
  if (!Array.isArray(v)) return "";
  const params = new URLSearchParams();
  for (const item of v) {
    const s = String(item);
    if (s !== "") params.append(String(key), s);
  }
  return params.toString();
}

function shellQuote(s: unknown): string {
  return "'" + String(s ?? "").replace(/'/g, "'\\''") + "'";
}

function toRows(v: unknown): string[] {
  if (!v) return [];
  if (Array.isArray(v)) {
    return v.map((row) => {
      if (typeof row === "string") return row;
      if (Array.isArray(row)) return row.map(String).join("\t");
      return String(row);
    });
  }
  return [];
}
