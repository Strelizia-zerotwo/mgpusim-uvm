package writethroughcache

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sarchlab/akita/v5/mem"
	"github.com/sarchlab/akita/v5/messaging"
	"github.com/sarchlab/akita/v5/modeling"
	"github.com/sarchlab/akita/v5/timing"
)

var _ = Describe("Address mapper snapshot", func() {
	It("should send addresses outside the limited range to the other port",
		func() {
			mapper := mem.NewInterleavedAddressPortMapper(4096)
			mapper.LowModules = []messaging.RemotePort{"L2[0]", "L2[1]"}
			mapper.UseAddressSpaceLimitation = true
			mapper.LowAddress = 4 * mem.GB
			mapper.HighAddress = 8 * mem.GB
			mapper.ModuleForOtherAddresses = "RDMA"

			c := MakeBuilder().
				WithRegistrar(
					modeling.NewStandaloneRegistrar(timing.NewSerialEngine())).
				WithSpec(DefaultSpec()).
				WithResources(Resources{AddressMapper: mapper}).
				Build("Cache")
			pmw := c.Middlewares()[1].(*pipelineMW)

			for _, addr := range []uint64{0, 4*mem.GB - 64, 8 * mem.GB,
				4 * mem.GB, 4*mem.GB + 4096, 8*mem.GB - 64} {
				Expect(pmw.findPort(addr)).To(Equal(mapper.Find(addr)))
			}
		})
})
