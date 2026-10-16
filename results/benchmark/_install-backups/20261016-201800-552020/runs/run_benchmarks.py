#!/usr/bin/env python3
"""Small, source-audited MGPUSim experiments. Python 3.10+, standard library only."""
import argparse
import csv
import datetime as dt
import hashlib
import importlib.util
import json
import math
import os
from pathlib import Path
import re
import shlex
import shutil
import signal
import sqlite3
import subprocess
import sys
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]
POLICIES = 'norep,inv,retain,i1-no-s3,i1,i1-full-stale,i1-adaptive'.split(',')
DEFAULT_POLICIES = ['inv','retain','i1-adaptive']
EXTRA = ['i1-write-prefetch', 'i2', 'infinity']
COLUMNS = ['benchmark', 'profile', 'mode', 'policy', 'repeat', 'status', 'verification',
           'wall_s', 'peak_rss_kib', 'application_us', 'kernel_us', 'barrier_us', 'recovery_us', 'host_transfer_us',
           'speedup_vs_inv', 'saving_percent_vs_inv', 'copy_bytes', 'copy_faults', 'refills',
           'invalidations', 'peak_replica_bytes', 'peak_stale_bytes', 'mapping_faults',
           'active_gpus', 'instructions_by_gpu', 'gpu_activity_ok', 'epochs', 'kernel_count', 'fallback_page_epochs',
           'non_owner_write_transactions', 'write_epoch_existing_replica_reads',
           'write_epoch_new_copies', 'bandwidth_gbs', 'link_ns', 'fault_ns',
           'shadow_refill_per_invalidation', 'shadow_dirty_fraction_p50', 'shadow_merged_write_epochs_p50',
           'shadow_reread_distance_p50', 'reason', 'directory']


def dump(p, x):
    p.write_text(json.dumps(x, indent=2, ensure_ascii=False, allow_nan=False) + '\n', encoding='utf-8')


def read(p):
    return json.loads(p.read_text(encoding='utf-8'))


def digest(p):
    h = hashlib.sha256()
    with p.open('rb') as f:
        for b in iter(lambda: f.read(1024 * 1024), b''):
            h.update(b)
    return h.hexdigest()


def csvwrite(p, rows, columns=None, delimiter='\t'):
    columns = columns or list(dict.fromkeys(k for r in rows for k in r))
    with p.open('w', newline='', encoding='utf-8') as f:
        w = csv.DictWriter(f, fieldnames=columns, extrasaction='ignore', delimiter=delimiter)
        w.writeheader()
        for r in rows:
            w.writerow({k: json.dumps(v) if isinstance(v, (list, dict)) else v for k, v in r.items()})


def execute(cmd, directory, timeout, logname='run.log', cwd=None):
    """Never use a shell; terminate the full simulator process group on timeout."""
    directory.mkdir(parents=True, exist_ok=True)
    dump(directory / (logname + '.command.json'), dict(argv=cmd, cwd=str(cwd or directory)))
    (directory / (logname + '.command.sh')).write_text(shlex.join(cmd) + '\n', encoding='utf-8')
    actual = cmd
    resource_path = directory / (logname + '.resources.txt')
    if Path('/usr/bin/time').exists():
        actual = ['/usr/bin/time', '-v', '-o', str(resource_path), *cmd]
    start = time.monotonic()
    status, code = 'OK', None
    with (directory / logname).open('w', encoding='utf-8') as log:
        proc = subprocess.Popen(actual, cwd=cwd or directory, stdout=log, stderr=subprocess.STDOUT,
                                env={**os.environ,'LC_ALL':'C','GODEBUG':os.environ.get('GODEBUG','')+',randautoseed=0,randseednop=0'},start_new_session=(os.name == 'posix'))
        try:
            code = proc.wait(timeout=timeout)
            if code:
                status = 'PROCESS_FAILED'
        except (subprocess.TimeoutExpired, KeyboardInterrupt) as exc:
            if os.name == 'posix':
                os.killpg(proc.pid, signal.SIGTERM)
            else:
                proc.terminate()
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                if os.name == 'posix':
                    os.killpg(proc.pid, signal.SIGKILL)
                else:
                    proc.kill()
                proc.wait()
            if isinstance(exc, KeyboardInterrupt):
                raise
            status = 'TIMEOUT'
    result = dict(status=status, returncode=code, wall_s=round(time.monotonic()-start, 6))
    if resource_path.exists():
        match = re.search(r'Maximum resident set size \(kbytes\):\s*(\d+)', resource_path.read_text())
        result['peak_rss_kib'] = int(match[1]) if match else None
    dump(directory / (logname + '.process.json'), result)
    return result


def sqlite_metrics(path, output=None):
    if not path.exists():
        raise RuntimeError('metrics.sqlite3 missing')
    with sqlite3.connect(path.resolve().as_uri()+'?mode=ro', uri=True) as c:
        rows = [dict(zip(['Location','What','Value','Unit'], r)) for r in
                c.execute('SELECT Location, What, Value, Unit FROM mgpusim_metrics')]
    if not rows:
        raise RuntimeError('empty simulator metrics')
    if output:
        csvwrite(output, rows, ['Location','What','Value','Unit'])
    return {(r['Location'],r['What']):r['Value'] for r in rows}


def metric_summary(m):
    # A CP can receive a launch whose WG filter selects no work. Only retired CU
    # instructions establish execution on a physical GPU.
    inst={}
    for (loc,key),v in m.items():
        if key=='cu_inst_count' and (g:=re.match(r'GPU\[(\d+)\]\.',loc)):
            gpu=int(g[1])
            inst[gpu]=inst.get(gpu,0)+v
    if not inst:
        raise RuntimeError('CU instruction metrics missing; cannot establish physical GPU activity')
    active=sorted(g for g,v in inst.items() if v>0)
    result = dict(active_gpus=active,instructions_by_gpu=inst,
                  kernel_us=m.get(('Driver','kernel_time'), 0)*1e6,
                  mapping_faults=sum(v for (loc,k),v in m.items() if k=='vm_local_faults'),
                  kernel_count=None)
    if ('Driver','kernel_time') not in m:
        raise RuntimeError('Driver kernel_time metric missing')
    return result


def baseline_flags(b, args):
    if b.get('blocked'):
        raise RuntimeError(b['blocked'])
    if args.mode == 'demand' and b.get('demand_block'):
        raise RuntimeError(b['demand_block'])
    flags = ['-timing', '-disable-rtm', '-use-unified-memory', '-report-all', '-verify',
             f'-arch={b["arch"]}', f'-gpu={b["gpu"]}', '-metric-file-name=metrics']
    if args.mode == 'demand':
        flags += ['-vm-mode=demand', '-gpus='+args.gpus]
    else:
        flags += ['-vm-mode=shared', '-unified-gpus='+args.gpus]
    return flags + b['profiles'][args.profile]


def audit(root, catalog):
    sample = root/'mgpusim/amd/samples'
    return dict(source=str(root/'mgpusim'),
                samples={b['id']: (sample/b['sample']).is_dir() for b in catalog},
                replica_sample=(sample/'replica/main.go').exists(),
                replica_runner=(root/'benchmark/replica-recovery/run.py').exists(),
                old_memtrace=(root/'mgpusim/amd/timing/memtrace').exists(),
                adapters={b['id']:b.get('replica',{}).get('workload') for b in catalog},
                scope='stock/shared and demand are platform checks; only explicit adapters support fair policy timing')


def fingerprint(root, out):
    names = []
    for folder in ['mgpusim', 'akita-patched']:
        for p in (root/folder).rglob('*'):
            if p.is_file() and p.suffix in ('.go','.mod','.sum','.hsaco','.cl'):
                names.append(p)
    names += [Path(__file__), root/'benchmark/mybenchmark/catalog.json', root/'runs/screen_opportunities.py',root/'runs/core_policy_runner.py']
    names += list((root/'benchmark/replica-recovery/configs').glob('*.json'))
    old_runner = root/'benchmark/replica-recovery/run.py'
    if old_runner.exists(): names.append(old_runner)
    dump(out/'source-sha256.json', {str(p.relative_to(root)):digest(p) for p in sorted(set(names)) if p.exists()})


def load_legacy(root):
    path = root/'benchmark/replica-recovery/run.py'
    spec = importlib.util.spec_from_file_location('replica_legacy_runner',path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def load_screen(root):
    spec = importlib.util.spec_from_file_location('opportunity_screen',root/'runs/screen_opportunities.py')
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def build(args, sample, suite):
    binary = suite/'bin'/sample
    if binary.exists():
        return binary
    binary.parent.mkdir(exist_ok=True)
    go = shutil.which('go') or '/usr/local/go/bin/go'
    cmd = [go,'build','-o',str(binary),'./amd/samples/'+sample]
    r = execute(cmd, suite/'build'/sample, args.build_timeout, cwd=args.root/'mgpusim')
    if r['status'] != 'OK':
        raise RuntimeError(f'build {r["status"]}; see {suite / "build" / sample}')
    dump(suite/'bin'/(sample+'.sha256.json'),dict(sha256=digest(binary)))
    return binary


def base_row(b,args,directory,policy=None,repeat=0):
    return dict(benchmark=b['id'],profile=args.profile,mode=args.mode,policy=policy,
                repeat=repeat,directory=str(directory),verification='NOT_RUN')


def stock(b,args,suite,run):
    flags = baseline_flags(b,args)
    binary = build(args,b['sample'],suite)
    rows=[]
    for repeat in range(args.repeats):
        directory=run/f'repeat-{repeat}'
        row=base_row(b,args,directory,repeat=repeat)
        result=execute([str(binary),*flags], directory,args.timeout)
        row.update(result)
        if result['status']=='OK':
            try:
                log=(directory/'run.log').read_text(errors='replace')
                if b['verify_kind']=='passed' and b.get('verify_marker','Passed!') not in log:
                    raise RuntimeError('expected numerical verification marker is missing')
                # conv2d/im2col Verify() is empty, but runner pre-enables CPU operator verification.
                row['verification']='CPU_OPERATOR_CHECKS' if b['verify_kind']=='operator' else 'CPU_PASSED'
                row.update(metric_summary(sqlite_metrics(directory/'metrics.sqlite3',directory/'metrics.tsv')))
                row['gpu_activity_ok']=row['active_gpus']==args.gpu_ids
                if not row['gpu_activity_ok']:
                    row.update(status='INSUFFICIENT_GPU_ACTIVITY',reason='not every requested physical GPU executed a kernel')
                if args.mode=='demand' and row['mapping_faults']==0:
                    row.update(status='NO_MAPPING_FAULTS',reason='requested demand experiment did not exercise mapping faults')
                # Never infer an application time or refill opportunity from ordinary kernel_time.
                row['reason']=row.get('reason','platform run only; replica/refill metrics and runtime kernel count unavailable')
            except Exception as exc:
                row.update(status='VALIDATION_FAILED',reason=str(exc),verification='FAILED_OR_INCOMPLETE')
        else:
            row['reason']='see run.log; failed and timed-out timings are excluded'
        dump(directory/'result.json',row)
        rows.append(row)
    return rows


def check_stats(directory,legacy):
    text=(directory/'run.log').read_text(errors='replace')
    if 'Passed!' not in text:
        raise RuntimeError('replica CPU numerical verification did not pass')
    s=read(directory/'stats.json')
    if s['model_revision']!=legacy.REVISION or s['fabric_model']!='destination-ready-v2' or not s['epochs']:
        raise RuntimeError('unsupported replica model revision or no epochs')
    if s['application_ps']!=sum(s[k] for k in ('kernel_ps','barrier_flush_ps','recovery_wait_ps'))+s.get('host_transfer_ps',0):
        raise RuntimeError('application time accounting mismatch')
    if s['application_ps']<=0:
        raise RuntimeError('nonpositive application time')
    return s


def core_policies(b,args,suite,run):
    from core_policy_runner import run_core
    return run_core(b,args,suite,run,globals())


def policies(b,args,suite,run):
    if b.get('core_replica'):
        return core_policies(b,args,suite,run)
    if not b.get('replica'):
        raise RuntimeError('no explicit concurrent owner-placement replica adapter; stock sample cannot replay the seven policies')
    legacy=load_legacy(args.root)
    binary=build(args,'replica',suite)
    shape=b['replica'][args.profile]
    workload=b['replica']['workload']
    if args.page_bytes!=4096:
        raise RuntimeError('this catalog validates owner geometry only for 4096-byte pages; add and validate a separate aligned profile first')
    cfg=read(args.root/'benchmark/replica-recovery/configs/inv.json')
    cfg.update(page_bytes=args.page_bytes,transfer_gran_bytes=32,bandwidth_gbs=args.bandwidth_gbs,
               link_ns=args.links[0],fault_ns=args.faults[0],replica_pages_per_gpu=args.replica_pages_per_gpu)
    common=[str(binary),'-timing','-disable-rtm','-use-unified-memory','-vm-mode=demand','-verify',
            '-gpus='+args.gpus,'-arch=gcn3','-gpu=r9nano','-report-all','-metric-file-name=metrics',
            '-replica-plan-out=observed-plan.json','-replica-stats=stats.json',
            f'-workload={workload}',f'-page-bytes={args.page_bytes}',
            '-home-placement=owner','-align-owner-pages=true',
            *[f'-{key}={value}' for key,value in shape.items()]]
    run.mkdir(parents=True,exist_ok=True)
    record=run/'record'
    dump(run/'record-config.json',{**cfg,'policy':'norep'})
    result=execute(common+['-replica-config='+str(run/'record-config.json')],record,args.timeout)
    if result['status']!='OK':
        row=base_row(b,args,record)
        row.update(result,reason='record failed; policy replay not started')
        dump(record/'result.json',row)
        return [row]
    observed=check_stats(record,legacy)
    plan=read(record/'observed-plan.json')
    ownership=observed['page_ownership']
    dump(record/'page-ownership.json',ownership)
    sqlite_metrics(record/'metrics.sqlite3',record/'metrics.tsv')
    screen=load_screen(args.root).analyze(plan,ownership)
    dump(run/'opportunities.json',screen)
    csvwrite(run/'opportunities.tsv',[screen])
    fairness=argparse.Namespace(workload=workload,home_placement='owner',align_owner_pages=True)
    if args.mode=='record':
        row=base_row(b,args,record)
        row.update(result,verification='CPU_PASSED',epochs=len(plan['epochs']),reason='record only; not a fair NoRep replay timing')
        dump(record/'result.json',row)
        return [row]
    rows=[]
    complete=[]
    plan_path=record/'observed-plan.json'
    for link in args.links:
        for fault in args.faults:
            for repeat in range(args.repeats):
                for policy in args.policies:
                    name=f'{policy}_link-{link}ns_fault-{fault}ns_repeat-{repeat}'
                    directory=run/name
                    case={**cfg,'policy':policy,'link_ns':link,'fault_ns':fault}
                    if policy=='i2':
                        case.update(metadata_ns=2,shootdown_ns=100)
                    config_path=run/(name+'.json')
                    dump(config_path,case)
                    row=base_row(b,args,directory,policy,repeat)
                    row.update(link_ns=link,fault_ns=fault,bandwidth_gbs=args.bandwidth_gbs)
                    row.update(execute(common+['-replica-config='+str(config_path),'-replica-plan='+str(plan_path)],directory,args.timeout))
                    if row['status']=='OK':
                        try:
                            s=check_stats(directory,legacy)
                            legacy.verify_replay(s,plan,args.gpu_ids,fairness)
                            if s['page_ownership']!=ownership:
                                raise RuntimeError('physical home placement differs from the common record')
                            replay_plan=read(directory/'observed-plan.json')
                            # Timing can differ; per-epoch access masks and transaction counts must match exactly.
                            if [e['accesses'] for e in replay_plan['epochs']]!=[e['accesses'] for e in plan['epochs']]:
                                raise RuntimeError('executed access plan differs across strategies')
                            m=sqlite_metrics(directory/'metrics.sqlite3',directory/'metrics.tsv')
                            activity=metric_summary(m)
                            active=activity['active_gpus']
                            if active!=args.gpu_ids:
                                raise RuntimeError('not every requested physical GPU executed a kernel')
                            row.update(verification='CPU_PASSED',active_gpus=active,instructions_by_gpu=activity['instructions_by_gpu'],gpu_activity_ok=True,
                                       application_us=s['application_ps']/1e6,kernel_us=s['kernel_ps']/1e6,
                                       barrier_us=s['barrier_flush_ps']/1e6,recovery_us=s['recovery_wait_ps']/1e6,
                                       epochs=s['epochs'],copy_bytes=s['copy_bytes'],copy_faults=s['copy_faults'],
                                       refills=s['refills'],invalidations=s['invalidations'],
                                       peak_replica_bytes=s['peak_replica_pages']*args.page_bytes,
                                       peak_stale_bytes=s['peak_stale_pages']*args.page_bytes)
                            for key in ['mapping_faults','fallback_page_epochs','non_owner_write_transactions',
                                        'write_epoch_existing_replica_reads','write_epoch_new_copies']:
                                row[key]=s[key]
                            for key in ['refill_per_invalidation','dirty_fraction_p50','merged_write_epochs_p50','reread_distance_p50']:
                                row['shadow_'+key]=screen[key]
                            s.update(case=name,repeat=repeat)
                            complete.append(s)
                        except Exception as exc:
                            row.update(status='VALIDATION_FAILED',reason=str(exc),verification='FAILED_OR_INCOMPLETE')
                    else:
                        row['reason']='see run.log; failed and timed-out timings are excluded'
                    dump(directory/'result.json',row)
                    rows.append(row)
                    print(f'{b["id"]} {name}: {row["status"]} application_us={row.get("application_us")}',flush=True)
    # The original selector groups real completed applications and includes INV.
    legacy.write_envelope(run,complete)
    dump(run/'validated-policy-stats.json',complete)
    return rows


def summarize(rows,out):
    bases={}
    for r in rows:
        if r.get('status')=='OK' and r.get('policy')=='inv':
            bases[(r['benchmark'],r['profile'],r['mode'],r['repeat'],r.get('link_ns'),r.get('fault_ns'),r.get('bandwidth_gbs'))]=r.get('application_us')
    for r in rows:
        key=(r['benchmark'],r['profile'],r['mode'],r['repeat'],r.get('link_ns'),r.get('fault_ns'),r.get('bandwidth_gbs'))
        base=bases.get(key)
        t=r.get('application_us')
        if r.get('status')=='OK' and base and t:
            r.update(speedup_vs_inv=base/t,saving_percent_vs_inv=100*(1-t/base))
    dump(out/'summary.json',rows)
    csvwrite(out/'summary.tsv',rows,COLUMNS)
    csvwrite(out/'summary.csv',rows,COLUMNS,',')
    failures=[r for r in rows if r['status']!='OK']
    dump(out/'failures.json',failures)
    dump(out/'summary-provenance.json',dict(
        summarized_at=dt.datetime.now(dt.timezone(dt.timedelta(hours=8))).isoformat(),
        runner_sha256=digest(Path(__file__)),
        activity_basis='sum of retired cu_inst_count by physical GPU; empty launches do not establish activity'))
    lines=['# 本轮实验结果','',
           'wall_s 是宿主运行耗时；application_us 是复制模型应用时间；kernel_us 是模拟 kernel 时间。',
           '平台检查的 application_us / speedup / refill 留空。FAIL / BLOCKED / TIMEOUT 不进入收益统计。','',
           '|应用|模式|策略|状态|应用 µs|kernel µs|宿主秒|INV 加速比|',
           '|---|---|---|---|---:|---:|---:|---:|']
    for r in rows:
        f=lambda k: f'{r[k]:.4f}' if isinstance(r.get(k),(int,float)) else ''
        lines.append('|'+ '|'.join([r['benchmark'],r['mode'],r.get('policy') or '',r['status'],f('application_us'),f('kernel_us'),f('wall_s'),f('speedup_vs_inv')])+'|')
    lines+=['','完整命令、日志、原始 SQLite、展开的 metrics.tsv、策略 stats.json、plan 和资源统计见各应用目录。',
            'opportunities 是同一个 INV shadow 的机制机会；不是每种策略的实测性能，也不表示 kernel 内被动复制协议。']
    (out/'REPORT.md').write_text('\n'.join(lines)+'\n',encoding='utf-8')


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('action',choices=['list','audit','run','summarize'])
    p.add_argument('--root',type=Path,default=ROOT)
    p.add_argument('--bench',default=None,help='core, all, A/B/C, or comma-separated IDs; policy defaults to ST,FW')
    p.add_argument('--mode',choices=['stock','demand','policy','record'],default='stock')
    p.add_argument('--profile',choices=['smoke','small'],default='smoke')
    p.add_argument('--gpus',default='1,2,3,4')
    p.add_argument('--policies',default=','.join(DEFAULT_POLICIES))
    p.add_argument('--link-ns',default='200')
    p.add_argument('--fault-ns',default='900')
    p.add_argument('--bandwidth-gbs',type=float,default=300)
    p.add_argument('--page-bytes',type=int,default=4096)
    p.add_argument('--replica-pages-per-gpu',type=int,default=0)
    p.add_argument('--repeats',type=int,default=1)
    p.add_argument('--timeout',type=int,default=1800,help='seconds per simulation, excluding build')
    p.add_argument('--build-timeout',type=int,default=600)
    p.add_argument('--dry-run',action='store_true')
    p.add_argument('--suite',type=Path,help='existing _suites/<id> directory, for summarize only')
    args=p.parse_args()
    args.root=args.root.resolve()
    args.gpu_ids=[int(x) for x in args.gpus.split(',')]
    args.policies=args.policies.split(',')
    args.links=[int(x) for x in args.link_ns.split(',')]
    args.faults=[int(x) for x in args.fault_ns.split(',')]
    if args.page_bytes!=4096:
        p.error('catalog currently validates only 4096-byte pages; create and validate an aligned profile for other page sizes')
    if (args.gpu_ids!=[1,2,3,4] or args.repeats<1 or args.timeout<1 or args.build_timeout<1 or
        args.replica_pages_per_gpu<0 or not math.isfinite(args.bandwidth_gbs) or args.bandwidth_gbs<0 or
        any(x<0 for x in args.links+args.faults) or len(set(args.links))!=len(args.links) or len(set(args.faults))!=len(args.faults) or
        any(x not in POLICIES+EXTRA for x in args.policies) or len(set(args.policies))!=len(args.policies)):
        p.error('catalog requires GPUs 1,2,3,4, positive repeats/timeouts, nonnegative budgets and known unique policies')
    catalog=read(args.root/'benchmark/mybenchmark/catalog.json')['benchmarks']
    if args.action=='summarize':
        if not args.suite:
            p.error('summarize requires --suite')
        suite=args.suite.resolve()
        manifest=read(suite/'manifest.json')
        rows=[]
        for directory in manifest['benchmark_directories']:
            for path in Path(directory).rglob('result.json'):
                row=read(path)
                for key in ['refill_per_invalidation','dirty_fraction_p50','merged_write_epochs_p50','reread_distance_p50']:
                    if key in row:
                        row['shadow_'+key]=row.pop(key)
                db=path.parent/'metrics.sqlite3'
                if row['status'] in ['OK','INSUFFICIENT_GPU_ACTIVITY'] and db.exists():
                    activity=metric_summary(sqlite_metrics(db))
                    row.update(active_gpus=activity['active_gpus'],instructions_by_gpu=activity['instructions_by_gpu'],
                               gpu_activity_ok=activity['active_gpus']==[1,2,3,4])
                    if not row['gpu_activity_ok']:
                        row.update(status='INSUFFICIENT_GPU_ACTIVITY',reason='CU instruction counters show that not every requested GPU executed work')
                    elif row['status']=='INSUFFICIENT_GPU_ACTIVITY':
                        row.update(status='OK',reason='CU instruction counters establish four-GPU work')
                    dump(path,row)
                rows.append(row)
            app_rows=[r for r in rows if r['directory'].startswith(str(directory))]
            summarize(app_rows,Path(directory))
        summarize(rows,suite)
        print(suite/'REPORT.md')
        return 0
    if args.action=='audit':
        print(json.dumps(audit(args.root,catalog),indent=2,ensure_ascii=False))
        return 0
    selector=args.bench or 'core'
    if selector in ['core','A','B','C','all']:
        selected=[b for b in catalog if selector=='all' or b['group']==('A' if selector=='core' else selector)]
    else:
        ids=selector.split(',')
        unknown=set(ids)-{b['id'] for b in catalog}
        if unknown:
            p.error('unknown benchmark IDs: '+','.join(sorted(unknown)))
        selected=[b for b in catalog if b['id'] in ids]
    if args.action=='list' or args.dry_run:
        for b in selected:
            print(json.dumps(dict(benchmark=b['id'],sample=b['sample'],flags=b['profiles'][args.profile],
                                  adapter=b.get('replica'),blocked=b.get('blocked'),
                                  mode=args.mode,policies=args.policies if args.mode=='policy' else None),ensure_ascii=False))
        return 0
    if os.name!='posix':
        p.error('run in Linux/WSL; list/audit/dry-run can also run on Windows')
    if not (args.root/'mgpusim/go.mod').exists():
        p.error('missing mgpusim/go.mod; check --root')
    os.environ['PATH']='/usr/local/go/bin:/home/only/go/bin:'+os.environ.get('PATH','')
    stamp=dt.datetime.now(dt.timezone(dt.timedelta(hours=8))).strftime('%Y%m%d-%H%M%S')+'-'+uuid.uuid4().hex[:6]
    suite=args.root/'results/benchmark/_suites'/stamp
    suite.mkdir(parents=True,exist_ok=False)
    dirs=[args.root/'results/benchmark'/b['id']/stamp/args.mode for b in selected]
    manifest=dict(format=1,started_at=dt.datetime.now(dt.timezone(dt.timedelta(hours=8))).isoformat(),
                  argv=sys.argv,arguments={k:str(v) if isinstance(v,Path) else v for k,v in vars(args).items()},
                  benchmark_directories=list(map(str,dirs)),audit=audit(args.root,catalog),
                  time_units=dict(application_ps='picoseconds',application_us='microseconds',wall_s='host seconds'))
    dump(suite/'manifest.json',manifest)
    go=shutil.which('go')
    if not go:
        p.error('Go not found; export PATH=/usr/local/go/bin:/home/only/go/bin:$PATH')
    (suite/'go-version.txt').write_text(subprocess.check_output([go,'version'],text=True))
    fingerprint(args.root,suite)
    print('SUITE:',suite,flush=True)
    rows=[]
    for b,directory in zip(selected,dirs):
        directory.mkdir(parents=True,exist_ok=False)
        dump(directory/'benchmark.json',b)
        dump(directory/'suite.json',dict(suite=str(suite)))
        try:
            new=(policies if args.mode in ['policy','record'] else stock)(b,args,suite,directory)
        except Exception as exc:
            row=base_row(b,args,directory)
            row.update(status='BLOCKED_OR_SETUP_FAILED',reason=str(exc))
            dump(directory/'result.json',row)
            new=[row]
        rows+=new
        summarize(new,directory)
        summarize(rows,suite)
        print(b['id'],[(r.get('policy'),r['status']) for r in new],flush=True)
    dump(args.root/'results/benchmark/latest-suite.json',dict(suite=str(suite)))
    print('REPORT:',suite/'REPORT.md',flush=True)
    return 0 if all(r['status']=='OK' for r in rows) else 2


if __name__=='__main__':
    sys.exit(main())
