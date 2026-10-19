package service

import (
	"crypto/sha256"

	"github.com/lazu-ai/lazu/internal/types"
)

const protocolTokenShadowMemoCapacity = 128

// Only the single tokenizer worker owns this cache. Keys retain a digest, never
// request text, and every input used by the pure-text count. Request IDs and
// admission estimates are intentionally absent: each task rebuilds its patch.
type protocolTokenShadowMemoKey struct {
	text                   [sha256.Size]byte
	model                  string
	format                 types.RelayFormat
	tokenType              types.TokenType
	tools, messages, names int
	stream                 bool
}

type protocolTokenShadowMemo struct {
	counts map[protocolTokenShadowMemoKey]int
	order  [protocolTokenShadowMemoCapacity]protocolTokenShadowMemoKey
	next   int
}

func newProtocolTokenShadowMemo() *protocolTokenShadowMemo {
	return &protocolTokenShadowMemo{counts: make(map[protocolTokenShadowMemoKey]int, protocolTokenShadowMemoCapacity)}
}

func (memo *protocolTokenShadowMemo) count(task protocolTokenShadowTask, compute func() (int, error)) (int, error) {
	// Any media, including an invalid nil media entry, keeps the complete
	// existing decoder/error path. Bound the strings retained in scalar keys.
	if memo == nil || task.meta == nil || len(task.meta.Files) != 0 || len(task.model) > 256 || len(task.format) > 64 || len(task.meta.TokenType) > 64 {
		return compute()
	}
	hash := sha256.New()
	// Feed bounded chunks rather than allocating a complete []byte copy of a
	// large prompt solely for the key.
	var chunk [4096]byte
	for rest := task.meta.CombineText; len(rest) > 0; {
		n := copy(chunk[:], rest)
		_, _ = hash.Write(chunk[:n])
		rest = rest[n:]
	}
	key := protocolTokenShadowMemoKey{
		model: task.model, format: task.format, tokenType: task.meta.TokenType,
		tools: task.meta.ToolsCount, messages: task.meta.MessagesCount, names: task.meta.NameCount,
		stream: task.stream,
	}
	hash.Sum(key.text[:0])
	if count, found := memo.counts[key]; found {
		return count, nil
	}
	count, err := compute()
	if err != nil {
		return count, err
	}
	if len(memo.counts) == protocolTokenShadowMemoCapacity {
		delete(memo.counts, memo.order[memo.next])
	}
	memo.order[memo.next] = key
	memo.next = (memo.next + 1) % protocolTokenShadowMemoCapacity
	memo.counts[key] = count
	return count, nil
}
