#!/usr/bin/env python3
"""Mechanism screening from observed-plan.json + actual ownership; never a timing predictor."""
import argparse
import json
from pathlib import Path
import statistics


def analyze(plan, ownership, peak_live_managed_pages=None):
    homes={p['page']:p['home_gpu'] for p in ownership}
    sectors=plan['page_bytes']//plan['sector_bytes']
    states={}
    invalidations=refills=cold=0
    dirty=[]
    merged=[]
    distances=[]
    events=[]
    active=set()
    peak_stale=peak_copies=0
    stale_count=copy_count=0
    for index,epoch in enumerate(plan['epochs']):
        pages={}
        for a in epoch['accesses'] or []:
            if a['page'] not in homes:
                raise ValueError('plan page lacks actual physical home')
            pages.setdefault(a['page'],[]).append(a)
            if (a['reads'] or a['writes']) and epoch.get('kind','kernel')=='kernel':
                active.add(a['gpu'])
        for page,accesses in pages.items():
            state=states.setdefault(page,dict(last_write={},copies={},write_epochs=[]))
            written=set().union(*(set(a['writes'] or []) for a in accesses))
            if written:
                state['write_epochs'].append(index)
                for sector in written:
                    state['last_write'][sector]=index
                for c in state['copies'].values():
                    if c['valid']:
                        invalidations+=1
                        stale_count+=1
                        c.update(valid=False,invalidated=index)
                # Global write epoch: conservative home service, no cold creation.
                continue
            for a in accesses:
                if not a['reads'] or a['gpu']==homes[page]:
                    continue
                c=state['copies'].get(a['gpu'])
                if c is None:
                    cold+=1
                    copy_count+=1
                elif not c['valid']:
                    refills+=1
                    stale_count-=1
                    count=sum(when>c['version'] for when in state['last_write'].values())
                    needed=sum(when>c['version'] and sector in set(a['reads']) for sector,when in state['last_write'].items())
                    n=sum(e>c['version'] for e in state['write_epochs'])
                    distance=index-c['invalidated']
                    dirty.append(count/sectors)
                    merged.append(n)
                    distances.append(distance)
                    events.append(dict(page=page,gpu=a['gpu'],epoch=index,dirty_sectors=count,
                                       required_dirty_sectors=needed,dirty_fraction=count/sectors,
                                       merged_write_epochs=n,reread_distance=distance))
                else:
                    # Reading an already-valid copy must not advance its last-repair version.
                    continue
                state['copies'][a['gpu']]=dict(valid=True,version=index)
        for page in epoch.get('retired_pages',[]):
            state=states.pop(page,None)
            if state:
                copy_count-=len(state['copies'])
                stale_count-=sum(not c['valid'] for c in state['copies'].values())
        peak_stale=max(peak_stale,stale_count)
        peak_copies=max(peak_copies,copy_count)
    hist={'0':0,'(0,0.1]':0,'(0.1,0.25]':0,'(0.25,0.5]':0,'(0.5,0.9]':0,'(0.9,1]':0}
    for f in dirty:
        key='0' if f==0 else '(0,0.1]' if f<=.1 else '(0.1,0.25]' if f<=.25 else '(0.25,0.5]' if f<=.5 else '(0.5,0.9]' if f<=.9 else '(0.9,1]'
        hist[key]+=1
    median=lambda xs: statistics.median(xs) if xs else None
    footprint=peak_live_managed_pages if peak_live_managed_pages is not None else len(homes)
    return dict(scope='unlimited INV shadow, kernel-barrier schedule, written 32B request masks, actual fixed homes; not online hardware or timing',
                dirty_definition='written-request-mask',changed_bytes_available=False,
                epochs=len(plan['epochs']),kernel_count=sum(e.get('kind')=='kernel' for e in plan['epochs']) if all('kind' in e for e in plan['epochs']) else None,active_gpus=sorted(active),
                cold_copies=cold,invalidations=invalidations,refills=refills,
                refill_per_invalidation=refills/invalidations if invalidations else None,
                dirty_fraction_p50=median(dirty),dirty_fraction_histogram=hist,
                merged_write_epochs_p50=median(merged),reread_distance_p50=median(distances),
                peak_stale_bytes=peak_stale*plan['page_bytes'],peak_replica_bytes=peak_copies*plan['page_bytes'],
                stale_copies_at_end=stale_count,right_censoring='final invalidations may have no later read within the captured run',
                managed_home_bytes=footprint*plan['page_bytes'],cumulative_managed_home_bytes=len(homes)*plan['page_bytes'],
                footprint_basis='peak live managed allocation pages' if peak_live_managed_pages is not None else 'static managed pages',
                peak_stale_over_home_footprint=peak_stale/footprint if footprint else None,
                refill_events=events)


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('record',type=Path)
    p.add_argument('--output',type=Path)
    a=p.parse_args()
    plan=json.loads((a.record/'observed-plan.json').read_text())
    stats=json.loads((a.record/'stats.json').read_text())
    result=analyze(plan,stats['page_ownership'],stats.get('peak_live_managed_pages') or None)
    text=json.dumps(result,indent=2,allow_nan=False)+'\n'
    if a.output:
        a.output.write_text(text)
    else:
        print(text)


if __name__=='__main__':
    main()
