import js from "@eslint/js";
import tseslint from "typescript-eslint";
import reactHooks from "eslint-plugin-react-hooks";

// The rules of hooks are invisible to tsc. See documentation/web-interface.md.
export default tseslint.config(
  // e2e/ is the Playwright suite: its own package, its own dependencies, not
  // part of the bundle this config lints.
  { ignores: ["src/api/schema.d.ts", "e2e/**"] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    files: ["src/**/*.{ts,tsx}"],
    plugins: { "react-hooks": reactHooks },
    rules: {
      ...reactHooks.configs.recommended.rules,
      "react-hooks/rules-of-hooks": "error",
    },
  },
);
