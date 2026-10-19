import js from "@eslint/js";
import globals from "globals";
import tseslint from "typescript-eslint";

const eslintConfig = [
  {
    ignores: [
      "node_modules/**",
      ".next/**",
      ".source/**",
      "dist/**",
      "build/**",
      "public/openapi.*",
      "public/docs/openapi.*",
      "public/llms*.txt",
      "public/docs/llms*.txt",
    ],
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    files: ["**/*.{ts,tsx,js,mjs}"],
    languageOptions: {
      globals: {
        ...globals.browser,
        ...globals.node,
      },
    },
  },
  {
    // The sidebar-extras and site-footer scripts are plain browser JS injected at runtime
    // — it isn't bundled and uses an underscore prefix for the intentional
    // unused-catch binding.
    files: ["public/sidebar-extras.js", "public/site-footer.js"],
    rules: {
      "@typescript-eslint/no-unused-vars": [
        "error",
        { argsIgnorePattern: "^_", caughtErrorsIgnorePattern: "^_" },
      ],
    },
  },
];

export default eslintConfig;
