#!/usr/bin/env python3
"""Conservative Claude Code hook: blocks obvious dangerous commands and sensitive writes.
NOT a shell parser and NOT a complete sandbox. Assumes direct tool JSON on stdin.
"""
import json
import re
import sys

SENSITIVE = re.compile(r"(^|/)(\.env(\..*)?|\.aws/credentials|id_(rsa|ed25519)|.*\.pem|.*\.tfstate(\..*)?)$", re.I)
BLOCK = [
    (r"\bterraform\s+(apply|destroy|import|state\s+rm|force-unlock)\b", "Terraform mutation needs manual approval outside Claude automated calls"),
    (r"\baws\s+[^\n]*\s(?:create-|delete-|terminate-|run-instances|put-|update-|modify-|attach-|detach-|start-|stop-)[a-z-]*\b", "AWS mutation requires explicit review and manual execution"),
    (r"\b(?:kubectl)\s+(?:delete|apply|replace|scale|rollout\s+restart)\b", "Kubernetes mutation requires approval"),
    (r"\b(?:docker\s+(?:system|volume)\s+prune|docker\s+compose\s+down\s+--volumes)\b", "Destructive Docker cleanup requires approval"),
    (r"\b(?:git\s+push\s+[^\n]*--force|git\s+reset\s+--hard|git\s+clean\s+-[a-z]*f)\b", "Destructive Git operation blocked"),
    (r"\brm\s+-[^\n]*r[^\n]*f|\brm\s+-[^\n]*f[^\n]*r", "Recursive force-delete blocked"),
    (r"\b(?:curl|wget)\b[^\n]*\|\s*(?:sudo\s+)?(?:bash|sh)\b", "Remote script piping into shell blocked"),
    (r"\b(?:aws\s+configure|gh\s+auth\s+login)\b", "Credential modifications require interactive user control"),
]

def check(payload):
    tool = payload.get('tool_name', '')
    args = payload.get('tool_input', {}) or {}
    if tool in ('Write', 'Edit', 'MultiEdit'):
        path = args.get('file_path', '')
        if path and SENSITIVE.search(path.replace('\\','/')):
            return f'Blocked write to sensitive file: {path}'
    if tool == 'Bash':
        command = args.get('command', '')
        for pattern, reason in BLOCK:
            if re.search(pattern, command, re.I):
                return reason + '. Ask user; explain impact, cost and undo.'
    return None

if __name__ == '__main__':
    try:
        payload = json.load(sys.stdin)
        reason = check(payload)
        if reason:
            print(reason, file=sys.stderr)
            sys.exit(2)
    except (json.JSONDecodeError, TypeError, ValueError) as exc:
        print(f'Hook could not parse input; refusing action: {exc}', file=sys.stderr)
        sys.exit(2)
