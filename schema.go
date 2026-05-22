package main

// jsonSchema is the JSON Schema (draft 2020-12) describing `stalewood --json`
// output. It is printed by --json-schema. TestJSONSchema keeps the worktree
// property set in sync with the Worktree struct.
const jsonSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://github.com/retif/stalewood/schemas/json-report.json",
  "title": "stalewood --json report",
  "description": "Report emitted by ` + "`stalewood --json`" + `, grouped by repo.",
  "type": "object",
  "required": ["root", "count", "repos"],
  "additionalProperties": false,
  "properties": {
    "root":  { "type": "string", "description": "absolute path that was scanned" },
    "count": { "type": "integer", "description": "total number of worktrees discovered" },
    "repos": {
      "type": "array",
      "description": "worktrees grouped by owning repo",
      "items": { "$ref": "#/$defs/repo" }
    }
  },
  "$defs": {
    "repo": {
      "type": "object",
      "required": ["repo", "name", "worktrees"],
      "additionalProperties": false,
      "properties": {
        "repo":      { "type": "string", "description": "absolute repo root path" },
        "name":      { "type": "string", "description": "repo path relative to root" },
        "worktrees": { "type": "array", "items": { "$ref": "#/$defs/worktree" } }
      }
    },
    "worktree": {
      "type": "object",
      "description": "one linked git worktree and its analysis",
      "required": [
        "path", "repo", "name", "kind", "claude", "registered", "on_disk",
        "branch", "head", "base", "merged", "dirty", "detached", "size_bytes"
      ],
      "additionalProperties": false,
      "properties": {
        "path":         { "type": "string", "description": "absolute path to the worktree directory" },
        "repo":         { "type": "string", "description": "absolute path to the owning repo root" },
        "name":         { "type": "string", "description": "basename of the worktree directory" },
        "kind":         { "type": "string", "enum": ["live", "abandoned-orphan", "abandoned-stale"] },
        "claude":       { "type": "boolean", "description": "lives under a .claude/worktrees/ path" },
        "registered":   { "type": "boolean", "description": "listed by git worktree list" },
        "on_disk":      { "type": "boolean", "description": "the directory exists" },
        "locked":       { "type": "boolean", "description": "a git worktree lock is set" },
        "lock_reason":  { "type": "string", "description": "reason recorded with the lock" },
        "git_prunable": { "type": "boolean", "description": "git worktree list flags the entry prunable" },
        "branch":       { "type": "string", "description": "checked-out branch, empty when detached" },
        "head":         { "type": "string", "description": "short HEAD commit sha" },
        "base":         { "type": "string", "description": "recovered fork base, empty when unknown" },
        "base_from":    { "type": "string", "enum": ["reflog", "reflog-sha", "upstream", "auto", "flag"] },
        "merged":       { "type": "boolean", "description": "work is integrated (see merged_into)" },
        "merged_into":  { "type": "string", "description": "ref the work was found in" },
        "dirty":        { "type": "boolean", "description": "has any uncommitted change" },
        "modified":     { "type": "boolean", "description": "tracked files have changes" },
        "untracked":    { "type": "boolean", "description": "untracked files present" },
        "detached":     { "type": "boolean", "description": "HEAD is detached" },
        "size_bytes":   { "type": "integer", "description": "disk usage in bytes; -1 when not measured" },
        "error":        { "type": "string", "description": "set when the worktree could not be analyzed" }
      }
    }
  }
}`
