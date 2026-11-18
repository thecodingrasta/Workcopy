# Conflict content and recovery

Workcopy collections are paged: use `pages` totals and `nextOffset` until complete before concluding a path or conflict is absent. Pagination is not a snapshot token. After concurrent changes, reread status and current generation.

Conflict `workcopy_content` reads are base64 chunks, limited to 65,536 bytes each. Use the exact current token and side (`base`, `local`, or `upstream`), advance `byteOffset` until `nextOffset` is `-1`, assemble bytes, then compare the full-file SHA-256 reported by Workcopy. A chunk hash alone is not the content hash.

Supported `workcopy_resolve` modes are `local`, `upstream`, `delete`, and `file`. File mode reads only a repository-relative file inside the owned lane. Resolve one conflict at a time and reread status; never treat one successful resolution as resolving the entire lane.

Busy/stale errors require coordination and a fresh read. Interrupted Workcopy operations may be recovered with `workcopy_recover`. Never manually remove a journal, registry entry, lock, conflict, or review receipt. Preserve the lane and report a blocked recovery.
