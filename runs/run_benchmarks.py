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
POLICIES = 'norep,inv,delta,delta-sub,retain,i1-no-s3,i1,i1-full-stale,i1-adaptive'.split(',')
DEFAULT_POLICIES = ['inv','infinity']
EXTRA = ['i1-write-prefetch', 'i2', 'infinity']
COLUMNS = ['benchmark', 'profile', 'input_sha256', 'input_flags', 'external_input_sha256', 'fw_requested_nodes', 'fw_requested_passes', 'fw_complete_apsp', 'mode', 'policy', 'page_bytes', 'protocol', 'arch', 'gpu', 'repeat', 'link_ns', 'fault_ns', 'bandwidth_gbs', 'replica_pages_per_gpu', 'status', 'verification', 'application_us', 'kernel_us', 'host_transfer_us', 'barrier_us', 'recovery_us', 'speedup_vs_inv', 'saving_percent_vs_inv', 'saving_us_vs_inv', 'cold_copies', 'refills', 'repairs', 'copy_faults', 'copy_bytes', 'charged_bytes', 'charged_fixed_us', 'invalidations', 'peak_replica_bytes', 'peak_stale_bytes', 'peak_retained_frames', 'peak_stale_retained_frames', 'peak_retained_bytes', 'peak_stale_retained_bytes', 'peak_backend_pinned_frames', 'intrakernel_refills', 'intrakernel_repairs', 'clean_replica_reads_during_write', 'write_after_repair', 'in_kernel_copy_service_us', 'in_kernel_cache_control_us', 'functional_read_corrections', 'write_transitions', 'stale_read_sectors', 'fallback_home_reads', 'mapping_faults', 'concurrent_repairs_high_water', 'repair_bytes', 'repair_events', 'active_gpus', 'instructions_by_gpu', 'gpu_activity_ok', 'kernel_count', 'workload_rounds', 'workload_vertices', 'workload_edges', 'workload_rows', 'workload_dim', 'workload_batches', 'input_distribution', 'workload_seed', 'workload_implementation', 'workload_scope', 'workload_update_semantics', 'workload_completion', 'workload_colors', 'workload_levels', 'workload_final_modularity', 'workload_triangles', 'workload_insertions', 'workload_constraints', 'epochs', 'non_owner_write_transactions', 'wall_s', 'peak_rss_kib', 'sanity_warnings', 'reason', 'directory']


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
    overrides={'LC_ALL':'C','GODEBUG':(os.environ.get('GODEBUG','')+',randautoseed=0,randseednop=0').strip(',')}
    dump(directory / (logname + '.command.json'), dict(argv=cmd, cwd=str(cwd or directory),env=overrides))
    (directory / (logname + '.command.sh')).write_text(shlex.join(['env',*[k+'='+v for k,v in overrides.items()],*cmd]) + '\n', encoding='utf-8')
    actual = cmd
    resource_path = directory / (logname + '.resources.txt')
    if Path('/usr/bin/time').exists():
        actual = ['/usr/bin/time', '-v', '-o', str(resource_path), *cmd]
    start = time.monotonic()
    status, code = 'OK', None
    with (directory / logname).open('w', encoding='utf-8') as log:
        proc = subprocess.Popen(actual, cwd=cwd or directory, stdout=log, stderr=subprocess.STDOUT,
                                env={**os.environ,**overrides},start_new_session=(os.name == 'posix'))
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
                adapters={b['id']:'core-kernel-internal-ideal-v3' if b.get('core_replica') else b.get('replica',{}).get('workload') for b in catalog},
                scope='stock/shared and demand are platform checks; only explicit adapters support fair policy timing')


def fingerprint(root, out):
    names = []
    for folder in ['mgpusim', 'akita-patched']:
        for p in (root/folder).rglob('*'):
            if p.is_file() and p.suffix in ('.go','.mod','.sum','.hsaco','.cl'):
                names.append(p)
    names += [Path(__file__), root/'benchmark/mybenchmark/catalog.json', root/'runs/screen_opportunities.py',root/'runs/core_policy_runner.py']
    names += [root/'runs/online_policy_runner.py',root/'runs/validate_kernel_internal.py',root/'runs/run_idea_candidates.py',root/'runs/run_recommended.py',root/'runs/run_infinity.py',root/'runs/run_cuda_ports.py',root/'runs/build_cuda_ports.py',root/'runs/run_cuda_ports2.py',root/'runs/run_delta.py',root/'runs/build_cuda_ports2.py',root/'mgpusim/amd/benchmarks/cudaports2/kernel-build.json',root/'benchmark/mybenchmark/cuda-ports/SOURCES.json',root/'mgpusim/amd/benchmarks/cudaports/kernel-build.json']
    names += [root/'runs/run_irregular.py',root/'runs/build_irregular.py',root/'mgpusim/amd/benchmarks/irregular/kernel-build.json']
    names += list((root/'benchmark/mybenchmark/irregular').rglob('*'))
    names += list((root/'benchmark/replica-recovery/configs').glob('*.json'))
    names += list((root/'benchmark/mybenchmark/presets').glob('*.json'))
    old_runner = root/'benchmark/replica-recovery/run.py'
    if old_runner.exists(): names.append(old_runner)
    dump(out/'source-sha256.json', {str(p.relative_to(root)):digest(p) for p in sorted(set(names)) if p.is_file()})


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
    cmd = [go,'build','-p','2','-o',str(binary),'./amd/samples/'+sample]
    r = execute(cmd, suite/'build'/sample, args.build_timeout, cwd=args.root/'mgpusim')
    if r['status'] != 'OK':
        raise RuntimeError(f'build {r["status"]}; see {suite / "build" / sample}')
    dump(suite/'bin'/(sample+'.sha256.json'),dict(sha256=digest(binary)))
    return binary


def base_row(b,args,directory,policy=None,repeat=0):
    flags=b['profiles'][args.profile]
    signature=hashlib.sha256(json.dumps([flags,b.get('external_input_sha256')],sort_keys=True).encode()).hexdigest()
    scope={}
    if b['id']=='FW':
        values=dict(f[1:].split('=',1) for f in flags if '=' in f)
        nodes=int(values.get('node','16'));passes=int(values.get('iter','0'))
        if passes==0 or passes>nodes:passes=nodes
        scope=dict(fw_requested_nodes=nodes,fw_requested_passes=passes,fw_complete_apsp=passes==nodes)
    return dict(**scope,benchmark=b['id'],profile=b.get('input_config_name',args.profile),input_sha256=signature,
                input_flags=flags,external_input_sha256=b.get('external_input_sha256'),mode=args.mode,policy=policy,
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
    if s['model_revision'] not in [legacy.REVISION,'replica-kernel-internal-v1','replica-kernel-internal-lazy-v2','replica-kernel-internal-ideal-v3'] or s['fabric_model']!='destination-ready-v2' or not s['epochs']:
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
    if args.protocol=='kernel-internal':
        raise RuntimeError('this sample has no kernel-internal core adapter; only --protocol kernel-barrier can select its historical adapter')
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
    # Re-summarization must never retain stale speedups for a failed row.
    for r in rows:
        r.pop('speedup_vs_inv',None);r.pop('saving_percent_vs_inv',None);r.pop('saving_us_vs_inv',None);r.pop('sanity_warnings',None)
    bases={}
    for r in rows:
        if r.get('status')=='OK' and r.get('policy')=='inv':
            bases[(r['benchmark'],r['profile'],r['mode'],r['repeat'],r.get('link_ns'),r.get('fault_ns'),r.get('bandwidth_gbs'),r.get('protocol'),r.get('page_bytes'),r.get('replica_pages_per_gpu'),r.get('arch'),r.get('gpu'),r.get('input_sha256'))]=r.get('application_us')
    for r in rows:
        key=(r['benchmark'],r['profile'],r['mode'],r['repeat'],r.get('link_ns'),r.get('fault_ns'),r.get('bandwidth_gbs'),r.get('protocol'),r.get('page_bytes'),r.get('replica_pages_per_gpu'),r.get('arch'),r.get('gpu'),r.get('input_sha256'))
        base=bases.get(key)
        t=r.get('application_us')
        if r.get('status')=='OK' and base and t:
            r.update(speedup_vs_inv=base/t,saving_percent_vs_inv=100*(1-t/base),saving_us_vs_inv=base-t)
            if r.get('protocol')=='kernel-internal-ideal-v3' and r.get('policy') in ['delta','infinity'] and t>base:
                r['sanity_warnings']='application ordering reversed; inspect event counts, overlap, cache state and execution order; timing is not clamped'
    references=[dict(r) for r in rows if r.get('policy')=='infinity' and r.get('status')=='OK' and 'saving_us_vs_inv' in r]
    dump(out/'copy-free-reference.json',references)
    csvwrite(out/'copy-free-reference.tsv',references,COLUMNS)
    dump(out/'summary.json',rows)
    csvwrite(out/'summary.tsv',rows,COLUMNS)
    csvwrite(out/'summary.csv',rows,COLUMNS,',')
    failures=[r for r in rows if r['status'] not in ['OK','SKIPPED_BY_REQUEST']]
    dump(out/'failures.json',failures)
    dump(out/'skipped.json',[r for r in rows if r['status']=='SKIPPED_BY_REQUEST'])
    dump(out/'summary-provenance.json',dict(
        summarized_at=dt.datetime.now(dt.timezone(dt.timedelta(hours=8))).isoformat(),
        runner_sha256=digest(Path(__file__)),
        activity_basis='sum of retired cu_inst_count by physical GPU; empty launches do not establish activity'))
    lines=['# 本轮实验结果','',
           'application_us 是模拟应用总时间，kernel_us 已包含请求在 kernel 内等待复制/修复的时间；wall_s 是宿主运行耗时。',
           'ideal-v3 的失效、版本元数据、缓存维护、在途正确性处理均免费。INV/delta/delta-sub 的复制/修复收固定 fault 和总字节传输费用，infinity 的复制/修复全部免费。',
           '失败与超时不计收益；按页大小、协议、链路和固定服务参数分别与 INV 比较。','',
           '|应用|页字节|fault ns|link ns|策略|状态|应用 µs|kernel µs|宿主秒|INV 加速比|节省 %|',
           '|---|---:|---:|---:|---|---|---:|---:|---:|---:|---:|']
    for r in rows:
        f=lambda k: f'{r[k]:.4f}' if isinstance(r.get(k),(int,float)) else ''
        lines.append('|'+ '|'.join([r['benchmark'],str(r.get('page_bytes','')),str(r.get('fault_ns','')),str(r.get('link_ns','')),r.get('policy') or '',r['status'],f('application_us'),f('kernel_us'),f('wall_s'),f('speedup_vs_inv'),f('saving_percent_vs_inv')])+'|')
    if references:
        lines+=['','## INV 与复制免费参考','',
                '|应用|页字节|fault ns|link ns|infinity µs|INV − infinity µs|节省 %|',
                '|---|---:|---:|---:|---:|---:|---:|']
        for r in references:
            lines.append('|'+ '|'.join([r['benchmark'],str(r.get('page_bytes','')),str(r.get('fault_ns','')),str(r.get('link_ns','')),f"{r['application_us']:.4f}",f"{r['saving_us_vs_inv']:.4f}",f"{r['saving_percent_vs_inv']:.4f}"])+'|')
        lines+=['','infinity 在实际 kernel 内请求处同步完成功能复制/修复，不收 fault、链路延迟或复制带宽；普通计算、缓存访问、home 写及其 RDMA 仍计时。',
                '差值表示这两次实际运行之间的端到端差异；异步交错、合并和缓存行为可能改变，因此作为机会参考，不宣称严格数学上界。负值原样保留并提醒检查。',
                '本轮若只运行 INV/infinity，不计算 delta 捕获率；不能把不同输入、参数或源码批次的旧 delta 拼入捕获率。']
    inputs={}
    for r in rows:
        if r.get('input_flags'):inputs.setdefault((r['benchmark'],r.get('input_sha256')),(r.get('profile',''),r['input_flags'],r))
    if inputs:
        lines+=['','|应用|输入配置|实际参数|','|---|---|---|']
        fw_scopes=[]
        for (name,_),(profile,flags,row) in inputs.items():
            lines.append('|'+name+'|'+profile+'|`'+' '.join(flags).replace('|','\\|')+'`|')
            if name=='FW':
                fw_scopes+=['','FW：请求 '+str(row.get('fw_requested_passes'))+' / '+str(row.get('fw_requested_nodes'))+' 个 pass；'+('完整 APSP' if row.get('fw_complete_apsp') else '部分 pass，不能标为完整 APSP')+'。']
        lines+=fw_scopes
    live=[r for r in rows if r.get('protocol')=='kernel-internal-ideal-v3']
    if live:
        lines+=['','|应用|页字节|fault ns|link ns|策略|冷复制|整页 refill|差量 repair|传输字节|失效|保留帧峰值|过期保留帧峰值|',
                '|---|---:|---:|---:|---|---:|---:|---:|---:|---:|---:|---:|']
        for r in live:
            lines.append('|'+ '|'.join(str(r.get(k,'')) for k in ['benchmark','page_bytes','fault_ns','link_ns','policy','cold_copies','refills','repairs','copy_bytes','invalidations','peak_retained_frames','peak_stale_retained_frames'])+'|')
        lines+=['','|应用|页字节|fault ns|link ns|策略|kernel 内 refill|kernel 内 repair|干净子块免修复读|复制服务累计 µs|功能读修正|过期子块放行|',
                '|---|---:|---:|---:|---|---:|---:|---:|---:|---:|---:|']
        for r in live:
            lines.append('|'+ '|'.join(str(r.get(k,'')) for k in ['benchmark','page_bytes','fault_ns','link_ns','policy','intrakernel_refills','intrakernel_repairs','clean_replica_reads_during_write','in_kernel_copy_service_us','functional_read_corrections','stale_read_sectors'])+'|')
        lines+=['','复制服务累计时间是并发任务耗时之和，已包含在 kernel_us 中，不能重复相加。',
                'functional_read_corrections 统计理想功能层替换后端读值的次数；它与过期副本被错误放行不同，详见 KERNEL_INTERNAL.md。',
                'INV 逻辑上立即释放失效帧；后端在途读为防物理地址重用而暂留的帧，另记 peak_backend_pinned_frames。',
                '隔离的同一复制事件上 delta 费用不大于 INV，infinity 为零；应用异步调度可能改变事件数与缓存行为，反向次序原样保留并标警告。']
    scopes={r['benchmark']:r for r in rows if r.get('workload_implementation')=='irregular-algorithmic-amd-v1'}
    if scopes:
        lines+=['','新增研究实现的算法范围（非上游访存等价复现，详见 irregular/FIDELITY.md）：','',
                '|应用|实现|完成标准|算法范围|','|---|---|---|---|']
        for name,r in scopes.items():
            values=[name,r.get('workload_implementation',''),r.get('workload_completion',''),r.get('workload_scope','')]
            lines.append('|'+ '|'.join(str(v or '').replace('|','\\|') for v in values)+'|')
    warnings=[r for r in rows if r.get('sanity_warnings')]
    for r in warnings:
        lines.append('检查提醒：'+r['benchmark']+' / '+str(r.get('page_bytes'))+' / '+str(r.get('policy'))+'：'+r['sanity_warnings'])
    lines+=['','原始日志、命令、stats.json、metrics.sqlite3、metrics.tsv、observed-accesses.json 与资源统计见各应用目录。',
            'observed-accesses 仅记录执行事实，不参与后续请求的策略决策。旧版 kernel-barrier/v2 结果具有不同成本口径。']
    (out/'REPORT.md').write_text('\n'.join(lines)+'\n',encoding='utf-8')


def apply_input_config(catalog,profile,config):
    if not isinstance(config,dict) or not isinstance(config.get('name'),str) or not config['name']:
        raise ValueError('expected a nonempty name')
    inputs=config.get('benchmarks')
    if not isinstance(inputs,dict):raise ValueError('expected benchmarks mapping')
    ids={b['id'] for b in catalog}
    if set(inputs)-ids:raise ValueError('unknown benchmark IDs: '+','.join(sorted(set(inputs)-ids)))
    for name,flags in inputs.items():
        if not isinstance(flags,list) or not flags or any(not isinstance(f,str) or not f.startswith('-') for f in flags):
            raise ValueError('benchmark arguments must be a nonempty list of flag strings: '+name)
    for b in catalog:
        if b['id'] in inputs:
            b['profiles'][profile]=list(inputs[b['id']]);b['input_config_name']=config['name']


def apply_bfs_graph(b,profile,path,sha256):
    flags=[f for f in b['profiles'][profile] if not f.startswith(('-node=','-degree=','-depth=','-load-graph='))]
    b['profiles'][profile]=flags+['-load-graph='+str(path),'-depth=0']
    b['external_input_sha256']=sha256


def main(argv=None):
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('action',choices=['list','audit','run','summarize'])
    p.add_argument('--root',type=Path,default=ROOT)
    p.add_argument('--bench',default=None,help='core (default), all, A/B/C, or comma-separated IDs')
    p.add_argument('--mode',choices=['stock','demand','policy','record'],default='stock')
    p.add_argument('--protocol',choices=['kernel-internal','kernel-barrier'],default='kernel-internal',help='core policy execution; default uses actual in-kernel requests with no future plan')
    p.add_argument('--profile',choices=['smoke','small'],default='smoke')
    p.add_argument('--input-config',type=Path,help='JSON file with name and benchmark argument lists; overrides selected profile inputs')
    p.add_argument('--bfs-graph',type=Path,help='real BFS edge-list graph; replaces -node/-degree with -load-graph and unlimited depth')
    p.add_argument('--lud-size',type=int,help='override LUD matrix size; positive multiple of 16')
    p.add_argument('--include-slow-dnn',action='store_true',help='explicitly enable LeNet/VGG16; skipped by default at user request')
    p.add_argument('--dnn-input',choices=['synthetic','dataset'],default='synthetic',help='explicit deterministic smoke data or real training datasets')
    p.add_argument('--mnist-data-folder',type=Path)
    p.add_argument('--imagenet-data-folder',type=Path)
    p.add_argument('--gpus',default='1,2,3,4')
    p.add_argument('--policies',default=','.join(DEFAULT_POLICIES))
    p.add_argument('--link-ns',default='200')
    p.add_argument('--fault-ns',default='900')
    p.add_argument('--bandwidth-gbs',type=float,default=300)
    p.add_argument('--page-bytes',default='4096',help='one page size or comma-separated sweep: 4096,65536,2097152')
    p.add_argument('--replica-pages-per-gpu',type=int,default=0)
    p.add_argument('--repeats',type=int,default=1)
    p.add_argument('--timeout',type=int,default=1800,help='seconds per simulation, excluding build')
    p.add_argument('--build-timeout',type=int,default=600)
    p.add_argument('--dry-run',action='store_true')
    p.add_argument('--suite',type=Path,help='existing _suites/<id> directory, for summarize only')
    args=p.parse_args(argv)
    args.root=args.root.resolve()
    args.gpu_ids=[int(x) for x in args.gpus.split(',')]
    args.policies=args.policies.split(',')
    args.links=[int(x) for x in args.link_ns.split(',')]
    args.faults=[int(x) for x in args.fault_ns.split(',')]
    try:
        args.pages=[int(x) for x in args.page_bytes.split(',')]
    except ValueError:
        p.error('--page-bytes requires integers separated by commas')
    args.page_bytes=args.pages[0]
    if not args.pages or any(x not in [4096,65536,2097152] for x in args.pages) or len(set(args.pages))!=len(args.pages):
        p.error('supported page sizes: 4096,65536,2097152, without duplicates')
    if args.protocol=='kernel-barrier' and args.pages!=[4096]:
        p.error('historical kernel-barrier adapter requires 4096-byte pages')
    if args.action=='run' and args.mode in ['policy','record'] and args.protocol=='kernel-internal' and any(x not in ['inv','delta','delta-sub','infinity'] for x in args.policies):
        p.error('ideal kernel-internal policies: inv,infinity (defaults); delta,delta-sub also available')
    if (args.gpu_ids!=[1,2,3,4] or args.repeats<1 or args.timeout<1 or args.build_timeout<1 or
        args.replica_pages_per_gpu<0 or not math.isfinite(args.bandwidth_gbs) or args.bandwidth_gbs<0 or
        any(x<0 for x in args.links+args.faults) or len(set(args.links))!=len(args.links) or len(set(args.faults))!=len(args.faults) or
        any(x not in POLICIES+EXTRA for x in args.policies) or len(set(args.policies))!=len(args.policies)):
        p.error('catalog requires GPUs 1,2,3,4, positive repeats/timeouts, nonnegative budgets and known unique policies')
    catalog=read(args.root/'benchmark/mybenchmark/catalog.json')['benchmarks']
    if args.input_config:
        try:
            config=read(args.input_config.resolve())
            apply_input_config(catalog,args.profile,config)
        except (ValueError,KeyError,TypeError,OSError) as exc:
            p.error('invalid --input-config: '+str(exc))
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
    if selector in ['core','A','B','C','E','F','all']:
        selected=[b for b in catalog if selector=='all' or b['group']==('A' if selector=='core' else selector)]
    else:
        ids=selector.split(',')
        unknown=set(ids)-{b['id'] for b in catalog}
        if unknown:
            p.error('unknown benchmark IDs: '+','.join(sorted(unknown)))
        if len(ids)!=len(set(ids)):p.error('--bench IDs must be unique')
        lookup={b['id']:b for b in catalog}
        selected=[lookup[name] for name in ids]
    if args.bfs_graph:
        if not any(b['id']=='BFS' for b in selected):p.error('--bfs-graph requires selecting BFS')
        graph=args.bfs_graph.resolve()
        if not graph.is_file():p.error('--bfs-graph file does not exist: '+str(graph))
        for b in selected:
            if b['id']=='BFS':
                apply_bfs_graph(b,args.profile,graph,digest(graph))
    if args.lud_size is not None:
        if args.lud_size<=0 or args.lud_size%16:p.error('--lud-size must be a positive multiple of 16')
        if not any(b['id']=='lud' for b in selected):p.error('--lud-size requires selecting lud')
        for b in selected:
            if b['id']=='lud':b['profiles'][args.profile]=['-size='+str(args.lud_size)]
    if args.action=='list' or args.dry_run:
        for b in selected:
            print(json.dumps(dict(benchmark=b['id'],sample=b['sample'],arch=b['arch'],gpu=b['gpu'],profile=b.get('input_config_name',args.profile),flags=b['profiles'][args.profile],
                                  adapter=('core-kernel-internal-ideal-v3' if args.protocol=='kernel-internal' else 'core-kernel-barrier-v1') if b.get('core_replica') and args.mode in ['policy','record'] else b.get('replica'),
                                  blocked=None if b.get('core_replica') and args.mode in ['policy','record'] else b.get('blocked'),
                                  skipped_by_request=b['id'] in ['LeNet','VGG16'] and not args.include_slow_dnn,
                                  page_sizes=args.pages,mode=args.mode,policies=args.policies if args.mode=='policy' else None),ensure_ascii=False))
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
    if args.input_config:dump(suite/'input-config.json',config)
    print('SUITE:',suite,flush=True)
    rows=[]
    for b,directory in zip(selected,dirs):
        directory.mkdir(parents=True,exist_ok=False)
        dump(directory/'benchmark.json',b)
        dump(directory/'suite.json',dict(suite=str(suite)))
        try:
            if b['id'] in ['LeNet','VGG16'] and not args.include_slow_dnn:
                r=base_row(b,args,directory)
                r.update(status='SKIPPED_BY_REQUEST',reason='LeNet/VGG16 are expensive; excluded from the default core experiment at user request')
                dump(directory/'result.json',r)
                new=[r]
            else:
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
    return 0 if all(r['status'] in ['OK','SKIPPED_BY_REQUEST'] for r in rows) else 2


if __name__=='__main__':
    sys.exit(main())
