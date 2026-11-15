package service

import (
	"github.com/gin-gonic/gin"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/types"
	"testing"
)

func TestMediaAdmissionFloorIsNotUsage(t *testing.T) {
	meta := &types.TokenCountMeta{CombineText: "a short question", AdmissionTokenFloor: 65536, Files: []*types.FileMeta{{FileType: types.FileTypeImage, OriginData: "not a locally supported tokenizer"}}}
	if tokens, err := EstimateRequestTPMTokens(meta, nil); err != nil || tokens != 65536 {
		t.Fatalf("reservation=%d %v", tokens, err)
	}
	c := &gin.Context{}
	if _, err := countRequestTokens(c, meta, &relaycommon.RelayInfo{}); err == nil {
		t.Fatal("admission floor treated as measured usage")
	}
}
