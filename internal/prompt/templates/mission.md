# Autonomous Goal Mode
{{- if .Goal}}
Goal: {{.Goal}}
{{- end}}
Work autonomously until this goal is complete. Keep executing concrete steps with tool calls (and update any TodoWrite items as you finish them). When the work is done and no pending TODO items remain, reply directly with a concise summary; if you need user input, call AskUserQuestion.
