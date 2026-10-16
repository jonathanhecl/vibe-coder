package permissions

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Gravity expresses how much damage a shell command can do.
//
// It is the deterministic floor under the decision model: the level is derived
// from the command text alone, before any model review, so an irreversible
// command can never be auto-approved just because a model sounded confident.
// The scale is deliberately coarse, because four levels answer the four
// questions a user actually asks: may this run silently (Read), do I want to
// see it (Write), is it about to destroy something (Destructive), and can that
// not be undone (Catastrophic).
type Gravity int

const (
	// GravityUnknown means the command was not recognised. It prompts exactly
	// like a write and is never auto-approved.
	GravityUnknown Gravity = iota
	// GravityRead only inspects: it never mutates the filesystem.
	GravityRead
	// GravityWrite creates or updates files, leaving existing content
	// recoverable.
	GravityWrite
	// GravityDestructive deletes or overwrites data that already exists.
	GravityDestructive
	// GravityCatastrophic is irreversible or system-level damage. It is always
	// confirmed: an approved session rule, yes mode, assisted mode and an
	// unattended mission never bypass it.
	GravityCatastrophic
)

// Label is the short uppercase tag shown in the permission prompt.
func (g Gravity) Label() string {
	switch g {
	case GravityRead:
		return "READ"
	case GravityWrite:
		return "WRITE"
	case GravityDestructive:
		return "DESTRUCTIVE"
	case GravityCatastrophic:
		return "CATASTROPHIC"
	default:
		return "UNCLASSIFIED"
	}
}

// Warn is the prompt line explaining why a command weighs more than an ordinary
// write, or "" when it deserves no extra emphasis.
func (g Gravity) Warn() string {
	switch g {
	case GravityDestructive:
		return "deletes or overwrites data that already exists"
	case GravityCatastrophic:
		return "irreversible: can destroy data or the system"
	default:
		return ""
	}
}

// isShellTool reports whether a tool runs a shell command string. All of them
// carry the command in the "command" parameter and must share one policy.
func isShellTool(tool string) bool {
	switch tool {
	case "bash", "interactivebash", "backgroundrun", "sendinput":
		return true
	}
	return false
}

// needsAlwaysConfirmBash reports whether a shell command must be confirmed by
// the user even in yes mode, under an approved session rule, under an approved
// persistent rule, or during an unattended mission.
func needsAlwaysConfirmBash(command, ws string) bool {
	return CommandGravity(command, ws) == GravityCatastrophic
}

// CommandGravity classifies a shell command by how much damage it can do.
// ws is the workspace domain: it decides whether a write target already exists
// and whether the paths a reader touches are reachable at all.
func CommandGravity(command, ws string) Gravity {
	return classifyLine(strings.TrimSpace(command), ws, 0)
}

// rank orders gravities for aggregation. A command line is only as safe as its
// least understood part, so an unclassified element outranks every known level
// except proven catastrophe: "I cannot tell" must never collapse into "safe".
func (g Gravity) rank() int {
	switch g {
	case GravityRead:
		return 0
	case GravityWrite:
		return 1
	case GravityDestructive:
		return 2
	case GravityUnknown:
		return 3
	default:
		return 4
	}
}

func classifyLine(command, ws string, depth int) Gravity {
	if strings.TrimSpace(command) == "" {
		return GravityUnknown
	}
	if depth > 4 {
		return GravityUnknown
	}
	if catastrophicCommand(command) {
		return GravityCatastrophic
	}
	// The token stream does not expand substitutions, so a reader here may be
	// reading the output of anything at all. Unclassified, never auto-allowed.
	if hasCommandSubstitution(command) {
		return GravityUnknown
	}

	seen := false
	worst := GravityRead
	consider := func(g Gravity) {
		if !seen || g.rank() > worst.rank() {
			worst = g
		}
		seen = true
	}
	for _, words := range tokenizeCommandSubcommands(command) {
		if len(words) == 0 {
			continue
		}
		consider(classifyWords(words, ws, depth+1))
	}
	// A redirection writes even when every subcommand only reads.
	if g, ok := redirectionGravity(command, ws); ok {
		consider(g)
	}
	if !seen {
		return GravityUnknown
	}
	return worst
}

func classifyWords(words []string, ws string, depth int) Gravity {
	if len(words) == 0 || depth > 4 {
		return GravityUnknown
	}
	bin := binName(words[0])
	args := words[1:]

	if isCatastrophicBinary(bin) {
		return GravityCatastrophic
	}
	if isVersionOrHelpOnly(args) {
		return GravityRead
	}
	if _, ok := shellRunners[bin]; ok {
		payload, err := runnerPayload(bin, args)
		if err != nil || strings.TrimSpace(payload) == "" {
			return GravityUnknown
		}
		return classifyLine(payload, ws, depth+1)
	}

	switch bin {
	case "rm":
		return rmGravity(args, ws)
	case "cp", "mv", "install", "rsync":
		if overwritesExisting(args, ws) {
			return GravityDestructive
		}
		return GravityWrite
	case "truncate", "shred", "unlink", "kill", "pkill", "killall":
		return GravityDestructive
	case "dd":
		if ddDeviceTarget(args) {
			return GravityCatastrophic
		}
		return GravityDestructive
	case "chmod", "chown", "chgrp":
		if hasRecursiveFlag(args) {
			if targetsSystemRoot(args) {
				return GravityCatastrophic
			}
			return GravityDestructive
		}
		return GravityWrite
	case "sort":
		if hasAnyFlag(args, "-o", "--output") {
			return GravityWrite
		}
		return readerGravity(words, ws)
	case "sed":
		if hasAnyFlag(args, "-i", "--in-place") {
			return GravityWrite
		}
		return readerGravity(words, ws)
	case "find", "fd", "fdfind":
		if hasAnyFlag(args, "-delete", "-exec", "-execdir", "-ok", "-okdir",
			"-fprint", "-fprint0", "-fprintf", "-fls", "-x", "-X", "--exec", "--exec-batch") {
			return GravityDestructive
		}
		return readerGravity(words, ws)
	case "git":
		return gitGravity(args)
	case "go":
		return goGravity(args)
	case "docker", "podman":
		return containerGravity(args)
	case "systemctl":
		if hasAnyFlag(args, "disable", "mask") {
			return GravityDestructive
		}
		return GravityWrite
	case "launchctl":
		if hasAnyFlag(args, "unload", "bootout", "remove") {
			return GravityDestructive
		}
		return GravityWrite
	case "diskutil":
		for _, a := range args {
			switch a {
			case "eraseDisk", "eraseVolume", "reformat", "zeroDisk", "partitionDisk":
				return GravityCatastrophic
			}
		}
		return GravityUnknown
	case "crontab":
		if hasAnyFlag(args, "-r") {
			return GravityDestructive
		}
		return GravityWrite
	case "history":
		if hasAnyFlag(args, "-c") {
			return GravityDestructive
		}
		return GravityRead
	case "npm", "yarn", "pnpm", "cargo", "gem", "bun":
		return packageGravity(args)
	}

	if _, ok := readOnlyBinaries[bin]; ok {
		return readerGravity(words, ws)
	}
	if _, ok := writingBinaries[bin]; ok {
		return GravityWrite
	}
	return GravityUnknown
}

// readerGravity returns GravityRead only when every candidate path the reader
// touches is reachable, that is inside the workspace or in system temp.
// Reading somewhere else is a different decision, so it stays unclassified.
func readerGravity(words []string, ws string) Gravity {
	if !readerPathsReachable(words, ws) {
		return GravityUnknown
	}
	return GravityRead
}

// readerPathsReachable reports whether every path a reader names is inside the
// workspace or in system temp.
func readerPathsReachable(words []string, ws string) bool {
	for _, candidate := range extractCandidatePathsFromSubcommand(words) {
		clean := cleanCandidatePath(candidate, ws)
		if clean == "" {
			continue
		}
		if !IsPathInsideDomain(ws, clean) && !isAllowedSystemPath(clean, ws) {
			return false
		}
	}
	return true
}

// rmGravity separates deleting something recoverable from deleting a tree that
// cannot be recreated.
func rmGravity(args []string, ws string) Gravity {
	targets := commandTargets(args)
	if !hasRecursiveFlag(args) {
		return GravityDestructive
	}
	for _, t := range targets {
		if isRootishTarget(t) || isSystemRootTarget(t) || isWorkspaceRootTarget(t, ws) {
			return GravityCatastrophic
		}
	}
	return GravityDestructive
}

func gitGravity(args []string) Gravity {
	sub, rest := gitSubcommand(args)
	switch sub {
	case "push":
		if hasAnyFlag(rest, "--force", "-f", "--mirror") {
			return GravityCatastrophic
		}
		if hasAnyFlag(rest, "--force-with-lease", "--delete", "--prune") {
			return GravityDestructive
		}
		return GravityWrite
	case "filter-branch", "filter-repo":
		return GravityCatastrophic
	case "reset":
		if hasAnyFlag(rest, "--hard", "--merge", "--keep") {
			return GravityDestructive
		}
		return GravityWrite
	case "clean":
		if hasRecursiveFlag(rest) || hasForceFlag(rest) {
			return GravityDestructive
		}
		return GravityWrite
	case "checkout", "restore":
		if hasAnyFlag(rest, ".", "--") || len(rest) == 0 {
			return GravityDestructive
		}
		return GravityWrite
	case "branch":
		if hasAnyFlag(rest, "-D", "-d", "--delete") {
			return GravityDestructive
		}
		return readerGravity([]string{"git", "branch"}, "")
	case "tag":
		if hasAnyFlag(rest, "-d", "--delete") {
			return GravityDestructive
		}
		return readerGravity([]string{"git", "tag"}, "")
	case "stash":
		if hasAnyFlag(rest, "drop", "clear") {
			return GravityDestructive
		}
		if len(rest) == 0 || hasAnyFlag(rest, "list", "show") {
			return readerGravity([]string{"git", "stash"}, "")
		}
		return GravityWrite
	case "reflog":
		if hasAnyFlag(rest, "expire", "delete") {
			return GravityDestructive
		}
		return readerGravity([]string{"git", "reflog"}, "")
	case "gc":
		if hasAnyFlag(rest, "--prune=now", "--aggressive") {
			return GravityDestructive
		}
		return GravityWrite
	case "worktree":
		if hasAnyFlag(rest, "remove", "prune") {
			return GravityDestructive
		}
		return readerGravity([]string{"git", "worktree"}, "")
	case "remote":
		if hasAnyFlag(rest, "remove", "rm", "set-url") {
			return GravityWrite
		}
		return readerGravity([]string{"git", "remote"}, "")
	case "rm", "mv":
		return GravityDestructive
	case "config":
		if hasAnyFlag(rest, "--get", "--get-all", "--get-regexp", "--list", "-l") {
			return GravityRead
		}
		return GravityWrite
	case "status", "diff", "log", "show", "rev-parse", "rev-list", "ls-files",
		"describe", "shortlog", "blame", "for-each-ref", "cat-file", "show-ref",
		"grep", "diff-tree", "diff-index", "diff-files", "merge-base",
		"count-objects", "verify-pack", "check-ignore", "check-attr",
		"symbolic-ref", "var", "version", "help", "fsck", "cherry",
		"name-rev", "whatchanged":
		return GravityRead
	case "", "ls-remote", "fetch", "pull", "add", "commit", "merge", "rebase",
		"cherry-pick", "revert", "init", "clone", "switch", "apply", "am",
		"submodule", "update-ref", "notes", "bisect":
		if sub == "" {
			return GravityUnknown
		}
		return GravityWrite
	}
	return GravityUnknown
}

func goGravity(args []string) Gravity {
	sub, rest := gitSubcommand(args)
	switch sub {
	case "version":
		return GravityRead
	case "env":
		if hasAnyFlag(rest, "-w") {
			return GravityWrite
		}
		return GravityRead
	case "":
		return GravityUnknown
	}
	return GravityWrite
}

func containerGravity(args []string) Gravity {
	sub, rest := gitSubcommand(args)
	switch sub {
	case "ps", "images", "logs", "inspect", "version", "info", "top", "stats", "port":
		return GravityRead
	case "rm", "rmi", "prune", "kill", "stop", "down":
		return GravityDestructive
	case "push", "login", "logout":
		return GravityWrite
	case "system":
		if hasAnyFlag(rest, "prune") {
			return GravityDestructive
		}
		return GravityWrite
	}
	return GravityWrite
}

func packageGravity(args []string) Gravity {
	sub, _ := gitSubcommand(args)
	switch sub {
	case "publish":
		return GravityCatastrophic
	case "unpublish", "uninstall", "remove", "rm", "deprecate":
		return GravityDestructive
	case "ls", "list", "view", "info", "outdated", "why", "explain", "audit", "version":
		return GravityRead
	}
	return GravityWrite
}

// gitSubcommand returns the first non-flag argument and the arguments after it,
// skipping the flag/value pairs git accepts before the subcommand.
func gitSubcommand(args []string) (string, []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			switch a {
			case "-C", "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path":
				i++
			}
			continue
		}
		return a, args[i+1:]
	}
	return "", nil
}

// shellRunners execute another command in place of themselves. The gravity of
// the line is the gravity of the command they run, never of the wrapper, so
// `nice sudo rm -rf /` cannot hide behind `nice`.
var shellRunners = map[string]struct{}{
	"sh": {}, "bash": {}, "zsh": {}, "dash": {}, "ksh": {}, "ash": {}, "fish": {},
	"env": {}, "nice": {}, "nohup": {}, "stdbuf": {}, "caffeinate": {},
	"time": {}, "timeout": {}, "watch": {}, "xargs": {}, "command": {}, "exec": {},
	"eval": {},
}

// runnerPayload resolves the command a shell runner delegates to. When the
// payload cannot be resolved the caller must stay unclassified rather than
// assume the wrapper is harmless.
func runnerPayload(bin string, args []string) (string, error) {
	switch bin {
	case "sh", "bash", "zsh", "dash", "ksh", "ash", "fish":
		for i := 0; i < len(args); i++ {
			if args[i] == "-c" && i+1 < len(args) {
				return args[i+1], nil
			}
		}
		// A bare or script-running shell is arbitrary code.
		return "", errUnresolvedRunner
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") || strings.Contains(a, "=") || isDurationLike(a) {
			continue
		}
		return strings.Join(args[i:], " "), nil
	}
	return "", errUnresolvedRunner
}

var errUnresolvedRunner = errString("permissions: shell runner has no resolvable command")

func isDurationLike(s string) bool {
	if s == "" {
		return false
	}
	digits := 0
	for i, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == '.' && i > 0:
		case strings.ContainsRune("smhd", r) && i == len(s)-1:
		default:
			return false
		}
	}
	return digits > 0
}

// readOnlyBinaries are inspection-only programs: they read files, query system
// state, or format text, and never modify the filesystem on their own. A few of
// them can write through specific flags (find -delete, sort -o, sed -i), which
// classifyWords rejects before reaching this table.
var readOnlyBinaries = map[string]struct{}{
	"ls": {}, "dir": {}, "vdir": {}, "pwd": {}, "cat": {}, "bat": {}, "batcat": {},
	"head": {}, "tail": {}, "tac": {}, "nl": {}, "wc": {}, "less": {}, "more": {},
	"file": {}, "stat": {}, "du": {}, "df": {}, "tree": {}, "realpath": {},
	"readlink": {}, "which": {}, "whereis": {}, "type": {},
	"whoami": {}, "id": {}, "groups": {}, "hostname": {}, "uname": {}, "sw_vers": {},
	"date": {}, "uptime": {}, "echo": {}, "printf": {}, "printenv": {},
	"basename": {}, "dirname": {}, "seq": {}, "expr": {}, "column": {}, "cut": {},
	"paste": {}, "fold": {}, "tr": {}, "uniq": {}, "comm": {}, "diff": {}, "cmp": {},
	"jq": {}, "yq": {}, "base64": {}, "xxd": {}, "od": {}, "strings": {}, "hexdump": {},
	"md5sum": {}, "md5": {}, "shasum": {}, "sha256sum": {}, "sha1sum": {}, "cksum": {},
	"grep": {}, "egrep": {}, "fgrep": {}, "rg": {}, "ag": {}, "ack": {},
	"sed": {}, "sort": {}, "find": {}, "fd": {}, "fdfind": {},
	"ps": {}, "top": {}, "free": {}, "vm_stat": {}, "lsof": {}, "netstat": {},
	"ifconfig": {}, "ipconfig": {}, "tty": {}, "locale": {}, "alias": {},
	"getent": {}, "man": {}, "true": {}, "false": {},
}

// writingBinaries are programs whose normal mode of operation changes files.
// They cannot be auto-approved, but naming them gives the prompt a WRITE label
// instead of an opaque UNCLASSIFIED one.
var writingBinaries = map[string]struct{}{
	"mkdir": {}, "touch": {}, "tee": {}, "ln": {}, "rmdir": {},
	"zip": {}, "unzip": {}, "tar": {}, "gzip": {}, "gunzip": {}, "bzip2": {},
	"xz": {}, "gofmt": {}, "make": {}, "cmake": {}, "ninja": {},
	"gcc": {}, "g++": {}, "clang": {}, "cc": {}, "rustc": {}, "rustup": {},
	"node": {}, "npx": {}, "deno": {}, "tsc": {}, "eslint": {}, "prettier": {},
	"vite": {}, "webpack": {}, "jest": {}, "vitest": {}, "playwright": {},
	"cypress": {}, "python": {}, "python3": {}, "pip": {}, "pip3": {}, "pytest": {},
	"uv": {}, "poetry": {}, "ruby": {}, "bundle": {}, "rake": {},
	"java": {}, "javac": {}, "mvn": {}, "gradle": {}, "sbt": {}, "dotnet": {},
	"swift": {}, "swiftc": {}, "xcodebuild": {}, "kubectl": {}, "helm": {},
	"terraform": {}, "ansible": {}, "vagrant": {}, "aws": {}, "gcloud": {},
	"defaults": {}, "open": {}, "pbcopy": {}, "pbpaste": {}, "osascript": {},
}

// catastrophicBinaries are programs whose mere invocation is a hard stop.
var catastrophicBinaries = map[string]struct{}{
	"sudo": {}, "doas": {}, "su": {}, "shutdown": {}, "reboot": {}, "halt": {},
	"poweroff": {}, "fdisk": {}, "parted": {}, "newfs": {},
}

func isCatastrophicBinary(bin string) bool {
	if _, ok := catastrophicBinaries[bin]; ok {
		return true
	}
	return strings.HasPrefix(bin, "mkfs")
}

var (
	forkBombRe  = regexp.MustCompile(`:\s*\(\s*\)\s*\{[^}]*\|[^}]*&[^}]*\}\s*;\s*:`)
	pipeShellRe = regexp.MustCompile(`(?i)(\bcurl\b|\bwget\b|\bfetch\b|\bhttpie\b|\bhttp\b)[^|;&]*\|\s*(sudo\s+)?([a-z0-9_/.-]*/)?(ba|z|k|da|fi)?sh\b`)
	devWriteRe  = regexp.MustCompile(`(?i)>\s*/dev/(sd|disk|rdisk|nvme|hd)`)
)

// catastrophicCommand holds the patterns that only make sense against the whole
// command line: a fork bomb has no recognisable binary, and a pipe into a shell
// interpreter turns a download into execution.
func catastrophicCommand(command string) bool {
	if forkBombRe.MatchString(command) {
		return true
	}
	if pipeShellRe.MatchString(command) {
		return true
	}
	return devWriteRe.MatchString(command)
}

func hasCommandSubstitution(command string) bool {
	return strings.Contains(command, "$(") || strings.Contains(command, "`") ||
		strings.Contains(command, "<(")
}

// redirection holds one output redirection found on a command line.
type redirection struct {
	target string
	append bool
}

// redirectionGravity treats an output redirection as a write, and as a
// destructive one when it truncates a file that already holds data. That is the
// difference between "dump this into a new file" and "dump this over my file".
// ok is false when the command has no output redirection at all.
func redirectionGravity(command, ws string) (Gravity, bool) {
	redirects := parseRedirections(command)
	if len(redirects) == 0 {
		return GravityUnknown, false
	}
	worst := GravityWrite
	for _, r := range redirects {
		if !r.append && targetExists(r.target, ws) {
			return GravityDestructive, true
		}
	}
	return worst, true
}

func parseRedirections(command string) []redirection {
	var out []redirection
	for _, words := range tokenizeCommandSubcommands(command) {
		for i := 0; i < len(words); i++ {
			op, rest := splitLeadingRedirect(words[i])
			if op == "" {
				continue
			}
			target := rest
			if target == "" {
				if i+1 >= len(words) {
					continue
				}
				target = words[i+1]
				i++
			}
			target = strings.Trim(strings.TrimSpace(target), `"'`)
			// "2>&1" and friends duplicate a descriptor instead of naming a file.
			if target == "" || strings.HasPrefix(target, "&") || isVirtualDevice(target) {
				continue
			}
			out = append(out, redirection{target: target, append: strings.Contains(op, ">>")})
		}
	}
	return out
}

func splitLeadingRedirect(tok string) (op, rest string) {
	for _, candidate := range []string{"2>>", "1>>", "&>>", "2>", "1>", "&>", ">>", ">"} {
		if strings.HasPrefix(tok, candidate) {
			return candidate, strings.TrimPrefix(tok, candidate)
		}
	}
	return "", ""
}

func targetExists(target, ws string) bool {
	clean := cleanCandidatePath(target, ws)
	if clean == "" {
		return false
	}
	fi, err := os.Stat(clean)
	return err == nil && !fi.IsDir()
}

// overwritesExisting reports whether a copy-like command names an existing file
// as its destination.
func overwritesExisting(args []string, ws string) bool {
	targets := commandTargets(args)
	if len(targets) == 0 {
		return false
	}
	return targetExists(targets[len(targets)-1], ws)
}

// ddDeviceTarget reports whether a dd writes straight onto a raw device. A
// virtual device such as /dev/null is the harmless exception.
func ddDeviceTarget(args []string) bool {
	for _, a := range args {
		if !strings.HasPrefix(a, "of=") {
			continue
		}
		target := strings.TrimPrefix(a, "of=")
		if strings.HasPrefix(target, "/dev/") && !isVirtualDevice(target) {
			return true
		}
	}
	return false
}

func binName(word string) string {
	return strings.TrimSuffix(strings.ToLower(filepath.Base(strings.TrimSpace(word))), ".exe")
}

func hasAnyFlag(args []string, flags ...string) bool {
	for _, a := range args {
		for _, f := range flags {
			if a == f {
				return true
			}
		}
	}
	return false
}

func hasRecursiveFlag(args []string) bool {
	for _, a := range args {
		if a == "--recursive" {
			return true
		}
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") &&
			strings.ContainsAny(a[1:], "rR") {
			return true
		}
	}
	return false
}

func hasForceFlag(args []string) bool {
	for _, a := range args {
		if a == "--force" {
			return true
		}
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") &&
			strings.Contains(a[1:], "f") {
			return true
		}
	}
	return false
}

// commandTargets returns the operand words of a command, skipping flags and any
// value written with them.
func commandTargets(args []string) []string {
	var out []string
	afterSeparator := false
	for _, a := range args {
		if a == "--" {
			afterSeparator = true
			continue
		}
		if !afterSeparator && strings.HasPrefix(a, "-") && len(a) > 1 {
			continue
		}
		out = append(out, strings.Trim(strings.TrimSpace(a), `"'`))
	}
	return out
}

// isRootishTarget reports whether a delete target is the whole tree: a
// filesystem root, the home directory, or a top-level glob.
func isRootishTarget(target string) bool {
	t := strings.Trim(strings.TrimSpace(target), `"'`)
	switch t {
	case "/", "/*", "~", "~/", "~/*", "~/**",
		"$HOME", "$HOME/", "$HOME/*", "${HOME}", "${HOME}/", "${HOME}/*",
		"*", "**", ".*", "./*", "./**", ".", "..", "../", "/.", "\\":
		return true
	}
	return false
}

// isSystemRootTarget reports whether a delete target is a top-level system
// directory itself, as opposed to something inside it.
func isSystemRootTarget(target string) bool {
	t := strings.Trim(strings.TrimSpace(target), `"'`)
	if t == "/" || t == `\` {
		return true
	}
	if !filepath.IsAbs(t) {
		return false
	}
	trimmed := strings.Trim(strings.TrimLeft(t, `/\`), `/\`)
	if trimmed == "" || strings.ContainsAny(trimmed, `/\`) {
		return false
	}
	for _, root := range []string{
		"etc", "bin", "sbin", "usr", "var", "lib", "opt", "boot", "dev", "proc",
		"sys", "root", "home", "System", "Library", "Applications", "Users",
	} {
		if strings.EqualFold(trimmed, root) {
			return true
		}
	}
	return false
}

// targetsSystemRoot reports whether any operand of a command names a top-level
// system directory.
func targetsSystemRoot(args []string) bool {
	for _, t := range commandTargets(args) {
		if isSystemRootTarget(t) {
			return true
		}
	}
	return false
}

// isWorkspaceRootTarget reports whether a delete target is the workspace itself
// or one of its ancestors, which would take the whole project with it.
func isWorkspaceRootTarget(target, ws string) bool {
	if strings.TrimSpace(ws) == "" {
		return false
	}
	clean := cleanCandidatePath(target, ws)
	if clean == "" {
		return false
	}
	root := filepath.Clean(ws)
	if abs, err := filepath.Abs(root); err == nil {
		root = filepath.Clean(abs)
	}
	return isPathUnder(clean, root)
}

// isVersionOrHelpOnly recognises the one argument form that cannot mutate
// anything. Only the long forms qualify: "-v" is a version flag on some
// programs and a verbose flag that drops into a REPL on others.
func isVersionOrHelpOnly(args []string) bool {
	if len(args) != 1 {
		return false
	}
	switch args[0] {
	case "--version", "--help", "--usage", "-version", "-help":
		return true
	}
	return false
}

type errString string

func (e errString) Error() string { return string(e) }
