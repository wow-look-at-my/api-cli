// Warnings — tracks features from the Go CLI that are unavailable or
// degraded on Cloudflare Workers.

export interface UnsupportedFeature {
  feature: string;
  reason: string;
  severity: "warning" | "error";
}

// analyzeConfig scans a config for features that won't work on Workers.
export function analyzeConfig(cfg: unknown): UnsupportedFeature[] {
  const warnings: UnsupportedFeature[] = [];
  const c = cfg as Record<string, unknown>;

  if (c["cwd"]) {
    warnings.push({
      feature: "cwd (top-level)",
      reason: "Workers have no filesystem; working directory is ignored",
      severity: "warning",
    });
  }
  if (c["stdin"]) {
    warnings.push({
      feature: "stdin (top-level)",
      reason: "Workers have no process stdin; stdin templates are ignored",
      severity: "warning",
    });
  }

  walkNodes(c["commands"] as unknown[], "", warnings);
  return warnings;
}

function walkNodes(
  cmds: unknown[] | undefined,
  prefix: string,
  warnings: UnsupportedFeature[],
): void {
  if (!cmds) return;
  for (const raw of cmds) {
    const c = raw as Record<string, unknown>;
    const name = prefix ? `${prefix}/${c["name"]}` : String(c["name"]);

    if (c["cwd"]) {
      warnings.push({
        feature: `${name}: cwd`,
        reason: "Workers have no filesystem; cwd is ignored",
        severity: "warning",
      });
    }
    if (c["stdin"]) {
      warnings.push({
        feature: `${name}: stdin`,
        reason: "Workers have no process stdin; stdin is ignored",
        severity: "warning",
      });
    }
    if (c["confirm"]) {
      warnings.push({
        feature: `${name}: confirm`,
        reason: "Workers cannot prompt interactively; confirm is skipped",
        severity: "warning",
      });
    }

    const cmd = c["command"];
    if (typeof cmd === "string" && !cmd.includes("curl")) {
      warnings.push({
        feature: `${name}: command`,
        reason: `non-curl command may not work on Workers (renders to shell command)`,
        severity: "warning",
      });
    }

    if (c["preconditions"]) {
      const preconds = c["preconditions"] as string[];
      for (const p of preconds) {
        if (p.includes("fileExists") || p.includes("dirExists")) {
          warnings.push({
            feature: `${name}: precondition`,
            reason: "fileExists/dirExists always return false on Workers",
            severity: "warning",
          });
        }
      }
    }

    for (const step of (c["steps"] ?? []) as Record<string, unknown>[]) {
      if (step["cwd"]) {
        warnings.push({
          feature: `${name}/step:${step["name"]}: cwd`,
          reason: "Workers have no filesystem",
          severity: "warning",
        });
      }
      if (step["stdin"]) {
        warnings.push({
          feature: `${name}/step:${step["name"]}: stdin`,
          reason: "Workers have no process stdin",
          severity: "warning",
        });
      }
    }

    walkNodes(c["commands"] as unknown[], name, warnings);
  }
}
