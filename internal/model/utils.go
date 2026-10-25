package model

import (
	"github.com/lazu-ai/lazu/internal/common"
)

func shouldUpdateRedis(fromDB bool, err error) bool {
	return common.RedisEnabled && fromDB && err == nil
}
