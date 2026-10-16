package runner

import "flag"

var pageBytesFlag = flag.Uint64("page-bytes", 0,
	"Override timing r9nano page size (power of two, >=4096); 0 preserves defaults.")
