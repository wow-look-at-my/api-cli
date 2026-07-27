// Width-aware column alignment — port of align.go.

export function displayWidth(s: string): number {
  let w = 0;
  let i = 0;
  while (i < s.length) {
    if (s.charCodeAt(i) === 0x1b) {
      i = skipEscape(s, i);
      continue;
    }
    const cp = s.codePointAt(i)!;
    const size = cp > 0xffff ? 2 : 1;
    i += size;
    w += runeWidth(cp);
  }
  return w;
}

export function stripANSI(s: string): string {
  if (!s.includes("\x1b")) return s;
  let out = "";
  let i = 0;
  while (i < s.length) {
    if (s.charCodeAt(i) === 0x1b) {
      i = skipEscape(s, i);
      continue;
    }
    out += s[i];
    i++;
  }
  return out;
}

function runeWidth(cp: number): number {
  if (cp < 0x20 || cp === 0x7f) return 0;
  if (isEastAsianWide(cp)) return 2;
  return 1;
}

function isEastAsianWide(cp: number): boolean {
  // CJK Unified Ideographs
  if (cp >= 0x4e00 && cp <= 0x9fff) return true;
  // CJK Unified Ideographs Extension A
  if (cp >= 0x3400 && cp <= 0x4dbf) return true;
  // CJK Compatibility Ideographs
  if (cp >= 0xf900 && cp <= 0xfaff) return true;
  // Fullwidth Forms
  if (cp >= 0xff01 && cp <= 0xff60) return true;
  if (cp >= 0xffe0 && cp <= 0xffe6) return true;
  // CJK Radicals Supplement, Kangxi Radicals
  if (cp >= 0x2e80 && cp <= 0x2fdf) return true;
  // CJK Symbols and Punctuation, Hiragana, Katakana
  if (cp >= 0x3000 && cp <= 0x303f) return true;
  if (cp >= 0x3040 && cp <= 0x309f) return true;
  if (cp >= 0x30a0 && cp <= 0x30ff) return true;
  // Hangul Syllables
  if (cp >= 0xac00 && cp <= 0xd7af) return true;
  // CJK Unified Ideographs Extension B+
  if (cp >= 0x20000 && cp <= 0x2fa1f) return true;
  return false;
}

function skipEscape(s: string, i: number): number {
  if (i + 1 >= s.length) return i + 1;
  const next = s.charCodeAt(i + 1);
  if (next === 0x5b) {
    // CSI: ESC [ ... final
    let j = i + 2;
    while (j < s.length) {
      const c = s.charCodeAt(j);
      if (c >= 0x40 && c <= 0x7e) return j + 1;
      j++;
    }
    return j;
  }
  if (next === 0x5d) {
    // OSC: ESC ] ... BEL or ST
    let j = i + 2;
    while (j < s.length) {
      if (s.charCodeAt(j) === 0x07) return j + 1;
      if (s.charCodeAt(j) === 0x1b && j + 1 < s.length && s.charCodeAt(j + 1) === 0x5c)
        return j + 2;
      j++;
    }
    return j;
  }
  return i + 2;
}

export function alignColumns(rows: string[], padding: number): string {
  if (padding < 1) padding = 1;
  if (rows.length === 0) return "";

  const cells = rows.map((r) => r.replace(/\n$/, "").split("\t"));
  const maxCols = Math.max(...cells.map((c) => c.length));

  const widths = new Array<number>(maxCols).fill(0);
  for (const row of cells) {
    for (let ci = 0; ci < row.length; ci++) {
      const w = displayWidth(row[ci]!);
      if (w > widths[ci]!) widths[ci] = w;
    }
  }

  let out = "";
  for (const row of cells) {
    for (let ci = 0; ci < maxCols; ci++) {
      const cell = ci < row.length ? row[ci]! : "";
      out += cell;
      if (ci < maxCols - 1) {
        const pad = widths[ci]! - displayWidth(cell) + padding;
        out += " ".repeat(pad);
      }
    }
    out += "\n";
  }
  return out;
}

export function padRight(n: number, s: string): string {
  const w = displayWidth(s);
  return w >= n ? s : s + " ".repeat(n - w);
}

export function padLeft(n: number, s: string): string {
  const w = displayWidth(s);
  return w >= n ? s : " ".repeat(n - w) + s;
}
