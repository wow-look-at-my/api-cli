// Curl command parser — extracts HTTP request details from rendered
// curl commands so Workers can execute them via fetch().

export interface ParsedRequest {
  url: string;
  method: string;
  headers: Record<string, string>;
  body?: string;
  followRedirects: boolean;
}

export interface ParseWarning {
  flag: string;
  message: string;
}

export interface CurlParseResult {
  request?: ParsedRequest;
  warnings: ParseWarning[];
  error?: string;
}

// parseCurlCommand parses a rendered curl command string into an HTTP request.
// Handles common curl flags; warns about unsupported ones.
export function parseCurlCommand(rendered: string): CurlParseResult {
  const argv = shellSplit(rendered);
  return parseCurlArgv(argv);
}

// parseCurlArgv parses a curl argv array into an HTTP request.
export function parseCurlArgv(argv: string[]): CurlParseResult {
  const warnings: ParseWarning[] = [];

  if (argv.length === 0) {
    return { warnings, error: "empty command" };
  }

  // Strip leading shell wrapper: "sh -c ..." or "/bin/sh -c ..."
  let args = argv;
  if (
    (args[0] === "sh" || args[0] === "/bin/sh") &&
    args[1] === "-c" &&
    args.length === 3
  ) {
    args = shellSplit(args[2]!);
  }

  // Handle piped commands: "curl ... | jq ..."
  // Take the curl portion, warn about the pipe.
  const pipeIdx = args.indexOf("|");
  let pipedCommand: string[] | undefined;
  if (pipeIdx !== -1) {
    pipedCommand = args.slice(pipeIdx + 1);
    args = args.slice(0, pipeIdx);
    warnings.push({
      flag: "|",
      message: `pipe to "${pipedCommand.join(" ")}" ignored on Workers; post-processing must be done in format views`,
    });
  }

  if (args[0] !== "curl") {
    return {
      warnings,
      error: `not a curl command: "${args[0]}"; Workers can only execute HTTP requests`,
    };
  }

  let method = "GET";
  const headers: Record<string, string> = {};
  let body: string | undefined;
  let url: string | undefined;
  let followRedirects = false;

  let i = 1;
  while (i < args.length) {
    const arg = args[i]!;

    // Method
    if (arg === "-X" || arg === "--request") {
      method = args[++i]?.toUpperCase() ?? "GET";
      i++;
      continue;
    }

    // Header
    if (arg === "-H" || arg === "--header") {
      const h = args[++i];
      if (h) {
        const colon = h.indexOf(":");
        if (colon > 0) {
          headers[h.slice(0, colon).trim()] = h.slice(colon + 1).trim();
        }
      }
      i++;
      continue;
    }

    // Data/body
    if (arg === "-d" || arg === "--data" || arg === "--data-raw") {
      body = args[++i] ?? "";
      if (method === "GET") method = "POST";
      i++;
      continue;
    }
    if (arg === "--data-binary") {
      body = args[++i] ?? "";
      if (method === "GET") method = "POST";
      i++;
      continue;
    }

    // Follow redirects
    if (arg === "-L" || arg === "--location") {
      followRedirects = true;
      i++;
      continue;
    }

    // Output to file — not supported
    if (arg === "-o" || arg === "--output") {
      warnings.push({
        flag: "-o",
        message: "file output not supported on Workers",
      });
      i += 2;
      continue;
    }

    // Silent/fail flags — no-ops for fetch
    if (
      arg === "-f" ||
      arg === "--fail" ||
      arg === "-s" ||
      arg === "--silent" ||
      arg === "-S" ||
      arg === "--show-error" ||
      arg === "-sS" ||
      arg === "-fsSL" ||
      arg === "-fsSl" ||
      arg === "-fsS" ||
      arg === "--compressed" ||
      arg === "--fail-with-body"
    ) {
      // Handle combined short flags
      if (arg.startsWith("-") && !arg.startsWith("--") && arg.length > 2) {
        if (arg.includes("L")) followRedirects = true;
      }
      i++;
      continue;
    }

    // Skip combined short opts we know about
    if (arg.startsWith("-") && !arg.startsWith("--") && arg.length > 1) {
      const flags = arg.slice(1);
      let knownAll = true;
      for (const ch of flags) {
        if ("fsSLl".includes(ch)) {
          if (ch === "L" || ch === "l") followRedirects = true;
        } else {
          knownAll = false;
        }
      }
      if (knownAll) {
        i++;
        continue;
      }
    }

    // Unknown flags with value
    if (arg.startsWith("-")) {
      warnings.push({
        flag: arg,
        message: `unsupported curl flag "${arg}" ignored`,
      });
      // Heuristic: skip next arg if it doesn't look like a flag or URL
      if (i + 1 < args.length && !args[i + 1]!.startsWith("-") && !looksLikeURL(args[i + 1]!)) {
        i += 2;
      } else {
        i++;
      }
      continue;
    }

    // Positional arg = URL
    if (!url) {
      url = arg;
    }
    i++;
  }

  if (!url) {
    return { warnings, error: "no URL found in curl command" };
  }

  return {
    request: { url, method, headers, body, followRedirects },
    warnings,
  };
}

function looksLikeURL(s: string): boolean {
  return s.startsWith("http://") || s.startsWith("https://");
}

// shellSplit splits a shell command string into argv, handling single and
// double quotes. Simplified — does not handle all shell escaping edge cases,
// but covers the patterns used in api-cli configs.
export function shellSplit(s: string): string[] {
  const args: string[] = [];
  let i = 0;
  while (i < s.length) {
    // Skip whitespace
    while (i < s.length && /\s/.test(s[i]!)) i++;
    if (i >= s.length) break;

    let arg = "";
    while (i < s.length && !/\s/.test(s[i]!)) {
      const ch = s[i]!;

      if (ch === "'") {
        // Single-quoted string — no escaping inside
        i++;
        while (i < s.length && s[i] !== "'") {
          arg += s[i];
          i++;
        }
        i++; // skip closing '
        continue;
      }

      if (ch === '"') {
        // Double-quoted string
        i++;
        while (i < s.length && s[i] !== '"') {
          if (s[i] === "\\" && i + 1 < s.length) {
            const next = s[i + 1]!;
            if (next === '"' || next === "\\" || next === "$" || next === "`") {
              arg += next;
              i += 2;
              continue;
            }
          }
          arg += s[i];
          i++;
        }
        i++; // skip closing "
        continue;
      }

      if (ch === "\\" && i + 1 < s.length) {
        arg += s[i + 1];
        i += 2;
        continue;
      }

      arg += ch;
      i++;
    }
    args.push(arg);
  }
  return args;
}
