#!/usr/bin/env python3
"""Compare fixed isolated UI samples; no missing measurements treated as zero."""
import json
import statistics
from pathlib import Path

root = Path(__file__).resolve().parents[2] / '.test/ui-unification'
before = json.loads((root / 'before-final/performance.json').read_text())
after = json.loads((root / 'after/performance.json').read_text())
assert before['browser'] == after['browser'], 'Browser changed between samples'
assert len(before['samples']) == len(after['samples']) == 30
rows, failures = [], []
for route in dict.fromkeys(x['path'] for x in before['samples']):
    old = [x for x in before['samples'] if x['path'] == route]
    new = [x for x in after['samples'] if x['path'] == route]
    assert len(old) == len(new) == 5
    row = {'path': route, 'samples': 5, 'metrics': {}}
    for key in ['lcp', 'interaction', 'compositionMs', 'billingReadMs', 'jsGzip']:
        a, b = [x[key] for x in old], [x[key] for x in new]
        if any(x is None for x in a + b):
            assert key not in ('lcp', 'jsGzip'), 'Core metric missing'
            row['metrics'][key] = {'before': None, 'after': statistics.median(b) if all(x is not None for x in b) else None, 'comparable': False}
            continue
        assert all(x > 0 for x in a + b)
        av, bv = statistics.median(a), statistics.median(b)
        threshold = 10240 if key == 'jsGzip' else 100
        passed = not (bv > av * 1.1 and bv - av > threshold)
        row['metrics'][key] = {'before': av, 'after': bv, 'delta': bv - av, 'percent': round((bv / av - 1) * 100, 1), 'passed': passed, 'comparable': True}
        if not passed: failures.append(route + ':' + key)
    row['mediaRequestsBefore'] = sum(len(x['mediaRequests']) for x in old)
    row['mediaRequestsAfter'] = sum(len(x['mediaRequests']) for x in new)
    if row['mediaRequestsAfter'] > row['mediaRequestsBefore']: failures.append(route + ':media')
    rows.append(row)
result = {'browser': after['browser'], 'passed': not failures, 'failures': failures, 'rows': rows, 'scope': 'Loopback production builds, intercepted synthetic APIs, 1440x960 headless Chromium; operation latency is not field INP. New order-query operation has no equivalent in baseline.'}
(root / 'performance-comparison.json').write_text(json.dumps(result, ensure_ascii=False, indent=2))
print(json.dumps(result, ensure_ascii=False, indent=2))
assert not failures, failures
