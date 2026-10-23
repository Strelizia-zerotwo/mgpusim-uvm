"""Kernel-internal ideal replication: free coherence; timed fault plus bulk bytes."""
from pathlib import Path
import json

PROTOCOL='kernel-internal-ideal-v3'
POLICIES=['inv','delta','delta-sub','infinity']


def cuda_workload_metadata(directory, benchmark):
    metadata=json.loads((directory/'workload.json').read_text(encoding='utf-8'))
    complete=(metadata.get('completed') is True and (not metadata.get('convergence_required') or metadata.get('converged') is True)) if benchmark.get('research_port') else bool(metadata.get('converged'))
    if (metadata.get('algorithm')!=benchmark['id'] or not complete or
        metadata.get('implementation')!=benchmark.get('implementation','cuda-derived-research-adaptation-v1')):
        raise RuntimeError('CUDA adaptation workload metadata missing or incomplete')
    return metadata


def failure_summary(directory, status, timeout):
    if status=='TIMEOUT':
        return f'host timeout after {timeout}s; inspect progress.jsonl and run.log'
    path=directory/'run.log'
    if path.exists():
        with path.open(encoding='utf-8',errors='replace') as log:
            for index,line in enumerate(log):
                if any(marker in line for marker in ['Panic:', 'panic:', 'fatal error:', 'SIGILL:', 'SIGSEGV:']):
                    return line.strip()
                if index>=99:break
    return 'see run.log and progress.jsonl; failed timing excluded'


def collect_online(directory,old,gpu_ids,api):
    s=api['check_stats'](directory,old);p=s.get('passive',{})
    if s['adapter']!='core-'+PROTOCOL or p.get('protocol')!=PROTOCOL:
        raise RuntimeError('not the current ideal in-kernel experiment')
    free=['write_transitions','cache_control_ps_in_kernel','home_fetch_ps_in_kernel','metadata_ps','inflight_wait_ps']
    if any(p[k] for k in free) or s['barrier_flush_ps'] or s['recovery_wait_ps']:
        raise RuntimeError('ideal coherence unexpectedly charged time')
    if p['stale_read_sectors'] or s['mapping_faults']:
        raise RuntimeError('stale replica admitted or mapping faults charged')
    if not p['managed_requests'] or p['managed_requests']!=p['read_requests']+p['write_requests']:
        raise RuntimeError('actual request accounting mismatch')
    page=s['config']['page_bytes']
    if s['config']['policy']!='infinity':
        if p['charged_bytes']!=s['copy_bytes'] or p['charged_fixed_ps']!=s['copy_faults']*s['config']['fault_ns']*1000:
            raise RuntimeError('fixed service or bulk-byte accounting mismatch')
    elif p['charged_bytes'] or p['charged_fixed_ps'] or p['copy_service_ps_in_kernel']:
        raise RuntimeError('infinity charged copy service')
    elif p['fallback_home_reads']:
        raise RuntimeError('infinity could not provide a local replica; inspect physical frame capacity')
    if s['copy_faults']!=s['cold_copies']+s['refills']+s['repairs']:
        raise RuntimeError('copy event classification mismatch')
    act=api['metric_summary'](api['sqlite_metrics'](directory/'metrics.sqlite3',directory/'metrics.tsv'))
    if act['active_gpus']!=gpu_ids:raise RuntimeError('not all four GPUs executed instructions')
    fields=dict(verification='BENCHMARK_VERIFY_AND_IDEAL_INVARIANTS_PASSED',
        active_gpus=act['active_gpus'],instructions_by_gpu=act['instructions_by_gpu'],gpu_activity_ok=True,
        page_bytes=page,application_us=s['application_ps']/1e6,kernel_us=s['kernel_ps']/1e6,
        barrier_us=0,recovery_us=0,host_transfer_us=s['host_transfer_ps']/1e6,
        epochs=s['epochs'],kernel_count=s['kernel_count'],copy_bytes=s['copy_bytes'],copy_faults=s['copy_faults'],
        cold_copies=s['cold_copies'],refills=s['refills'],repairs=s['repairs'],invalidations=s['invalidations'],
        peak_replica_bytes=s['peak_replica_pages']*page,peak_stale_bytes=s['peak_stale_pages']*page,
        mapping_faults=0,charged_bytes=p['charged_bytes'],charged_fixed_us=p['charged_fixed_ps']/1e6,
        non_owner_write_transactions=s['non_owner_write_transactions'],
        intrakernel_refills=p['intrakernel_refills'],intrakernel_repairs=p['intrakernel_repairs'],
        write_after_repair=p['write_after_repair_metadata_events'],
        clean_replica_reads_during_write=p['clean_replica_reads_while_page_stale'],
        in_kernel_cache_control_us=0,in_kernel_copy_service_us=p['copy_service_ps_in_kernel']/1e6,
        write_transitions=0,stale_read_sectors=p['stale_read_sectors'],
        concurrent_repairs_high_water=p['concurrent_repairs_high_water'],fallback_home_reads=p['fallback_home_reads'],
        repair_bytes=p['repair_bytes'],repair_events=p['repair_events'],
        peak_retained_frames=p['peak_retained_frames'],peak_stale_retained_frames=p['peak_stale_retained_frames'],
        peak_retained_bytes=p['peak_retained_frames']*page,peak_stale_retained_bytes=p['peak_stale_retained_frames']*page,
        peak_backend_pinned_frames=p['peak_backend_pinned_frames'],functional_read_corrections=p['functional_read_corrections'])
    return s,fields


def run_online(b,a,suite,run,api):
    if any(p not in POLICIES for p in a.policies):raise RuntimeError('ideal supports inv,delta,delta-sub and optional infinity')
    binary=api['build'](a,b['sample'],suite)
    common=[str(binary),'-timing','-disable-rtm','-use-unified-memory','-vm-mode=demand','-verify',
            '-replica-core','-core-protocol=ideal','-unified-gpus='+a.gpus,
            '-report-all','-metric-file-name=metrics','-core-replica-plan-out=observed-accesses.json',
            '-core-replica-stats=stats.json','-core-digest-terminal',
            '-arch='+b.get('arch','gcn3'),'-gpu='+b.get('gpu','r9nano'),*b['profiles'][a.profile]]
    if b['id'] in ['LeNet','VGG16']:
        common+=['-dnn-synthetic-samples='+str(b['synthetic_samples'][a.profile] if a.dnn_input=='synthetic' else 0)]
        if a.dnn_input=='dataset':
            if b['id']=='VGG16':common=[x for x in common if not x.startswith('-input-size=')]+['-input-size=224']
            folder=a.mnist_data_folder if b['id']=='LeNet' else a.imagenet_data_folder
            if folder:common+=['-'+('mnist' if b['id']=='LeNet' else 'imagenet')+'-data-folder='+str(folder.resolve())]
    api['dump'](run/'adapter.json',dict(adapter='core-'+PROTOCOL,
        arch=b.get('arch','gcn3'),gpu=b.get('gpu','r9nano'),
        trigger='each actual read/write at AT, before translation and data caches, including TLB hits',
        future_information=False,write='immediate free metadata update and invalidation',
        timing='one fault_ns plus one bulk data-bandwidth/latency charge per copy or repair',
        infinity='synchronous functional copy/repair at actual read; zero fixed service, latency and link bandwidth; ordinary accesses still timed',
        functional_correctness='authoritative per-request snapshots and free cache operations; in-flight forwarding is ideal',
        placement='same static contiguous page partitions across policies at each page size',
        system_scope_note='ordinary loads/stores only; hardware atomics are not added'))
    old=api['load_legacy'](a.root)
    rows=[];completed=[]
    policies=['inv'] if a.mode=='record' else a.policies
    for page in a.pages:
      cfg=dict(page_bytes=page,sector_bytes=32,transfer_gran_bytes=32,fault_slots=16,
               metadata_ns=0,shootdown_ns=0,has_system_scope_sync=False,
               bandwidth_gbs=a.bandwidth_gbs,replica_pages_per_gpu=a.replica_pages_per_gpu)
      for link in a.links:
       for fault in a.faults:
        for repeat in range(a.repeats):
         for policy in policies:
            name=f'{policy}_page-{page}_link-{link}ns_fault-{fault}ns_repeat-{repeat}'
            directory=run/name;config=run/(name+'.json')
            api['dump'](config,{**cfg,'policy':policy,'link_ns':link,'fault_ns':fault})
            r=api['base_row'](b,a,directory,policy,repeat)
            r.update(protocol=PROTOCOL,page_bytes=page,link_ns=link,fault_ns=fault,
                     arch=b.get('arch','gcn3'),gpu=b.get('gpu','r9nano'),
                     bandwidth_gbs=a.bandwidth_gbs,replica_pages_per_gpu=a.replica_pages_per_gpu)
            r.update(api['execute'](common+['-page-bytes='+str(page),'-core-replica-config='+str(config)],directory,a.timeout))
            if r['status']=='OK':
              try:
                s,fields=collect_online(directory,old,a.gpu_ids,api)
                if b['id']=='FW' and s['kernel_count']!=r['fw_requested_passes']:raise RuntimeError('FW actual pass count differs from requested scope')
                if b.get('cuda_port') or b.get('research_port'):
                    metadata=cuda_workload_metadata(directory,b)
                    fields.update(workload_metadata=metadata,workload_rounds=metadata.get('rounds'),
                        workload_vertices=metadata.get('vertices'),workload_edges=metadata.get('directed_edges'),
                        workload_rows=metadata.get('rows'),workload_dim=metadata.get('dimension'),
                        workload_batches=metadata.get('batches'),input_distribution=metadata.get('distribution',metadata.get('graph_kind')),
                        workload_seed=metadata.get('seed'),workload_scope=metadata.get('scope'),
                        workload_update_semantics=metadata.get('update_semantics'),workload_implementation=metadata.get('implementation'),
                        workload_completion=metadata.get('completion_criterion','algorithm convergence'),
                        workload_colors=metadata.get('colors'),workload_levels=metadata.get('levels'),
                        workload_final_modularity=metadata.get('final_modularity'),workload_triangles=metadata.get('triangles'),
                        workload_insertions=metadata.get('refinement_insertions'),workload_constraints=metadata.get('constraints_by_kind'))
                r.update(fields)
                s.update(case=name,repeat=repeat);completed.append(s)
              except Exception as exc:r.update(status='VALIDATION_FAILED',verification='FAILED_OR_INCOMPLETE',reason=str(exc))
            else:r['reason']=failure_summary(directory,r['status'],a.timeout)
            api['dump'](directory/'result.json',r);rows.append(r)
            print(f'{b["id"]} {name}: {r["status"]} application_us={r.get("application_us")} '
                  f'cold/refill/repair={r.get("cold_copies")}/{r.get("refills")}/{r.get("repairs")} '
                  f'bytes={r.get("copy_bytes")}',flush=True)
            if r['status']!='OK':print('  reason='+r.get('reason','see run.log'),flush=True)
    groups={}
    for s in completed:
        c=s['config'];groups.setdefault((c['page_bytes'],c['link_ns'],c['fault_ns'],s['repeat']),[]).append(s)
    for items in groups.values():
        reference=next((s for s in items if s['config']['policy']=='inv'),items[0])
        for s in items:
            if ([(v['page'],v['home_gpu']) for v in s['page_ownership']]!=[(v['page'],v['home_gpu']) for v in reference['page_ownership']]
                or s.get('initial_input_sha256')!=reference.get('initial_input_sha256') or not s.get('initial_input_sha256')):
                for r in rows:
                    if Path(r['directory']).name==s['case']:
                        r.update(status='VALIDATION_FAILED',reason='physical homes or initialized inputs differ')
                        api['dump'](Path(r['directory'])/'result.json',r)
    api['dump'](run/'validated-policy-stats.json',[s for s in completed if any(r['status']=='OK' and Path(r['directory']).name==s['case'] for r in rows)])
    return rows
