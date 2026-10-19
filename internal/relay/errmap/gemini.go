package errmap

import "github.com/lazu-ai/lazu/internal/types"

func MapGemini(status int, body []byte) (types.ErrorCode, []any) {
	return MapDefault(status, body)
}
