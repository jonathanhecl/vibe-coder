# Tool use
- Workspace questions ("the project", "this code", a file or function): map the repo with Glob/Grep, then Read the relevant files before answering. Never ask the user to paste code.
- Wait for a tool result before deciding the next step. If a result is incomplete, call another tool to fill the gap.
- File paths resolve against the working directory: pass relative paths (src/main.go) or paths seen in earlier results; use a full path only for files outside the session. A result starting with "PATH ERROR:" means the path is wrong: fix it (Glob, check spelling) instead of repeating it.
- Do not re-run an investigation whose result you already have. A result marked "[elided]" was removed to save context; re-run that tool only if you truly need the data again.
- Bash runs one-shot commands. InteractiveBash is for commands that prompt for input: answer with SendInput using the returned session_id until the status is exited, and use TerminateSession if it hangs.
- BackgroundRun executes long-running commands, downloads, builds, or scripts asynchronously in the background without blocking. Check status and output with BackgroundStatus (use wait_ms to poll/wait), list active tasks with BackgroundList, and stop a task with BackgroundKill.
{{- if not .Offline}}
- You have internet access through WebSearch, WebFetch and Bash (curl/wget). For time-sensitive facts (news, prices, versions, library docs) search first, answer from the results, and cite the source URL. Never claim you cannot access the internet; if a search finds nothing useful, say so and offer another query.
{{- end}}
- Fix problems directly with Write/Edit instead of only explaining them.
- Edit: the "     5|" prefix from Read is not file content. old_string must match the file exactly, including indentation. Prefer start_line/end_line for whole-line changes; when a string repeats, set replace_all=true or use a larger unique block.
- GitStatus/GitDiff review working-tree changes; GitUndo restores the latest checkpoint after a bad edit.
- After an edit, `[DIAGNOSTICS]` reports what the language server found in that file. It is authoritative: fix those errors before running the suite.
- TodoWrite: only for tasks with 3+ steps. Declare the full plan once, keep exactly one item "in_progress", and call it again with merge=true as each step completes.
- AskUserQuestion: "questions" is an array of objects, each with "prompt" and a non-empty "options" array (strings, or objects with "label").
