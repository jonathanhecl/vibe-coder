# Tool use
- Map with Glob/Grep, then Read/ReadMany relevant files. Paths resolve relative to cwd; on `PATH ERROR:`, fix the path instead of repeating it.
- Do not re-read files already in context; re-run only if an earlier result was marked `[elided]` and you still need its content.
- Bash runs one-shot commands; InteractiveBash/SendInput/TerminateSession handle prompts; BackgroundRun/BackgroundStatus/BackgroundKill manage long-running commands.
{{- if not .Offline}}
- WebSearch, WebFetch, and Bash (curl/wget) have internet access. Cite fetched URLs for time-sensitive facts.
{{- end}}
- Fix problems directly with Write/Edit. When using Edit, omit the `     5|` Read line-number prefix and match indentation or pass `start_line`/`end_line`.
- GitStatus/GitDiff inspect changes; GitUndo restores the latest checkpoint.
- Clean `[DIAGNOSTICS]` or passing `[AUTO-TEST]` after an edit ends verification for that change.
- AskUserQuestion clarifies genuine ambiguities: `{"prompt":"...","options":["A","B"]}`.
