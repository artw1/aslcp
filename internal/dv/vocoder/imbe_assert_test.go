package vocoder_test

import (
	"github.com/artw1/aslcp/internal/dv/imbe"
	"github.com/artw1/aslcp/internal/dv/vocoder"
)

var _ vocoder.IMBE = (*imbe.Codec)(nil)
