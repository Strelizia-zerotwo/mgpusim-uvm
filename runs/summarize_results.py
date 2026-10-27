#!/usr/bin/env python3
"""Read saved experiments and build a non-destructive results index. Never runs a simulator."""
import argparse
from collections import Counter, defaultdict
import csv
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import re

PROTOCOL = 'kernel-internal-ideal-v3'


def read(path, default=None):
    try:
        return json.loads(path.read_text(encoding='utf-8'))
    except (OSError, ValueError):
        return default


def dump(path, value):
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2, allow_nan=False)+'\n', encoding='utf-8')


def sha(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(',', ':')).encode()).hexdigest()


def stats_header(path):
    """Decode only selected root values; don't build large per-request event arrays."""
    if not path.exists():
        return {}
    text = path.read_text(encoding='utf-8')
    decoder = json.JSONDecoder()
    fields = ['config','adapter','initial_input_sha256','application_ps','kernel_ps','host_transfer_ps',
              'kernel_count','copy_bytes','copy_faults','cold_copies','refills','repairs','invalidations',
              'mapping_faults','peak_replica_pages','peak_stale_pages','core_allocations']
    out = {}
    for key in fields:
        m = re.search(r'^  "'+re.escape(key)+r'":\s*', text, re.M)
        if m:
            try: out[key] = decoder.raw_decode(text, m.end())[0]
            except ValueError: pass
    m = re.search(r'^  "page_ownership":\s*', text, re.M)
    if m:
        try:
            homes = decoder.raw_decode(text, m.end())[0]
            out['homes_sha256'] = sha([(v['page'],v['home_gpu']) for v in homes])
        except (ValueError, KeyError, TypeError): pass
    m = re.search(r'^  "passive":\s*\{', text, re.M)
    if m:
        tail = text[m.end()-1:]
        end = re.search(r',\s*"events":\s*\[', tail)
        if end:
            try: out['passive'] = json.loads(tail[:end.start()]+'}')
            except ValueError: pass
    if not out and len(text)<2_000_000:
        value = json.loads(text)
        out = {k:value[k] for k in fields if k in value}
    return out


def csvfile(path, rows):
    columns = list(dict.fromkeys(k for r in rows for k in r))
    with path.open('w', encoding='utf-8-sig', newline='') as f:
        writer = csv.DictWriter(f, fieldnames=columns)
        writer.writeheader()
        for row in rows:
            writer.writerow({k:json.dumps(v, ensure_ascii=False) if isinstance(v,(dict,list)) else v for k,v in row.items()})


def collect(root):
    base = root/'results/benchmark'
    catalog = read(root/'benchmark/mybenchmark/catalog.json', {}).get('benchmarks', [])
    cat = {b['id']:b for b in catalog}
    manifests = {p.parent.name:read(p,{}) for p in sorted((base/'_suites').glob('*/manifest.json'))}
    fingerprints = {sid:read(base/'_suites'/sid/'source-sha256.json',{}) for sid in manifests}
    rows = {}
    # App-level summaries are fallbacks only; batch summaries would duplicate them.
    for app in sorted(p for p in base.iterdir() if p.is_dir() and not p.name.startswith('_') and p.name!='synthetic'):
        for path in sorted(app.glob('*/*/summary.json')):
            data = read(path, [])
            if isinstance(data,list):
                for row in data:
                    if isinstance(row,dict) and row.get('directory'):
                        rows[row['directory']] = {**row, '_source':str(path)}
        for path in sorted(app.rglob('result.json')):
            row = read(path)
            if isinstance(row,dict):
                directory = str(path.parent)
                rows[directory] = {**rows.get(directory,{}), **row, 'directory':directory, '_source':str(path)}
        for path in sorted(app.rglob('run.log.command.json')):
            directory = str(path.parent)
            if directory not in rows:
                rows[directory] = dict(benchmark=app.name,directory=directory,_source=str(path))
    records=[]
    for index,(directory,row) in enumerate(sorted(rows.items())):
        d=Path(directory)
        try: parts=d.relative_to(base).parts
        except ValueError: continue
        if len(parts)<3:continue
        bench,sid,mode=parts[:3];manifest=manifests.get(sid,{})
        arguments=manifest.get('arguments',{});b=read(base/bench/sid/mode/'benchmark.json',cat.get(bench,{}))
        command=read(d/'run.log.command.json',{})
        argv=command.get('argv',[]) if isinstance(command,dict) else command if isinstance(command,list) else []
        options={}
        for arg in argv[1:]:
            if arg.startswith('-'):
                k,sep,v=arg.lstrip('-').partition('=');options[k]=v if sep else True
        process=read(d/'run.log.process.json',{})
        stats=stats_header(d/'stats.json')
        config=stats.get('config') or read(Path(directory+'.json'),{})
        if not isinstance(config,dict):config={}
        policy=row.get('policy') or config.get('policy')
        match=re.match(r'(.+?)_(?:page-|link-)',d.name)
        if not policy and match:policy=match[1]
        profile=row.get('profile') or arguments.get('profile')
        flags=row.get('input_flags')
        if flags is None:
            # Saved per-batch benchmark file, not today's potentially changed catalog.
            cfg=read(base/'_suites'/sid/'input-config.json',{}).get('benchmarks',{})
            flags=cfg.get(bench) or b.get('profiles',{}).get(arguments.get('profile'),[])
        adapter=read(base/bench/sid/mode/'adapter.json',{}).get('adapter')
        protocol=row.get('protocol') or stats.get('passive',{}).get('protocol')
        if not protocol:
            candidate=stats.get('adapter') or adapter
            protocol=candidate.removeprefix('core-') if candidate else ('platform-'+mode if mode in ['stock','demand'] else 'legacy/unspecified')
        status=row.get('status')
        if not status:
            status=process.get('status') or 'INCOMPLETE_NO_FINAL_RECORD'
            if status=='OK':status='INCOMPLETE_NO_FINAL_RECORD'
        if (d/'result.json').exists() and status=='OK' and process and process.get('status')!='OK':status='RECORD_CONFLICT'
        rec={k:v for k,v in row.items() if k not in ['_source','workload_metadata']}
        rec.update(benchmark=bench,suite=sid,mode=mode,policy=policy,profile=profile,protocol=protocol,
                   status=status,directory=directory,source_record=row['_source'],input_flags=flags,
                   sample=b.get('sample'),implementation=b.get('implementation'),
                   command_saved=bool(argv),process_status=process.get('status'),final_result=(d/'result.json').exists(),stats_saved=(d/'stats.json').exists())
        for key in ['arch','gpu']:
            rec[key]=row.get(key) or options.get(key) or b.get(key)
        for key in ['page_bytes','sector_bytes','fault_ns','link_ns','bandwidth_gbs','replica_pages_per_gpu']:
            rec[key]=config.get(key,row.get(key,arguments.get(key)))
        rec['gpu_ids']=options.get('unified-gpus') or options.get('gpus') or arguments.get('gpus')
        rec['vm_mode']=options.get('vm-mode')
        rec['core_protocol_flag']=options.get('core-protocol')
        rec['engine']='parallel' if options.get('parallel') else 'serial (default)' if argv else None
        rec['timeout_host_s']=arguments.get('timeout')
        rec['wall_s']=row.get('wall_s',process.get('wall_s'))
        rec['peak_rss_kib']=row.get('peak_rss_kib',process.get('peak_rss_kib'))
        for key in ['application_us','kernel_us','host_transfer_us']:
            if rec.get(key) is None and stats.get(key.replace('_us','_ps')) is not None:
                rec[key]=stats[key.replace('_us','_ps')]/1e6
        for key in ['kernel_count','copy_bytes','copy_faults','cold_copies','refills','repairs','invalidations','mapping_faults']:
            if rec.get(key) is None and stats.get(key) is not None:rec[key]=stats[key]
        passive=stats.get('passive',{})
        for src,dst in [('repair_bytes','repair_bytes'),('repair_events','repair_events'),('peak_retained_frames','peak_retained_frames'),('stale_read_sectors','stale_read_sectors'),('charged_bytes','charged_bytes')]:
            if rec.get(dst) is None and src in passive:rec[dst]=passive[src]
        rec['initial_input_sha256']=stats.get('initial_input_sha256');rec['homes_sha256']=stats.get('homes_sha256')
        rec['config_sha256']=sha({k:v for k,v in config.items() if k!='policy'}) if config else None
        # Immutable build + same initialized data/config is the strongest cross-batch match.
        binary=Path(argv[0]).name if argv else b.get('sample')
        rec['binary_sha256']=read(base/'_suites'/sid/'bin'/(str(binary)+'.sha256.json'),{}).get('sha256')
        fp=fingerprints.get(sid,{})
        relevant={k:v for k,v in fp.items() if '/timing/replica/' in k and k.endswith('.go') and not k.endswith('_test.go')}
        rec['replica_source_sha256']=sha(relevant) if relevant else None
        rec['gpu_builder_sha256']=fp.get('mgpusim/amd/samples/runner/timingconfig/'+str(rec.get('gpu'))+'/builder.go')
        rec['eligible_current']=(protocol==PROTOCOL and status=='OK' and rec['process_status']=='OK' and rec['stats_saved'] and rec['final_result'] and isinstance(rec.get('application_us'),(int,float)) and rec['application_us']>0 and 'PASSED' in str(rec.get('verification','')))
        rec.pop('speedup_vs_inv',None);rec.pop('saving_percent_vs_inv',None);rec.pop('saving_us_vs_inv',None)
        records.append(rec)
        if index and index%75==0:print('Read saved records:',index,flush=True)
    records.sort(key=lambda r:(r['suite'],r['benchmark'],r['mode'],str(r['policy']),r['directory']))
    # Candidates differ only in policy. Cross-suite borrowing additionally requires
    # equal binary and source identities, actual initial input and ownership hashes.
    def key(r):return (r['benchmark'],r['protocol'],r.get('input_sha256') or sha(r.get('input_flags')),r.get('arch'),r.get('gpu'),r.get('gpu_ids'),r.get('config_sha256'),r.get('repeat',0))
    baselines=defaultdict(list)
    for r in records:
        if r['eligible_current'] and r['policy']=='inv':baselines[key(r)].append(r)
    pairs=[]
    for r in records:
        if not r['eligible_current'] or r['policy']=='inv':continue
        matches=[]
        for inv in baselines.get(key(r),[]):
            if not r['initial_input_sha256'] or r['initial_input_sha256']!=inv['initial_input_sha256'] or not r['homes_sha256'] or r['homes_sha256']!=inv['homes_sha256']:continue
            same_suite=r['suite']==inv['suite']
            cross_ok=(r['binary_sha256'] and r['binary_sha256']==inv['binary_sha256'] and r['replica_source_sha256']==inv['replica_source_sha256'])
            if same_suite or cross_ok:matches.append(inv)
        if not matches:continue
        inv=max(matches,key=lambda x:(x['suite']==r['suite'],x['suite']))
        pair=dict(benchmark=r['benchmark'],policy=r['policy'],input_flags=r['input_flags'],profile=r['profile'],arch=r['arch'],gpu=r['gpu'],gpu_ids=r['gpu_ids'],
                  page_bytes=r['page_bytes'],fault_ns=r['fault_ns'],link_ns=r['link_ns'],bandwidth_gbs=r['bandwidth_gbs'],
                  inv_us=inv['application_us'],policy_us=r['application_us'],saving_fraction=1-r['application_us']/inv['application_us'],
                  inv_bytes=inv.get('copy_bytes'),policy_bytes=r.get('copy_bytes'),inv_events=inv.get('copy_faults'),policy_events=r.get('copy_faults'),
                  inv_kernels=inv.get('kernel_count'),policy_kernels=r.get('kernel_count'),inv_rounds=inv.get('workload_rounds'),policy_rounds=r.get('workload_rounds'),
                  suite=r['suite'],inv_suite=inv['suite'],match='same batch + input/config/homes' if r['suite']==inv['suite'] else 'same binary + source + input/config/homes',
                  directory=r['directory'],inv_directory=inv['directory'])
        pair['bytes_saving_fraction']=1-pair['policy_bytes']/pair['inv_bytes'] if pair['inv_bytes'] and pair['policy_bytes'] is not None else None
        pairs.append(pair)
    overview=[]
    for b in catalog:
        rr=[r for r in records if r['benchmark']==b['id']];current=[r for r in rr if r['eligible_current']]
        latest=max(current,key=lambda x:(x['suite'],x['policy']=='inv')) if current else None
        overview.append(dict(benchmark=b['id'],sample=b['sample'],group=b['group'],adapter=bool(b.get('core_replica')),attempt_records=len(rr),started_records=sum(x['command_saved'] for x in rr),
                             statuses=dict(Counter(x['status'] for x in rr)),current_verified_records=len(current),current_policies=sorted(set(x['policy'] for x in current)),
                             latest_suite=latest['suite'] if latest else None,latest_input=latest['input_flags'] if latest else None,latest_directory=latest['directory'] if latest else None))
    suites=[]
    for sid,m in manifests.items():
        args=m.get('arguments',{});rr=[r for r in records if r['suite']==sid]
        label=str(args.get('bench',''))+' / '+str(args.get('input_config') or args.get('profile',''))
        label+=' / BW='+str(args.get('bandwidth_gbs','?'))+' GB/s / '+','.join(args.get('policies',[]))
        suites.append(dict(suite=sid,started_at=m.get('started_at'),label=label,benchmarks=args.get('bench'),mode=args.get('mode'),
                           profile=args.get('profile'),input_config=args.get('input_config'),bandwidth_gbs=args.get('bandwidth_gbs'),
                           policies=args.get('policies'),records=len(rr),statuses=dict(Counter(x['status'] for x in rr)),directory=str(base/'_suites'/sid)))
    synthetic=[]
    for f in sorted((base/'synthetic').glob('*/summary.json')):
        for row in read(f,[]):synthetic.append(dict(suite=f.parent.name,**row))
    return dict(as_of=dt.datetime.now(dt.timezone(dt.timedelta(hours=8))).isoformat(),root=str(root),records=records,pairs=pairs,overview=overview,suites=suites,synthetic=synthetic,
                counts=dict(suites=len(suites),records=len(records),started=sum(x['command_saved'] for x in records),statuses=dict(Counter(x['status'] for x in records)),
                            current_verified=sum(x['eligible_current'] for x in records),current_apps=len(set(x['benchmark'] for x in records if x['eligible_current'])),
                            protocols=dict(Counter(x['protocol'] for x in records)),matched_pairs=len(pairs),synthetic_cases=len(synthetic)))


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root',type=Path,default=Path(__file__).resolve().parents[1])
    parser.add_argument('--output',type=Path)
    args=parser.parse_args();root=args.root.resolve()
    stamp=dt.datetime.now(dt.timezone(dt.timedelta(hours=8))).strftime('%Y%m%d-%H%M%S')
    out=args.output or root/'results/benchmark/_reports'/('overview-'+stamp)
    out.mkdir(parents=True,exist_ok=True)
    data=collect(root);dump(out/'audit-data.json',data)
    for key in ['records','pairs','overview','suites','synthetic']:csvfile(out/(key+'.csv'),data[key])
    print(json.dumps(data['counts'],ensure_ascii=False));print('OUTPUT:',out)


if __name__=='__main__':main()
