import { readFileSync } from "node:fs";

// Scopes are repository-specific and live in commitlint-scopes.json so the
// rule logic below stays identical across repositories.
const scopes = JSON.parse(
  readFileSync(new URL("./commitlint-scopes.json", import.meta.url), "utf8"),
);

const emojiByType = {
  feat: "✨",
  fix: "🐛",
  refactor: "♻️",
  perf: "⚡️",
  docs: "📝",
  test: "✅",
  build: "📦",
  ci: "👷",
  chore: "🔧",
  revert: "⏪",
};

const scopedTypes = new Set(["feat", "fix", "refactor", "perf"]);

const emojiPlugin = {
  rules: {
    "emoji-match-type": ({ type, subject }) => {
      if (!type || !subject || !emojiByType[type]) {
        return [true];
      }

      const expected = emojiByType[type];

      return [
        subject.startsWith(`${expected} `),
        `subject must start with "${expected} " for type "${type}"`,
      ];
    },

    "scope-required-for-type": ({ type, scope }) => {
      if (!type || !scopedTypes.has(type)) {
        return [true];
      }

      return [Boolean(scope), `scope is required for type "${type}"`];
    },
  },
};

export default {
  extends: ["@commitlint/config-conventional"],
  plugins: [emojiPlugin],
  rules: {
    "emoji-match-type": [2, "always"],
    "header-max-length": [2, "always", 100],
    "scope-case": [2, "always", "kebab-case"],
    "scope-enum": [2, "always", scopes],
    "scope-required-for-type": [2, "always"],
    "subject-empty": [2, "never"],
    "subject-full-stop": [2, "never", "."],
    "type-enum": [2, "always", Object.keys(emojiByType)],
  },
};
