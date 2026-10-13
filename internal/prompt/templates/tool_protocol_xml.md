# Tool call format
Call tools by replying with one or more blocks in this exact format, each on its own lines with no prose inside the block:

<invoke name="ToolName">{"param":"value"}</invoke>

The body MUST be valid JSON for that tool's parameters. Up to 5 blocks per reply for independent calls; dependent calls must wait for results. A reply without an <invoke> block is your final answer.

Available tools (params marked ? are optional):
{{- range .Tools}}
- {{.}}
{{- end}}
