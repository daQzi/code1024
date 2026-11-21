# New API compatibility runtime

Upstream: <https://github.com/QuantumNous/new-api>

Revision: `48f9df96ea7c68eae04a0f3687869f232f48929f`

The `jsplugin` implementation and its upstream regression tests, `contract`
channel settings, `contracttypes` endpoint definitions, and `builtin` scripts
are derived from this revision. They are licensed under **AGPL-3.0-or-later**;
see `LICENSE`. The surrounding repository's LGPL notice does not replace
these terms. Preserve the applicable notices and source-offer obligations
when distributing or operating a combined application.

Local adaptations replace New API host imports with Sub2API bridges for JSON,
logging and immutable limits. No New API database, account, balance or server
implementation is imported. `contract` contains the upstream RelayKit route
contract; downstream protocol conversions must continue through shared codecs.

The local engine checks cancellation before and after admission, so a request
already cancelled cannot randomly win a ready semaphore and execute a hook.

The included scripts establish a pinned compatibility baseline. A successful
compile or fixture is not evidence that a live upstream, its credentials,
billing, storage, or every protocol is supported by the Sub2API host.

The `sub2api-dreamina` and `sub2api-official5` builtins are code1024 adapters,
written against the public channel API documentation on 2026-11-21. They are
not scripts from the pinned New API revision and use AGPL-3.0-or-later as well.
Corresponding source for this distribution is available at
<https://github.com/daQzi/code1024> (use the matching release tag).
