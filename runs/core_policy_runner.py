"""Three-policy orchestration for the ordinary-core kernel-barrier adapter."""
def run_core(b,a,suite,run,api):
    build,execute,dump,read,base,check,metrics,activity,screenmod,legacy,csv=(api[x] for x in
        ['build','execute','dump','read','base_row','check_stats','sqlite_metrics','metric_summary','load_screen','load_legacy','csvwrite'])
    if any(p not in ['inv','retain','i1-adaptive'] for p in a.policies):
        raise RuntimeError('core adapter supports inv,retain,i1-adaptive')
    old=legacy(a.root)
    binary=build(a,b['sample'],suite)
    cfg=read(a.root/'benchmark/replica-recovery/configs/inv.json')
    cfg.update(page_bytes=4096,transfer_gran_bytes=32,bandwidth_gbs=a.bandwidth_gbs,
               link_ns=a.links[0],fault_ns=a.faults[0],replica_pages_per_gpu=a.replica_pages_per_gpu)
    common=[str(binary),'-timing','-disable-rtm','-use-unified-memory','-vm-mode=demand','-verify',
            '-replica-core','-unified-gpus='+a.gpus,'-report-all','-metric-file-name=metrics',
            '-core-replica-plan-out=observed-plan.json','-core-replica-stats=stats.json',
            '-arch=gcn3','-gpu=r9nano','-page-bytes=4096',*b['profiles'][a.profile]]
    if b['id'] in ['LeNet','VGG16']:
        common+=['-dnn-synthetic-samples='+str(b['synthetic_samples'][a.profile] if a.dnn_input=='synthetic' else 0)]
        if a.dnn_input=='dataset':
            if b['id']=='VGG16':
                common=[x for x in common if not x.startswith('-input-size=')]+['-input-size=224']
            folder=a.mnist_data_folder if b['id']=='LeNet' else a.imagenet_data_folder
            if folder:common+=['-'+('mnist' if b['id']=='LeNet' else 'imagenet')+'-data-folder='+str(folder.resolve())]
    if b['id']=='BFS':common+=['-core-digest-terminal']
    dump(run/'record-config.json',{**cfg,'policy':'inv'})
    dump(run/'adapter.json',dict(adapter='core-kernel-barrier-v1',
        scheduler='balanced contiguous workgroups on four physical GPUs',
        placement='static contiguous page partitions per allocation; non-owner writers allowed',
        dnn_input=a.dnn_input if b['id'] in ['LeNet','VGG16'] else None,
        dnn_parallelism='one shared network, each kernel partitioned across physical GPUs' if b['id'] in ['LeNet','VGG16'] else None))
    record=run/'record'
    process=execute(common+['-core-replica-config='+str(run/'record-config.json')],record,a.timeout)
    if process['status']!='OK':
        r=base(b,a,record);r.update(process,reason='core record failed; replay not started')
        dump(record/'result.json',r);return [r]
    original=check(record,old)
    if original.get('adapter')!='core-kernel-barrier-v1':raise RuntimeError('not the current core adapter')
    plan=read(record/'observed-plan.json')
    replay_plan=plan
    if b['id']=='BFS':
        import copy
        replay_plan=copy.deepcopy(plan)
        replay_plan['conservative']=True
        allocations=original['core_allocations']
        mutable={allocations[0]['base'],allocations[3]['base']}
        for epoch in replay_plan['epochs']:
            if epoch.get('kind')!='kernel':continue
            declared=[]
            for allocation in allocations:
                for page in range(allocation['base'],allocation['base']+allocation['bytes'],4096):
                    for gpu in a.gpu_ids:
                        declared.append(dict(page=page,gpu=gpu,reads=list(range(128)),
                            writes=list(range(128)) if allocation['base'] in mutable else [],read_transactions=0,write_transactions=0))
            epoch['accesses']=sorted(declared,key=lambda v:(v['page'],v['gpu']))
        dump(run/'conservative-plan-scope.json',dict(reason='BFS competing discovery stores can change writer GPU across timing runs',
            mutable_buffers='levels and flag; routed home using declared may-write masks',
            read_only_buffers='CSR arrays; declared read supersets',validation='CPU final levels and terminal logical-buffer digest'))
    oracle=run/'oracle-plan.json'
    dump(oracle,replay_plan)
    ownership=original['page_ownership']
    dump(record/'page-ownership.json',ownership)
    act=activity(metrics(record/'metrics.sqlite3',record/'metrics.tsv'))
    screen=screenmod(a.root).analyze(plan,ownership,original['peak_live_managed_pages'])
    dump(run/'opportunities.json',screen);csv(run/'opportunities.tsv',[screen])
    if a.mode=='record':
        r=base(b,a,record);r.update(process,verification='CPU_PASSED',epochs=len(plan['epochs']),
            kernel_count=original['kernel_count'],active_gpus=act['active_gpus'],instructions_by_gpu=act['instructions_by_gpu'],
            gpu_activity_ok=act['active_gpus']==a.gpu_ids,reason='record only; not fair INV timing')
        if not r['gpu_activity_ok']:r['status']='INSUFFICIENT_GPU_ACTIVITY'
        dump(record/'result.json',r);return [r]
    def signature(p):
        return [(e['name'],e.get('kind'),e.get('retired_pages'),e['accesses']) for e in p['epochs']]
    rows=[];complete=[]
    for link in a.links:
      for fault in a.faults:
       for repeat in range(a.repeats):
        for policy in a.policies:
            name=f'{policy}_link-{link}ns_fault-{fault}ns_repeat-{repeat}'
            directory=run/name;config=run/(name+'.json')
            dump(config,{**cfg,'policy':policy,'link_ns':link,'fault_ns':fault})
            r=base(b,a,directory,policy,repeat)
            r.update(link_ns=link,fault_ns=fault,bandwidth_gbs=a.bandwidth_gbs)
            r.update(execute(common+['-core-replica-config='+str(config),'-core-replica-plan='+str(oracle)],directory,a.timeout))
            if r['status']=='OK':
              try:
                s=check(directory,old)
                if not s['oracle_mapping_preinstall'] or s['mapping_faults']!=0:raise RuntimeError('unfair mapping preparation')
                if s['mapping_installs']!=sum(len(e['accesses'] or []) for e in replay_plan['epochs']) or s['tlb_flush_epochs']!=len(replay_plan['epochs']):raise RuntimeError('mapping/TLB count mismatch')
                if not replay_plan.get('conservative') and signature(read(directory/'observed-plan.json'))!=signature(plan):raise RuntimeError('access masks, counts or lifetimes changed')
                if [(v['page'],v['home_gpu']) for v in s['page_ownership']]!=[(v['page'],v['home_gpu']) for v in ownership]:raise RuntimeError('physical owner placement changed')
                digest_key='terminal_sha256' if replay_plan.get('conservative') else 'validation_sha256'
                if not s.get(digest_key) or s[digest_key]!=original.get(digest_key):raise RuntimeError('data digest differs from CPU-checked record')
                if policy=='i1-adaptive' and any(s[k] for k in ['write_epoch_new_copies','write_epoch_new_copy_bytes','write_epoch_new_copy_faults','write_epoch_new_replica_reads']):raise RuntimeError('cold write-phase replica')
                act=activity(metrics(directory/'metrics.sqlite3',directory/'metrics.tsv'))
                if act['active_gpus']!=a.gpu_ids:raise RuntimeError('not all four GPUs executed instructions')
                r.update(verification='CPU_AND_TERMINAL_DIGEST_PASSED' if replay_plan.get('conservative') else 'CPU_AND_WRITE_DIGEST_PASSED',active_gpus=act['active_gpus'],instructions_by_gpu=act['instructions_by_gpu'],gpu_activity_ok=True,
                    application_us=s['application_ps']/1e6,kernel_us=s['kernel_ps']/1e6,barrier_us=s['barrier_flush_ps']/1e6,
                    recovery_us=s['recovery_wait_ps']/1e6,host_transfer_us=s.get('host_transfer_ps',0)/1e6,
                    epochs=s['epochs'],kernel_count=s['kernel_count'],copy_bytes=s['copy_bytes'],copy_faults=s['copy_faults'],
                    refills=s['refills'],invalidations=s['invalidations'],peak_replica_bytes=s['peak_replica_pages']*4096,peak_stale_bytes=s['peak_stale_pages']*4096)
                for k in ['mapping_faults','fallback_page_epochs','non_owner_write_transactions','write_epoch_existing_replica_reads','write_epoch_new_copies']:r[k]=s[k]
                for k in ['refill_per_invalidation','dirty_fraction_p50','merged_write_epochs_p50','reread_distance_p50']:r['shadow_'+k]=screen[k]
                s.update(case=name,repeat=repeat);complete.append(s)
              except Exception as exc:r.update(status='VALIDATION_FAILED',verification='FAILED_OR_INCOMPLETE',reason=str(exc))
            else:r['reason']='see run.log; failed timing excluded'
            dump(directory/'result.json',r);rows.append(r)
            print(f'{b["id"]} {name}: {r["status"]} {r.get("reason", "")} application_us={r.get("application_us")}',flush=True)
    groups={}
    for s in complete:
        c=s['config'];groups.setdefault((c['link_ns'],c['fault_ns'],c['bandwidth_gbs'],s['repeat']),[]).append(s)
    envelope=[]
    for (link,fault,bw,repeat),items in groups.items():
        inv=next((s for s in items if s['config']['policy']=='inv'),None)
        if inv is None:continue
        best=min(items,key=lambda s:(s['application_ps'],s['case']))
        envelope.append(dict(selection_scope='whole completed application; core INV/retain/adaptive candidates',
            link_ns=link,fault_ns=fault,bandwidth_gbs=bw,repeat=repeat,selected_policy=best['config']['policy'],
            selected_case=best['case'],application_us=best['application_ps']/1e6,
            inv_application_us=inv['application_ps']/1e6,candidate_cases=[s['case'] for s in items]))
    dump(run/'oracle-envelope.json',envelope)
    csv(run/'oracle-envelope.tsv',envelope,['link_ns','fault_ns','repeat','selected_policy','application_us','inv_application_us'])
    dump(run/'validated-policy-stats.json',complete)
    return rows
