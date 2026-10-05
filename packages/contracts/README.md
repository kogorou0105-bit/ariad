# Frontend contracts

`@ariad/contracts` is a declaration-only package. It may export TypeScript types
and interfaces, but it has no JavaScript runtime entry point. Consumers must use
`import type`.

If Ariad later needs shared runtime constants or validation code, introduce a
separate reviewed package that emits JavaScript and declaration files instead of
turning this package into an implicit source-transpilation dependency.
