// Algorithmic AMD adaptations. See benchmark/mybenchmark/irregular/FIDELITY.md.
// Explicit builtins avoid hidden-offset ABI dependencies. No global atomics,
// no protocol work, and no policy decision is embedded in these kernels.
typedef unsigned int u32;
#define ID (__builtin_amdgcn_workgroup_id_x()*256u+__builtin_amdgcn_workitem_id_x())
#define LID (__builtin_amdgcn_workitem_id_x())
#define WG (__builtin_amdgcn_workgroup_id_x())
#define NONE 0xffffffffu
#define ARGS volatile __global u32 *a0, volatile __global u32 *a1, volatile __global u32 *a2, volatile __global u32 *a3, volatile __global u32 *a4, volatile __global u32 *a5, volatile __global u32 *a6, volatile __global u32 *a7, volatile __global u32 *a8, volatile __global u32 *a9, volatile __global u32 *a10, volatile __global u32 *a11, u32 n,u32 k,u32 step,u32 extra,u32 q,u32 r,u32 s,u32 t,float f0,float f1,float f2,float f3
#define KERNEL __attribute__((reqd_work_group_size(256,1,1))) __kernel
#define F(a) ((volatile __global float *)(a))
static inline void count(u32 x, volatile __local u32 *buf, volatile __global u32 *out) {
  u32 l=LID; buf[l]=x; __builtin_amdgcn_s_barrier();
  for(u32 d=128;d;d>>=1){if(l<d)buf[l]+=buf[l+d];__builtin_amdgcn_s_barrier();}
  if(!l)out[WG]=buf[0];
}

// a0/1 incoming CSR, a2/3 outgoing CSR; a4 labels; a5/6 reach masks;
// a7 group progress. Labels NONE means not assigned to an SCC yet.
KERNEL void scc_trim(ARGS) {
  volatile __local u32 buf[256];u32 v=ID,changed=0;
  if(v<n && a4[v]==NONE){u32 in=0,out=0;
    for(u32 e=a0[v];e<a0[v+1];++e)if(a4[a1[e]]==NONE){in=1;break;}
    for(u32 e=a2[v];e<a2[v+1];++e)if(a4[a3[e]]==NONE){out=1;break;}
    if(!in||!out){a4[v]=v;changed=1;}
  }count(changed,buf,a7);
}
KERNEL void scc_seed(ARGS){u32 v=ID;if(v<n){a5[v]=(v==k);a6[v]=(v==k);}}
KERNEL void scc_reach(ARGS){
  volatile __local u32 buf[256];u32 v=ID,changed=0;
  if(v<n&&a4[v]==NONE&&a5[v]==0){
    for(u32 e=a0[v];e<a0[v+1];++e){u32 u=a1[e];
      if(a4[u]==NONE&&a5[u]){a5[v]=1;changed=1;break;}}
  }count(changed,buf,a7);
}
KERNEL void scc_assign(ARGS){u32 v=ID;if(v<n&&a4[v]==NONE&&a5[v]&&a6[v])a4[v]=k;}
KERNEL void scc_pivot(ARGS){
  volatile __local u32 buf[256];u32 l=LID,v=ID;buf[l]=(v<n&&a4[v]==NONE)?v:NONE;
  __builtin_amdgcn_s_barrier();for(u32 d=128;d;d>>=1){
    if(l<d&&buf[l+d]<buf[l])buf[l]=buf[l+d];__builtin_amdgcn_s_barrier();}
  if(!l)a7[WG]=buf[0];
}

// PPR follows the source's filter / advance / dense residual-copy phases.
// a0/1 incoming CSR; a2 degrees; a3 p; a4 r; a5 r_prime; a6 active;
// a7 next-active; a8 progress. k=seed; f0=2alpha/(1+alpha), f1=(1-alpha)/(1+alpha).
KERNEL void ppr_filter(ARGS){u32 v=ID;if(v<n&&a6[v]){
  F(a3)[v]+=f0*F(a4)[v];F(a5)[v]=0.0f;}}
KERNEL void ppr_advance(ARGS){u32 v=ID;if(v<n){
  float value=F(a5)[v];
  for(u32 e=a0[v];e<a0[v+1];++e){u32 u=a1[e];
    if(a6[u]){value+=f1*F(a4)[u]/(float)a2[u];F(a5)[v]=value;}}
  a7[v]=(value>=(float)a2[v]*f2);
}}
KERNEL void ppr_copy(ARGS){volatile __local u32 buf[256];u32 v=ID,x=0;
  if(v<n){F(a4)[v]=F(a5)[v];a6[v]=a7[v];x=a7[v];}count(x,buf,a8);}

// Color scheduling updates the original vector/labels in place. Adjacent
// vertices never share a color. No proposal or old-state array is used.
KERNEL void symgs(ARGS){u32 v=ID;if(v<n&&a2[v]==k){float sum=F(a4)[v];
  for(u32 e=a0[v];e<a0[v+1];++e)sum+=F(a3)[a1[e]];
  F(a3)[v]=sum/(float)(a0[v+1]-a0[v]+1);}}
KERNEL void label_propagation(ARGS){volatile __local u32 buf[256];u32 v=ID,changed=0;
  if(v<n&&a2[v]==k){u32 old=a3[v],best=old,bestCount=0;
    for(u32 e=a0[v];e<a0[v+1];++e){u32 candidate=a3[a1[e]],votes=0;
      for(u32 j=a0[v];j<a0[v+1];++j)votes+=(a3[a1[j]]==candidate);
      if(votes>bestCount||(votes==bestCount&&candidate==old)){
        best=candidate;bestCount=votes;}}
    // Retain the old label on ties. Each changed vertex strictly increases
    // agreeing undirected edges, ensuring finite termination.
    u32 oldCount=0;for(u32 e=a0[v];e<a0[v+1];++e)oldCount+=(a3[a1[e]]==old);
    if(bestCount>oldCount){a3[v]=best;changed=1;}
  }count(changed,buf,a7);}

// Full finite Andersen closure: address facts are initialized on the host;
// a0 row offsets for copy/load constraints, a1 source, a2 kind (0=copy,1=load),
// a3 stores [pointer,rhs], a4 points-to bitsets, a7 progress;
// k=objects,step=words,extra=store-count. Each pointer row has one writer.
KERNEL void points_to(ARGS){volatile __local u32 buf[256];u32 v=ID,changed=0;
  if(v<n){for(u32 w=0;w<step;++w){u32 old=a4[v*step+w],value=old;
    for(u32 e=a0[v];e<a0[v+1];++e){u32 src=a1[e];
      if(a2[e]==0)value|=a4[src*step+w];
      else for(u32 o=0;o<k;++o)if(a4[src*step+(o>>5)]&(1u<<(o&31)))value|=a4[o*step+w];}
    if(v<k)for(u32 e=0;e<extra;++e){u32 pointer=a3[2*e],rhs=a3[2*e+1];
      if(a4[pointer*step+(v>>5)]&(1u<<(v&31)))value|=a4[rhs*step+w];}
    if(value!=old){a4[v*step+w]=value;changed=1;}
  }}count(changed,buf,a7);
}

// Multilevel Louvain: CSR a0/1 with float weights a2; labels a3, degree a4,
// community totals a5, proposals a6, group counts a7, accepted a8.
KERNEL void louvain_degree(ARGS){u32 v=ID;if(v<n){float sum=0;
  for(u32 e=a0[v];e<a0[v+1];++e)sum+=F(a2)[e];F(a4)[v]=sum;a3[v]=v;}}
KERNEL void louvain_totals(ARGS){u32 c=ID;if(c<n){float sum=0;
  for(u32 v=0;v<n;++v)if(a3[v]==c)sum+=F(a4)[v];F(a5)[c]=sum;}}
static inline float links(u32 v,u32 c,volatile __global u32 *row,
 volatile __global u32 *col,volatile __global float *weight,volatile __global u32 *label){
  float sum=0;for(u32 e=row[v];e<row[v+1];++e)if(col[e]!=v&&label[col[e]]==c)sum+=weight[e];return sum;}
KERNEL void louvain_propose(ARGS){u32 v=ID;if(v<n){u32 old=a3[v],best=old;
  float degree=F(a4)[v],oldLinks=links(v,old,a0,a1,F(a2),a3),gainBest=f1;
  for(u32 e=a0[v];e<a0[v+1];++e){u32 c=a3[a1[e]];if(c==old)continue;
    float gain=links(v,c,a0,a1,F(a2),a3)-oldLinks+degree*(F(a5)[old]-degree-F(a5)[c])/f0;
    if(gain>gainBest||(gain==gainBest&&best!=old&&c<best)){gainBest=gain;best=c;}}
  a6[v]=best;
}}
KERNEL void louvain_select(ARGS){volatile __local u32 buf[256];u32 v=ID,accept=0;
  if(v<n){u32 src=a3[v],dst=a6[v];if(src!=dst){accept=1;
    // Deterministic disjoint-community moves avoid atomics and stale totals.
    // Both proposal and label arrays stay immutable throughout this phase.
    for(u32 u=0;u<v;++u){u32 os=a3[u],od=a6[u];
      if(os!=od&&(src==os||src==od||dst==os||dst==od)){accept=0;break;}}}
    a8[v]=accept;
  }count(accept,buf,a7);
}
KERNEL void louvain_apply(ARGS){u32 v=ID;if(v<n&&a8[v])a3[v]=a6[v];}
// a9 maps old community IDs to compact IDs, a10 original vertex membership.
KERNEL void louvain_compact(ARGS){volatile __local u32 buf[256];u32 c=ID,used=0;
  if(c<n){u32 rank=0;for(u32 j=0;j<n;++j)if(a3[j]==c){used=1;break;}
    if(used)for(u32 j=0;j<c;++j){u32 found=0;
      for(u32 v=0;v<n;++v)if(a3[v]==j){found=1;break;}rank+=found;}
    a9[c]=used?rank:NONE;
  }count(used,buf,a7);
}
KERNEL void louvain_membership(ARGS){u32 v=ID;if(v<k)a10[v]=a9[a3[a10[v]]];}
// a11 packed edge records [src,dst,float-weight-bits,pad], padded to step.
KERNEL void louvain_edges(ARGS){u32 e=ID;if(e<step){
  u32 src=NONE,dst=NONE,bits=0;
  if(e<k){u32 lo=0,hi=n;while(lo<hi){u32 mid=(lo+hi)>>1;
    if(a0[mid+1]<=e)lo=mid+1;else hi=mid;}
    src=a9[a3[lo]];dst=a9[a3[a1[e]]];bits=a2[e];}
  a11[4*e]=src;a11[4*e+1]=dst;a11[4*e+2]=bits;
}}
KERNEL void louvain_sort(ARGS){u32 i=ID;if(i>=n)return;u32 j=i^k;if(j<=i)return;
  u32 si=a11[4*i],sj=a11[4*j],di=a11[4*i+1],dj=a11[4*j+1];
  u32 greater=si>sj||(si==sj&&di>dj),less=si<sj||(si==sj&&di<dj);
  if(((i&step)==0)?greater:less)for(u32 c=0;c<3;++c){u32 x=a11[4*i+c];a11[4*i+c]=a11[4*j+c];a11[4*j+c]=x;}}
KERNEL void louvain_csr(ARGS){u32 i=ID;
  if(i<=n){u32 lo=0,hi=k;while(lo<hi){u32 mid=(lo+hi)>>1;if(a11[4*mid]<i)lo=mid+1;else hi=mid;}a0[i]=lo;}
  if(i<k){a1[i]=a11[4*i+1];a2[i]=a11[4*i+2];}}

// DMR: Bowyer-Watson insertion with GPU cavity extraction and free-slot reuse.
// a0 triangles [a,b,c,alive]; a1 xy float coordinates; a2 cavity; a3 free flags;
// a4 boundary flags (3 per triangle); a5 boundary endpoint pairs;
// a6 group counts; a7 host-prefix offsets; a8 rank->edge/slot; a9 other rank.
static inline float orient(float ax,float ay,float bx,float by,float cx,float cy){return (bx-ax)*(cy-ay)-(by-ay)*(cx-ax);}
static inline float circle(u32 a,u32 b,u32 c,u32 p,volatile __global float *xy){
  float ax=xy[2*a]-xy[2*p],ay=xy[2*a+1]-xy[2*p+1];
  float bx=xy[2*b]-xy[2*p],by=xy[2*b+1]-xy[2*p+1];
  float cx=xy[2*c]-xy[2*p],cy=xy[2*c+1]-xy[2*p+1];
  return (ax*ax+ay*ay)*(bx*cy-by*cx)-(bx*bx+by*by)*(ax*cy-ay*cx)+(cx*cx+cy*cy)*(ax*by-ay*bx);}
KERNEL void mesh_cavity(ARGS){u32 i=ID;if(i<n){u32 alive=a0[4*i+3],inside=0;
  if(alive)inside=circle(a0[4*i],a0[4*i+1],a0[4*i+2],k,F(a1))>0.0f;
  a2[i]=inside;a3[i]=!alive||inside;}}
KERNEL void mesh_boundary(ARGS){u32 e=ID;if(e<3*n){u32 i=e/3,side=e%3,boundary=0;
  if(a2[i]){u32 u=a0[4*i+side],v=a0[4*i+(side+1)%3];boundary=1;
    for(u32 j=0;j<n&&boundary;++j)if(j!=i&&a2[j])for(u32 d=0;d<3;++d){
      u32 x=a0[4*j+d],y=a0[4*j+(d+1)%3];if((u==x&&v==y)||(u==y&&v==x)){boundary=0;break;}}
    if(boundary){a5[2*e]=u;a5[2*e+1]=v;}
  }a4[e]=boundary;}}
KERNEL void mesh_count(ARGS){volatile __local u32 buf[256];u32 i=ID;count(i<n?a3[i]:0,buf,a6);}
KERNEL void mesh_rank(ARGS){u32 i=ID;if(i<n&&a3[i]){u32 rank=a7[WG];
  for(u32 j=WG*256;j<i;++j)rank+=a3[j];a8[rank]=i;}}
KERNEL void mesh_clear(ARGS){u32 i=ID;if(i<n&&a2[i])a0[4*i+3]=0;}
KERNEL void mesh_rebuild(ARGS){u32 i=ID;if(i<k){u32 edge=a8[i],slot=a9[i];
  a0[4*slot]=a5[2*edge];a0[4*slot+1]=a5[2*edge+1];a0[4*slot+2]=step;a0[4*slot+3]=1;}}
KERNEL void mesh_select(ARGS){volatile __local u32 ids[256];volatile __local float area[256];u32 i=ID,l=LID,id=NONE;float value=0;
  if(i<n&&a0[4*i+3]){u32 a=a0[4*i],b=a0[4*i+1],c=a0[4*i+2];if(a>=3&&b>=3&&c>=3){
    value=orient(F(a1)[2*a],F(a1)[2*a+1],F(a1)[2*b],F(a1)[2*b+1],F(a1)[2*c],F(a1)[2*c+1])*0.5f;
    if(value>f0)id=i;else value=0;}}
  ids[l]=id;area[l]=value;__builtin_amdgcn_s_barrier();
  for(u32 d=128;d;d>>=1){if(l<d&&(area[l+d]>area[l]||(area[l+d]==area[l]&&ids[l+d]<ids[l]))){area[l]=area[l+d];ids[l]=ids[l+d];}__builtin_amdgcn_s_barrier();}
  if(!l){a6[WG]=ids[0];F(a7)[WG]=area[0];}}
KERNEL void mesh_centroid(ARGS){u32 i=ID;if(i==k){u32 a=a0[4*i],b=a0[4*i+1],c=a0[4*i+2];
  F(a1)[2*step]=(F(a1)[2*a]+F(a1)[2*b]+F(a1)[2*c])/3.0f;
  F(a1)[2*step+1]=(F(a1)[2*a+1]+F(a1)[2*b+1]+F(a1)[2*c+1])/3.0f;}}
