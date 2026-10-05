const types = [
  "build",
  "chore",
  "ci",
  "docs",
  "feat",
  "fix",
  "perf",
  "refactor",
  "revert",
  "test",
];

const scopes = [
  "agent",
  "api",
  "auth",
  "console",
  "contracts",
  "conversation",
  "db",
  "deploy",
  "deps",
  "docs",
  "evaluation",
  "ingestion",
  "knowledge",
  "platform",
  "repo",
  "retrieval",
  "runtime",
  "security",
  "tenant",
  "widget",
  "worker",
];

export default {
  extends: ["@commitlint/config-conventional"],
  rules: {
    "body-max-line-length": [2, "always", 100],
    "footer-max-line-length": [2, "always", 100],
    "header-max-length": [2, "always", 100],
    "scope-empty": [2, "never"],
    "scope-enum": [2, "always", scopes],
    "subject-case": [
      2,
      "never",
      ["sentence-case", "start-case", "pascal-case", "upper-case"],
    ],
    "subject-empty": [2, "never"],
    "type-enum": [2, "always", types],
  },
};
