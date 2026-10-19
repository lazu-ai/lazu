# Private braces security fork

This directory vendors the exact published `braces@3.0.3` runtime from
<https://registry.npmjs.org/braces/-/braces-3.0.3.tgz>. Its upstream project is
<https://github.com/micromatch/braces>. The original MIT license, copyright,
author metadata and README are retained.

The private local package is `@lazu/braces@3.0.3-lazu.1`. It is not an upstream
release or a published npm package. `UPSTREAM.json` records the registry SHA512
and original per-file SHA256 values; `PATCH.diff` records every runtime and
package metadata change. The regression reverses that patch and verifies every
original file hash. The published tarball SHA512 was verified before vendoring.

The fix for [GHSA-vfj7-8cjw-p6xm](https://github.com/advisories/GHSA-vfj7-8cjw-p6xm)
bounds parser container nesting before recursive walkers run. Direct AST
compile, expand and stringify paths use an iterative validator with a fixed
100-level depth bound, cycle detection, and a finite `2 * MAX_LENGTH + 3` node
budget. Excessive inputs raise a controlled `SyntaxError` with a stable error
code. These safety bounds cannot be raised through caller options. Normal glob,
range, quoted and escaped patterns retain upstream behavior. Patterns requiring
more than 99 nested containers are deliberately rejected.

The package is used through a local `braces` override. Tests verify installed
micromatch and Tailwind chokidar consumers resolve this source and reject the
malicious input. No advisory ignore or audit threshold change is used. Remove
this fork when a reviewed upstream release provides the equivalent safeguards.
