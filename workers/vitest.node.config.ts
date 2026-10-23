import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    include: ["test/comparative.test.ts"],
  },
});
