package replica
import (
    "encoding/binary"
    "fmt"
    "github.com/sarchlab/mgpusim/v5/amd/driver"
)

type allocation struct {bytes uint64; ctx *driver.Context}

// AttachCore adapts ordinary sample API submissions to drained global epochs.
// Allocation owners are static contiguous page partitions, independent of policy.
// Unified dispatch still targets distinct GPUs with independent MMUs/caches.
func AttachCore(s *Session,gpus []int) {
    regions:=map[driver.Ptr]allocation{}
    kernelID,hostID:=0,0
    s.d.ReplicaBalancedDispatch=true
    s.d.ReplicaHooks=&driver.ReplicaHooks{
        Allocate:func(ctx *driver.Context,bytes uint64) driver.Ptr {
            s.d.WaitForEngineIdle()
            if ctx.PID()!=s.ctx.PID() {panic("replica core requires a common address space")}
            if bytes==0 {panic("zero-size managed allocation")}
            page:=s.Config.PageBytes
            n:=(bytes+page-1)/page
            var base driver.Ptr
            for i,g:=range gpus {
                lo,hi:=(n*uint64(i)+uint64(len(gpus))-1)/uint64(len(gpus)),(n*uint64(i+1)+uint64(len(gpus))-1)/uint64(len(gpus))
                if hi==lo {continue}
                p:=s.d.AllocateUnifiedMemoryOnGPU(ctx,(hi-lo)*page,g)
                if base==0 {base=p-driver.Ptr(lo*page)}
                if p!=base+driver.Ptr(lo*page) {panic("noncontiguous managed allocation")}
            }
            s.Manage(base,bytes)
            regions[base]=allocation{bytes,ctx}
            live:=uint64(0)
            for _,st:=range s.pages {if !st.Retired {live++}}
            s.Stats.PeakLiveManagedPages=max(s.Stats.PeakLiveManagedPages,live)
            return base
        },
        Kernel:func(q *driver.CommandQueue,work func()) {
            kernelID++
            s.Epoch(fmt.Sprintf("kernel/%06d",kernelID),work)
        },
        HostWrite:func(q *driver.CommandQueue,ptr driver.Ptr,src interface{},work func()) bool {
            size:=binary.Size(src)
            if size<0 {panic("unsupported H2D source")}
            end:=uint64(ptr)+uint64(size)
            managed:=false
            for base,r:=range regions {if uint64(ptr)<uint64(base)+r.bytes && end>uint64(base) {managed=true;break}}
            if !managed {return false}
            s.d.WaitForEngineIdle()
            // Before the first kernel, initial data establish version zero.
            if s.at==0 {work();return true}
            hostID++
            s.hostPhase=true
            defer func(){s.hostPhase=false}()
            s.Epoch(fmt.Sprintf("host-write/%06d",hostID),func(){
                for pos:=uint64(ptr);pos<end; {
                    page:=pos/s.Config.PageBytes*s.Config.PageBytes
                    hi:=min(end,page+s.Config.PageBytes)
                    if st:=s.pages[page];st!=nil && !st.Retired {s.observe(int(st.Home.DeviceID),pos,hi-pos,nil,true)}
                    pos=hi
                }
                work()
            })
            return true
        },
        Free:func(ctx *driver.Context,ptr driver.Ptr) bool {
            r,ok:=regions[ptr];if !ok {return false}
            s.d.WaitForEngineIdle()
            s.d.FlushAtBarrier(ctx)
            s.invalidateTLBs()
            for p:=uint64(ptr);p<uint64(ptr)+r.bytes;p+=s.Config.PageBytes {
                st:=s.pages[p]
                for g,c:=range st.Copies {s.release(g,c)}
                s.tables.RetireAtBarrier(ctx.PID(),p)
                s.d.ReleaseReplicaFrame(int(st.Home.DeviceID),st.Home.PAddr)
                st.Retired=true
                s.Stats.RetiredManagedPages++
            }
            s.d.RetireReplicaBuffer(ctx,ptr,r.bytes)
            delete(regions,ptr)
            return true
        },
    }
}
