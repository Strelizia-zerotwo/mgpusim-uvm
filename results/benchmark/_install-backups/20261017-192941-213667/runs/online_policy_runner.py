"""Execute live, request-triggered replication inside kernels; no future plan."""
from pathlib import Path

def run_online(b, a, suite, run, api):
    build,execute,dump,read,base,check,metrics,activity,csv=(api[x] for x in
        ['build','execute','dump','read','base_row','check_stats','sqlite_metrics','metric_summary','csvwrite'])
    if any(p not in ['inv','retain','i1-adaptive'] for p in a.policies):
        raise RuntimeError('online core protocol supports inv,retain,i1-adaptive')
    binary=build(a,b['sample'],suite)
    cfg=read(a.root/'benchmark/replica-recovery/configs/inv.json')
    cfg.update(page_bytes=4096,sector_bytes=32,transfer_gran_bytes=32,bandwidth_gbs=a.bandwidth_gbs,
               replica_pages_per_gpu=a.replica_pages_per_gpu)
    common=[str(binary),'-timing','-disable-rtm','-use-unified-memory','-vm-mode=demand','-verify',
            '-replica-core','-core-protocol=passive','-unified-gpus='+a.gpus,
            '-report-all','-metric-file-name=metrics','-core-replica-plan-out=observed-accesses.json',
            '-core-replica-stats=stats.json','-core-digest-terminal',
            '-arch=gcn3','-gpu=r9nano','-page-bytes=4096',*b['profiles'][a.profile]]
    if b['id'] in ['LeNet','VGG16']:
        common+=['-dnn-synthetic-samples='+str(b['synthetic_samples'][a.profile] if a.dnn_input=='synthetic' else 0)]
        if a.dnn_input=='dataset':
            if b['id']=='VGG16':common=[x for x in common if not x.startswith('-input-size=')]+['-input-size=224']
            folder=a.mnist_data_folder if b['id']=='LeNet' else a.imagenet_data_folder
            if folder:common+=['-'+('mnist' if b['id']=='LeNet' else 'imagenet')+'-data-folder='+str(folder.resolve())]
    dump(run/'adapter.json',dict(adapter='core-kernel-internal-lazy-v2',
        trigger='actual read/write requests before data-cache admission, including TLB hits',
        future_information=False,adaptive='previous observed read-interval footprint and isolated service cost',
        scheduler='balanced contiguous workgroups on four physical GPUs',
        cache_control='per-GPU reader queue; page-local leases; running clean-range invalidation; real home L2 fetch; zero write-triggered transitions',
        time_accounting='kernel_us includes in-kernel permission, cache-control and copy waits',
        placement='same static contiguous page partitions for every policy'))
    old=api['load_legacy'](a.root)
    rows=[];completed=[]
    policies=['inv'] if a.mode=='record' else a.policies
    for link in a.links:
      for fault in a.faults:
       for repeat in range(a.repeats):
        for policy in policies:
            name=f'{policy}_link-{link}ns_fault-{fault}ns_repeat-{repeat}'
            directory=run/name;config=run/(name+'.json')
            dump(config,{**cfg,'policy':policy,'link_ns':link,'fault_ns':fault})
            r=base(b,a,directory,policy,repeat)
            r.update(protocol='kernel-internal-lazy-v2',link_ns=link,fault_ns=fault,bandwidth_gbs=a.bandwidth_gbs)
            r.update(execute(common+['-core-replica-config='+str(config)],directory,a.timeout))
            if r['status']=='OK':
              try:
                s=check(directory,old);p=s.get('passive',{})
                if s['adapter']!='core-kernel-internal-lazy-v2' or p.get('protocol')!='kernel-internal-lazy-v2':
                    raise RuntimeError('not the kernel-internal online protocol')
                if p['write_transitions'] or p['stale_read_sectors']:
                    raise RuntimeError('write-triggered cache control or stale read')
                if s['mapping_faults'] or s['recovery_wait_ps']:
                    raise RuntimeError('mapping faults or double-counted in-kernel recovery')
                if not p['managed_requests'] or p['managed_requests']!=p['read_requests']+p['write_requests']:
                    raise RuntimeError('actual request accounting mismatch')
                act=activity(metrics(directory/'metrics.sqlite3',directory/'metrics.tsv'))
                if act['active_gpus']!=a.gpu_ids:raise RuntimeError('not all four GPUs executed instructions')
                r.update(verification='BENCHMARK_VERIFY_AND_PROTOCOL_DRAIN_PASSED',
                    active_gpus=act['active_gpus'],instructions_by_gpu=act['instructions_by_gpu'],gpu_activity_ok=True,
                    application_us=s['application_ps']/1e6,kernel_us=s['kernel_ps']/1e6,
                    barrier_us=s['barrier_flush_ps']/1e6,recovery_us=0,host_transfer_us=s['host_transfer_ps']/1e6,
                    epochs=s['epochs'],kernel_count=s['kernel_count'],copy_bytes=s['copy_bytes'],copy_faults=s['copy_faults'],
                    refills=s['refills'],repairs=s['repairs'],invalidations=s['invalidations'],
                    peak_replica_bytes=s['peak_replica_pages']*4096,peak_stale_bytes=s['peak_stale_pages']*4096,
                    mapping_faults=s['mapping_faults'],fallback_page_epochs=0,
                    non_owner_write_transactions=s['non_owner_write_transactions'],
                    write_epoch_existing_replica_reads=s['write_epoch_existing_replica_reads'],write_epoch_new_copies=0,
                    intrakernel_refills=p['intrakernel_refills'],intrakernel_repairs=p['intrakernel_repairs'],
                    write_after_repair=p['write_after_repair_invalidations'],
                    clean_replica_reads_during_write=p['clean_replica_reads_during_write'],
                    adaptive_demand_repairs=p['adaptive_demand_repairs'],adaptive_full_stale_repairs=p['adaptive_full_stale_repairs'],
                    in_kernel_cache_control_us=p['cache_control_ps_in_kernel']/1e6,
                    in_kernel_copy_service_us=p['copy_service_ps_in_kernel']/1e6,
                    write_transitions=p['write_transitions'],stale_read_sectors=p['stale_read_sectors'],
                    home_fetch_us=p['home_fetch_ps_in_kernel']/1e6,home_fetch_bytes=p['home_fetch_bytes'],
                    home_flush_events=p['home_flush_events'],replica_invalidate_events=p['replica_invalidate_events'],
                    replica_invalidate_bytes=p['replica_invalidate_bytes'],home_l1_invalidate_events=p['home_l1_invalidate_events'],
                    concurrent_repairs_high_water=p['concurrent_repairs_high_water'],fallback_home_reads=p['fallback_home_reads'],
                    repair_bytes=p['repair_bytes'],repair_events=p['repair_events'],repair_granularities=p['repair_granularities'],
                    repairs_by_trigger=p['repairs_by_trigger'])
                s.update(case=name,repeat=repeat);completed.append(s)
              except Exception as exc:r.update(status='VALIDATION_FAILED',verification='FAILED_OR_INCOMPLETE',reason=str(exc))
            else:r['reason']='see run.log; failed timing excluded'
            dump(directory/'result.json',r);rows.append(r)
            print(f'{b["id"]} {name}: {r["status"]} application_us={r.get("application_us")} '
                  f'cold/INV-refill/repair={s.get("cold_copies",0) if r["status"]=="OK" else "?"}/{r.get("refills")}/{r.get("repairs")} '
                  f'in-kernel-refill/repair={r.get("intrakernel_refills")}/{r.get("intrakernel_repairs")} '
                  f'write-after-repair={r.get("write_after_repair")} clean-local-read={r.get("clean_replica_reads_during_write")}',flush=True)
    # Compare homes and the complete host-initialized input across policies.
    groups={}
    for s in completed:
        c=s['config'];groups.setdefault((c['link_ns'],c['fault_ns'],s['repeat']),[]).append(s)
    envelopes=[]
    for key,items in groups.items():
        reference=next((s for s in items if s['config']['policy']=='inv'),items[0])
        for s in items:
            if ([(v['page'],v['home_gpu']) for v in s['page_ownership']]!=[(v['page'],v['home_gpu']) for v in reference['page_ownership']]
                or s.get('initial_input_sha256')!=reference.get('initial_input_sha256') or not s.get('initial_input_sha256')):
                for r in rows:
                    if Path(r['directory']).name==s['case']:
                        r.update(status='VALIDATION_FAILED',reason='physical homes or host-initialized inputs differ')
                        dump(Path(r['directory'])/'result.json',r)
        successful=[s for s in items if any(r['status']=='OK' and Path(r['directory']).name==s['case'] for r in rows)]
        inv=next((s for s in successful if s['config']['policy']=='inv'),None)
        if inv:
            best=min(successful,key=lambda s:s['application_ps'])
            envelopes.append(dict(selection_scope='whole completed application; offline selection of actual online-policy runs',
                link_ns=key[0],fault_ns=key[1],repeat=key[2],selected_policy=best['config']['policy'],
                application_us=best['application_ps']/1e6,inv_application_us=inv['application_ps']/1e6))
    dump(run/'validated-policy-stats.json',[s for s in completed if any(r['status']=='OK' and Path(r['directory']).name==s['case'] for r in rows)])
    dump(run/'oracle-envelope.json',envelopes);csv(run/'oracle-envelope.tsv',envelopes)
    return rows
