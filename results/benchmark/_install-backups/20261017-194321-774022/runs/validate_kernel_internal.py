#!/usr/bin/env python3
"""Build and execute same-kernel read/write/repair/write correctness checks."""
import argparse
import datetime as dt
import json
import os
from pathlib import Path
import sys
import uuid

import run_benchmarks as suite


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root',type=Path,default=Path(__file__).resolve().parents[1])
    parser.add_argument('--output',type=Path)
    parser.add_argument('--timeout',type=int,default=180)
    args=parser.parse_args()
    root=args.root.resolve()
    if os.name!='posix':parser.error('run in Linux/WSL')
    os.environ['PATH']='/usr/local/go/bin:/home/only/go/bin:'+os.environ.get('PATH','')
    stamp=dt.datetime.now(dt.timezone(dt.timedelta(hours=8))).strftime('%Y%m%d-%H%M%S')+'-'+uuid.uuid4().hex[:6]
    out=(args.output or root/'results/benchmark/_kernel-internal-validation'/stamp).resolve()
    out.mkdir(parents=True,exist_ok=False)
    suite.fingerprint(root,out)
    binary=out/'replica-internal'
    build=suite.execute(['go','build','-p','2','-o',str(binary),'./amd/samples/replica-internal'],
                        out,args.timeout,'build.log',cwd=root/'mgpusim')
    if build['status']!='OK':raise RuntimeError('build failed; see '+str(out/'build.log'))
    cfg=suite.read(root/'benchmark/replica-recovery/configs/inv.json')
    rows=[]
    for case in ['repeated-write','clean-local-read']:
      for policy in ['inv','retain','i1-adaptive']:
        directory=out/(case+'-'+policy);directory.mkdir()
        config=directory/'config.json';suite.dump(config,{**cfg,'policy':policy})
        cmd=[str(binary),'-timing','-verify','-disable-rtm','-replica-core','-core-protocol=passive',
             '-unified-gpus=1,2,3,4','-use-unified-memory','-vm-mode=demand','-report-all','-metric-file-name=metrics',
             '-copy-kernel='+str(root/'mgpusim/amd/driver/memcopy.hsaco'),'-rounds=4','-core-replica-config='+str(config)]
        if case=='clean-local-read':cmd+=['-clean-reads']
        process=suite.execute(cmd,directory,args.timeout)
        if process['status']!='OK':raise RuntimeError('simulation failed; see '+str(directory/'run.log'))
        s=suite.read(directory/'stats.json');p=s['passive']
        assert 'Passed! single kernel' in (directory/'run.log').read_text()
        assert s['kernel_count']==1
        assert p['protocol']=='kernel-internal-lazy-v2'
        assert p['write_transitions']==0 and p['stale_read_sectors']==0
        assert p['home_fetch_requests']>0
        assert s['mapping_faults']==0 and s['recovery_wait_ps']==0
        assert s['application_ps']==s['kernel_ps']+s['barrier_flush_ps']+s['host_transfer_ps']
        activity=suite.metric_summary(suite.sqlite_metrics(directory/'metrics.sqlite3',directory/'metrics.tsv'))
        assert activity['active_gpus']==[1,2,3,4],activity
        if case=='clean-local-read':
            assert (p['clean_replica_reads_during_write']>0)==(policy=='i1-adaptive')
        row=dict(case=case,policy=policy,status='OK',application_us=s['application_ps']/1e6,
                 copy_bytes=s['copy_bytes'],write_transitions=p['write_transitions'],stale_read_sectors=p['stale_read_sectors'],
                 concurrent_repairs_high_water=p['concurrent_repairs_high_water'],in_kernel_cache_control_us=p['cache_control_ps_in_kernel']/1e6,
                 intrakernel_refills=p['intrakernel_refills'],
                 intrakernel_repairs=p['intrakernel_repairs'],write_after_repair=p['write_after_repair_invalidations'],
                 clean_replica_reads_during_write=p['clean_replica_reads_during_write'],
                 adaptive_demand_repairs=p['adaptive_demand_repairs'],adaptive_full_stale_repairs=p['adaptive_full_stale_repairs'],
                 initial_input_sha256=s['initial_input_sha256'],active_gpus=activity['active_gpus'],directory=str(directory))
        rows.append(row);suite.dump(out/'summary.json',rows);suite.csvwrite(out/'summary.tsv',rows)
        print(json.dumps(row),flush=True)
    for case in ['repeated-write','clean-local-read']:
        assert len({r['initial_input_sha256'] for r in rows if r['case']==case})==1
    suite.dump(out/'validation.json',dict(status='PASS',cases=6,scope='one kernel, four modifying rounds, entire page CPU checked; not application speedup',rows=rows))
    print('PASS:',out,flush=True)
    return 0


if __name__=='__main__':
    sys.exit(main())
