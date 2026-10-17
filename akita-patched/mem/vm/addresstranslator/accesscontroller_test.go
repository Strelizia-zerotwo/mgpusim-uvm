package addresstranslator

import (
	"bytes"
	"github.com/sarchlab/akita/v5/mem"
	"github.com/sarchlab/akita/v5/mem/memprotocol"
	"github.com/sarchlab/akita/v5/mem/vm"
	"github.com/sarchlab/akita/v5/mem/vm/vmprotocol"
	"github.com/sarchlab/akita/v5/messaging"
	"github.com/sarchlab/akita/v5/modeling"
	"github.com/sarchlab/akita/v5/timing"
	"testing"
)

type testDeferredController struct {
	ready     bool
	completed uint64
}

func (c *testDeferredController) Admit(_ *Comp, r memprotocol.AccessReq) bool {
	return r.GetAddress() != 4096 || c.ready
}
func (c *testDeferredController) Resolve(_ *Comp, _ uint64, p vm.Page) vm.Page { return p }
func (c *testDeferredController) Complete(_ *Comp, id uint64)                  { c.completed = id }
func (c *testDeferredController) DeferBlockedAccess() bool                     { return true }
func (c *testDeferredController) ReadData(_ *Comp, _ uint64, _ []byte) []byte {
	return []byte{1, 2, 3, 4}
}
func idealTestAT() *Comp {
	reg := modeling.NewStandaloneRegistrar(timing.NewSerialEngine())
	spec := DefaultSpec()
	spec.Log2PageSize = 12
	c := MakeBuilder().WithRegistrar(reg).WithSpec(spec).WithResources(Resources{MemProviderMapper: &mem.SinglePortMapper{Port: "Memory"}, TranslationProviderMapper: &mem.SinglePortMapper{Port: "MMU"}}).Build("IdealAT")
	assignPorts(reg, c, 4)
	for _, n := range []string{"Top", "Bottom", "Translation", "Control"} {
		(&noopConn{}).PlugIn(c.GetPortByName(n))
	}
	return c
}
func TestDeferredFaultDoesNotBlockNextPageInSameAT(t *testing.T) {
	c := idealTestAT()
	controller := &testDeferredController{}
	SetAccessController(c, controller)
	defer SetAccessController(c, nil)
	top, translation := c.GetPortByName("Top"), c.GetPortByName("Translation")
	m := &parseTranslateMW{comp: c}
	a := memprotocol.ReadReq{MsgMeta: messaging.MsgMeta{ID: 101, Src: "Agent"}, Address: 4096, PID: 1, AccessByteSize: 4}
	b := a
	b.ID = 102
	b.Address = 8192
	top.Deliver(a)
	top.Deliver(b)
	if !m.translate() || len(c.State.DeferredRequests) != 1 {
		t.Fatal("fault not deferred")
	}
	tr := translation.RetrieveOutgoing().(vmprotocol.TranslationReq)
	if tr.VAddr != 8192 || c.State.Transactions[0].IncomingReqs[0].ID != 102 {
		t.Fatal("different page did not bypass waiting request")
	}
	controller.ready = true
	if !m.translate() || len(c.State.DeferredRequests) != 0 {
		t.Fatal("ready fault not resumed")
	}
	tr = translation.RetrieveOutgoing().(vmprotocol.TranslationReq)
	if tr.VAddr != 4096 || c.State.Transactions[1].IncomingReqs[0].ID != 101 {
		t.Fatal("resumed request lost identity")
	}
}
func TestFunctionalReadHookPrecedesCompletion(t *testing.T) {
	c := idealTestAT()
	controller := &testDeferredController{}
	SetAccessController(c, controller)
	defer SetAccessController(c, nil)
	c.State.InflightReqToBottom = []reqToBottomState{{ReqToBottomID: 501, ReqFromTopID: 101, ReqFromTopSrc: "Agent", ReqFromTopType: "memprotocol.ReadReq"}}
	c.GetPortByName("Bottom").Deliver(memprotocol.DataReadyRsp{MsgMeta: messaging.MsgMeta{ID: 701, RspTo: 501}, Data: []byte{9, 9, 9, 9}})
	if !(&respondPipelineMW{comp: c}).respond() {
		t.Fatal("response stalled")
	}
	rsp := c.GetPortByName("Top").RetrieveOutgoing().(memprotocol.DataReadyRsp)
	if !bytes.Equal(rsp.Data, []byte{1, 2, 3, 4}) || controller.completed != 101 {
		t.Fatal("functional data or completion hook missing")
	}
}
