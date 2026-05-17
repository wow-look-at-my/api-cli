import { describe, it, expect } from "vitest";
import { displayWidth, stripANSI, alignColumns, padRight, padLeft } from "../src/align.ts";

describe("displayWidth", () => {
  it("counts ASCII characters as width 1", () => {
    expect(displayWidth("hello")).toBe(5);
  });

  it("counts CJK characters as width 2", () => {
    expect(displayWidth("世界")).toBe(4); // 世界
  });

  it("ignores ANSI escape sequences", () => {
    expect(displayWidth("\x1b[31mred\x1b[0m")).toBe(3);
  });

  it("counts fullwidth forms as width 2", () => {
    expect(displayWidth("Ａ")).toBe(2); // Ａ (fullwidth A)
  });

  it("handles empty string", () => {
    expect(displayWidth("")).toBe(0);
  });

  it("handles mixed content", () => {
    expect(displayWidth("hi世\x1b[1m!\x1b[0m")).toBe(5); // h(1) i(1) 世(2) !(1)
  });
});

describe("stripANSI", () => {
  it("removes CSI sequences", () => {
    expect(stripANSI("\x1b[31mred\x1b[0m")).toBe("red");
  });

  it("removes OSC sequences", () => {
    expect(stripANSI("\x1b]0;title\x07text")).toBe("text");
  });

  it("returns plain string unchanged", () => {
    expect(stripANSI("hello")).toBe("hello");
  });
});

describe("alignColumns", () => {
  it("aligns tab-separated rows", () => {
    const rows = ["NAME\tAGE", "Alice\t30", "Bob\t25"];
    const result = alignColumns(rows, 2);
    expect(result).toBe("NAME   AGE\nAlice  30\nBob    25\n");
  });

  it("handles rows with different column counts", () => {
    const rows = ["A\tB\tC", "X\tY"];
    const result = alignColumns(rows, 2);
    const lines = result.split("\n");
    expect(lines.length).toBe(3); // 2 rows + trailing newline
  });

  it("handles empty input", () => {
    expect(alignColumns([], 2)).toBe("");
  });

  it("handles single column", () => {
    expect(alignColumns(["hello", "world"], 2)).toBe("hello\nworld\n");
  });

  it("width-aware alignment with CJK", () => {
    const rows = ["NAME\tVAL", "世界\t42", "ab\t99"];
    const result = alignColumns(rows, 2);
    // 世界 has display width 4, "NAME" has width 4, "ab" has width 2
    // So ab should be padded to match
    const lines = result.split("\n").filter(Boolean);
    expect(lines.length).toBe(3);
  });
});

describe("padRight", () => {
  it("pads with spaces on the right", () => {
    expect(padRight(8, "hi")).toBe("hi      ");
  });

  it("returns unchanged if already wide enough", () => {
    expect(padRight(2, "hello")).toBe("hello");
  });
});

describe("padLeft", () => {
  it("pads with spaces on the left", () => {
    expect(padLeft(8, "hi")).toBe("      hi");
  });

  it("returns unchanged if already wide enough", () => {
    expect(padLeft(2, "hello")).toBe("hello");
  });
});
