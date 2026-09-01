import js from "@eslint/js";
import tseslint from "typescript-eslint";
import reactHooks from "eslint-plugin-react-hooks";

// The rules of hooks are invisible to tsc: calling useQuery after an early
// return type-checks perfectly and then fails at runtime with "rendered fewer
// hooks than expected". That has already happened once here, which is why this
// config exists rather than relying on review.
export default tseslint.config(
  { ignores: ["src/api/schema.d.ts"] },
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
