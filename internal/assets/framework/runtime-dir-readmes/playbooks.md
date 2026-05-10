# playbooks

This directory mixes **framework-shipped defaults** with **runtime state**.

Files whose names begin with `_fw-` are managed by the Nanite framework and
were placed here by `nanite-agent init`. They may be refreshed on future runs
(user modifications to `_fw-` files are preserved unless `--force` is passed).

All other files are user-ephemeral runtime state created by Nanite or by you
directly. They are not tracked by `nanite-agent init` and will not be restored
if deleted.

Do not rely on non-`_fw-` files persisting across installs.
