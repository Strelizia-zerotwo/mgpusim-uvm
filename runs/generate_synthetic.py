#!/usr/bin/env python3
"""Generate controlled global-epoch access plans; report opportunity/bytes, never simulator time."""
import argparse
import csv
import datetime as dt
import json
from pathlib import Path
import uuid

from screen_opportunities import analyze


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--root',type=Path,default=Path(__file__).resolve().parents[1])
    p.add_argument('--page-bytes',type=int,default=4096)
    p.add_argument('--fractions',default='0.0078125,0.0625,0.25,1')
    p.add_argument('--gaps',default='1,4,16',help='first-write to reread epoch distance')
    p.add_argument('--write-epochs',default='1,4')
    p.add_argument('--readers',default='1,3')
    a=p.parse_args()
    if a.page_bytes<4096 or a.page_bytes&(a.page_bytes-1):
        p.error('page size must be power of two >=4096')
    fractions=[float(x) for x in a.fractions.split(',')]
    gaps=[int(x) for x in a.gaps.split(',')]
    writes=[int(x) for x in a.write_epochs.split(',')]
    readers=[int(x) for x in a.readers.split(',')]
    if any(not 0<x<=1 for x in fractions) or min(gaps+writes)<1 or any(x not in [1,2,3] for x in readers):
        p.error('fractions in (0,1], positive gaps/writes, readers in 1..3')
    stamp=dt.datetime.now(dt.timezone(dt.timedelta(hours=8))).strftime('%Y%m%d-%H%M%S')+'-'+uuid.uuid4().hex[:6]
    out=a.root.resolve()/'results/benchmark/synthetic'/stamp
    out.mkdir(parents=True,exist_ok=False)
    n=a.page_bytes//32
    home=[dict(page=a.page_bytes,home_gpu=1)]
    results=[]
    def acc(g,r=(),w=()):
        return dict(page=a.page_bytes,gpu=g,reads=list(r),writes=list(w),read_transactions=bool(r),write_transactions=bool(w))
    for f in fractions:
        count=max(1,min(n,round(f*n)))
        for layout in ['contiguous','scattered']:
            mask=list(range(count)) if layout=='contiguous' else sorted({i*n//count for i in range(count)})
            for gap in gaps:
                for nw in writes:
                    if gap<nw:
                        continue
                    for nr in readers:
                        epochs=[dict(name='cold-read',accesses=[acc(g,range(n)) for g in range(2,2+nr)])]
                        epochs += [dict(name=f'write-{k}',accesses=[acc(1,w=mask)]) for k in range(nw)]
                        epochs += [dict(name='reader-absent',accesses=[]) for _ in range(gap-nw)]
                        epochs += [dict(name='reread',accesses=[acc(g,range(n)) for g in range(2,2+nr)])]
                        plan=dict(format=1,page_bytes=a.page_bytes,sector_bytes=32,epochs=epochs)
                        s=analyze(plan,home)
                        case=f'f{count}of{n}_{layout}_gap{gap}_writes{nw}_readers{nr}'
                        directory=out/case
                        directory.mkdir()
                        (directory/'generated-plan.json').write_text(json.dumps(plan,indent=2)+'\n')
                        (directory/'ownership.json').write_text(json.dumps(home,indent=2)+'\n')
                        (directory/'opportunities.json').write_text(json.dumps(s,indent=2)+'\n')
                        inv=s['refills']*a.page_bytes
                        retain=sum(e['dirty_sectors']*32 for e in s['refill_events'])
                        extents=sum(i==0 or mask[i-1]+1!=x for i,x in enumerate(mask))
                        results.append(dict(case=case,dirty_fraction=count/n,layout=layout,dirty_extents=extents,
                                            reread_distance=gap,write_epochs=nw,readers=nr,
                                            inv_refill_payload_bytes=inv,retain_refill_payload_bytes=retain,
                                            payload_saving_fraction=1-retain/inv if inv else None,
                                            transport_header_bytes_per_repair=extents*16,
                                            application_us=None,scope='synthetic opportunity and payload arithmetic only'))
    (out/'summary.json').write_text(json.dumps(results,indent=2)+'\n')
    with (out/'summary.tsv').open('w',newline='') as fp:
        w=csv.DictWriter(fp,fieldnames=list(results[0]) if results else ['case'],delimiter='\t')
        w.writeheader()
        w.writerows(results)
    print(out)
    print(f'{len(results)} synthetic cases. No simulator runtime or strategy speedup was measured.')


if __name__=='__main__':
    main()
