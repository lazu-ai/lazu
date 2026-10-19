package model

import (
	"regexp"
	"sort"
	"strings"
)

// ModelOrderKey is everything the display order reads about one model.
type ModelOrderKey struct {
	Name  string
	Maker string
	// ReleaseDate is YYYY-MM-DD; empty sinks to the end of its maker.
	ReleaseDate string
}

var modelNameWord = regexp.MustCompile(`[a-z]+`)

var (
	flagshipModelWords = map[string]struct{}{"astra": {}, "fable": {}, "max": {}, "opus": {}, "pro": {}, "sol": {}, "ultra": {}}
	lightModelWords    = map[string]struct{}{"air": {}, "flash": {}, "haiku": {}, "lite": {}, "luna": {}, "mini": {}, "nano": {}, "small": {}, "turbo": {}}
)

// modelWeight orders models released the same day: flagship first, then the
// standard model, then each lighter variant. Whole words only — `gemini` ends
// in `mini` and must not read as a light model.
func modelWeight(name string) int {
	weight := 0
	for _, word := range modelNameWord.FindAllString(strings.ToLower(name), -1) {
		if _, ok := flagshipModelWords[word]; ok {
			weight--
		}
		if _, ok := lightModelWords[word]; ok {
			weight++
		}
	}
	return weight
}

// CompareModelOrder is the one display order: the operator's maker ranking,
// then newest release first, then flagship before light, then by name (so a
// dated snapshot follows its alias). Returns <0 when a reads first.
func CompareModelOrder(a, b ModelOrderKey) int {
	if ra, rb := ModelMakerRank(a.Maker), ModelMakerRank(b.Maker); ra != rb {
		return ra - rb
	}
	if a.Maker != b.Maker {
		// Two unranked makers: group by the name people read.
		return strings.Compare(strings.ToLower(ModelMakerLabel(a.Maker)), strings.ToLower(ModelMakerLabel(b.Maker)))
	}
	if a.ReleaseDate != b.ReleaseDate {
		switch {
		case a.ReleaseDate == "":
			return 1
		case b.ReleaseDate == "":
			return -1
		case a.ReleaseDate > b.ReleaseDate:
			return -1
		default:
			return 1
		}
	}
	if wa, wb := modelWeight(a.Name), modelWeight(b.Name); wa != wb {
		return wa - wb
	}
	return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
}

// SortByModelOrder sorts items in place by the display order.
func SortByModelOrder[T any](items []T, key func(T) ModelOrderKey) {
	sort.SliceStable(items, func(i, j int) bool {
		return CompareModelOrder(key(items[i]), key(items[j])) < 0
	})
}
