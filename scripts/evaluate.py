#!/usr/bin/env python3
"""Run paired synthetic MCP workflow checks. Does not call an LLM or operate apps."""
import argparse
import json
from pathlib import Path
import subprocess
import sys


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, help='Optional JSON report path')
    parser.add_argument('--native-server', type=Path, help='Optional read-only --doctor check; no GUI actions')
    args = parser.parse_args()
    root = Path(__file__).resolve().parent.parent
    run = subprocess.run(['go', 'test', './internal/mcpserver', '-run', '^TestAgentWorkflowScenarios$', '-count=1', '-v'], cwd=root, text=True, capture_output=True, timeout=120)
    if run.returncode:
        sys.stderr.write(run.stdout + run.stderr)
        return run.returncode
    rows = [json.loads(line.split('EVAL_RESULT ', 1)[1]) for line in run.stdout.splitlines() if 'EVAL_RESULT ' in line]
    expected = {'browser_like', 'form_like', 'terminal_like', 'cad_like'}
    if len(rows) != 8 or {row['scenario'] for row in rows} != expected:
        raise RuntimeError('Incomplete evaluation output; refusing to report success.')
    pairs = []
    for scenario in sorted(expected):
        variants = {row['variant']: row for row in rows if row['scenario'] == scenario}
        before, after = variants['separate_tools'], variants['unified_tools']
        if not before['passed'] or not after['passed']:
            raise RuntimeError(f'Failed scenario: {scenario}')
        if after['tool_calls'] > before['tool_calls'] or after['images'] > before['images']:
            raise RuntimeError(f'Unified workflow increased tool/image counts: {scenario}')
        pairs.append({'scenario': scenario, 'tool_calls_before': before['tool_calls'], 'tool_calls_after': after['tool_calls'], 'images_before': before['images'], 'images_after': after['images']})
    report = {
        'environment': 'synthetic scripted fixtures; no real apps, game engine or LLM',
        'scope': 'driver contract and explicit UI predicate verification, not model quality or subscription savings',
        'scenarios': rows,
        'paired_summary': pairs,
        'native': {'status': 'not_requested', 'real_app_scenarios': 'not_run'},
    }
    if args.native_server:
        native = subprocess.run([str(args.native_server.resolve()), '--doctor'], text=True, capture_output=True, timeout=20)
        try:
            data = json.loads(native.stdout)
            blocked = data.get('session_available') is False or data.get('console_unlocked') is False
            report['native'] = {'status': 'blocked' if blocked else 'preflight_only', 'backend': data.get('backend'), 'blocking_reason': data.get('blocking_reason'), 'session_available': data.get('session_available'), 'real_app_scenarios': 'not_run'}
        except ValueError:
            report['native'] = {'status': 'doctor_failed', 'returncode': native.returncode, 'real_app_scenarios': 'not_run'}
    encoded = json.dumps(report, indent=2, ensure_ascii=False) + '\n'
    if args.output:
        args.output.write_text(encoded)
    print(encoded, end='')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
