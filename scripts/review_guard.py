#!/usr/bin/env python3
"""Simple offline repository reviewer (not a replacement for SAST).
Scans tracked project text with a small, transparent set of guard rules.
"""
from pathlib import Path
import argparse, re, subprocess, sys
ROOT=Path(__file__).resolve().parents[1]
parser=argparse.ArgumentParser()
parser.add_argument('--ci',action='store_true')
args=parser.parse_args()
try:
    names=subprocess.check_output(['git','ls-files'],cwd=ROOT,text=True,stderr=subprocess.DEVNULL).splitlines()
except (OSError,subprocess.CalledProcessError):
    names=[str(p.relative_to(ROOT)) for p in ROOT.rglob('*') if p.is_file()]
issues=[]
patterns=[
    ('possible AWS access key',re.compile(r'AKIA[0-9A-Z]{16}')),
    ('possible private key',re.compile(r'-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----')),
]
for name in names:
    path=ROOT/name
    if not path.is_file() or path.stat().st_size>1_000_000:continue
    if path.suffix in {'.png','.jpg','.ico','.zip','.sum'}:continue
    try: content=path.read_text(encoding='utf-8')
    except UnicodeDecodeError:continue
    if name.startswith('.env') and name!='.env.example':issues.append(f'{name}: environment file should not be tracked')
    if name.endswith('.tfstate'):issues.append(f'{name}: Terraform state must not be tracked')
    for label,rx in patterns:
        for match in rx.finditer(content):
            line=content.count('\n',0,match.start())+1
            issues.append(f'{name}:{line}: {label}')
if issues:
    print('AUTOMATED REVIEW FAILED:')
    for issue in issues: print(' -',issue)
    sys.exit(1)
print('AUTOMATED REVIEW PASSED: baseline repository safety checks')
